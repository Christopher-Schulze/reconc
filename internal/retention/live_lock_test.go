package retention

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/filelock"
)

const retentionLockHolderEnvironment = "RECONC_RETENTION_LOCK_HOLDER"

func TestRunPreservesCrossProcessHeldLockAcrossClassAndTotalPressure(t *testing.T) {
	repository := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	path := filepath.Join(ProjectDir(stateRoot, repository), "locks", "held.lock")
	writeTimed(t, path, []byte("held"), now.Add(-48*time.Hour))
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	holder := startRetentionLockHolder(t, path)
	policy := DefaultPolicy()
	policy.Locks = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 0

	dryReport := Run(Options{
		RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir(), DryRun: true,
	})
	if requireClassReport(t, dryReport, "locks").FilesDeleted != 0 || requireClassReport(t, dryReport, "state-total").FilesDeleted != 0 {
		t.Fatalf("dry-run projected deletion of a held lock: %+v", dryReport)
	}
	report := Run(Options{RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected state uses") {
		t.Fatalf("held over-budget lock was not reported: %+v", report)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("held lock identity changed: before=%v after=%v err=%v", before, after, err)
	}
	contender, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filelock.TryLock(contender); err == nil || !filelock.IsContended(err) {
		_ = contender.Close()
		t.Fatalf("retention split the held lock inode: %v", err)
	}
	if err := contender.Close(); err != nil {
		t.Fatal(err)
	}
	holder.release(t)

	report = Run(Options{RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors after release: %v", report.Errors)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released stale lock survived: %v", err)
	}
}

func requireClassReport(t *testing.T, report Report, name string) ClassReport {
	t.Helper()
	for _, class := range report.Classes {
		if class.Name == name {
			return class
		}
	}
	t.Fatalf("retention report omitted %s: %+v", name, report)
	return ClassReport{}
}

func TestRemoveCandidateAcquiresReleasedLockOnDiscoveredIdentity(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "released.lock")
	writeTimed(t, path, nil, time.Now().Add(-48*time.Hour))
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	holder := startRetentionLockHolder(t, path)
	report := Report{}
	removed := removeCandidateWithHooks(candidate{path: path, info: info, probeLock: true}, false, &report, candidateRemovalHooks{
		afterValidation: func(candidate) error {
			return holder.releaseError()
		},
	})
	if !removed || len(report.Errors) != 0 {
		t.Fatalf("released lock was not safely removed: removed=%v errors=%v", removed, report.Errors)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock survived: %v", err)
	}
}

func TestExplicitActiveSessionIgnoresAgeAndMissingState(t *testing.T) {
	for _, test := range []struct {
		name         string
		writeSession bool
	}{
		{name: "old state", writeSession: true},
		{name: "state not written"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			stateRoot := t.TempDir()
			now := time.Now().UTC()
			project := ProjectDir(stateRoot, repository)
			sessionID := "explicit-active"
			if test.writeSession {
				writeTimed(t, filepath.Join(project, "sessions", sessionID+".json"), []byte("state"), now.Add(-48*time.Hour))
			}
			for _, path := range []string{
				filepath.Join(project, "reports", sessionID+".json"),
				filepath.Join(project, "locks", sessionID+".lock"),
			} {
				writeTimed(t, path, []byte("active"), now.Add(-48*time.Hour))
			}
			policy := DefaultPolicy()
			policy.Sessions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
			policy.Reports = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
			policy.Locks = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
			policy.StateTotalBytes = 0

			report := Run(Options{
				RepoRoot: repository, StateRoot: stateRoot, ActiveSession: sessionID,
				Policy: policy, Now: now, TempRoot: t.TempDir(),
			})
			for _, path := range []string{
				filepath.Join(project, "reports", sessionID+".json"),
				filepath.Join(project, "locks", sessionID+".lock"),
			} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("explicit active artifact was removed: %s: %v", path, err)
				}
			}
			if test.writeSession {
				if _, err := os.Lstat(filepath.Join(project, "sessions", sessionID+".json")); err != nil {
					t.Fatalf("old explicit session was removed: %v", err)
				}
			}
			if !strings.Contains(strings.Join(report.Errors, "\n"), "protected state uses") {
				t.Fatalf("protected pressure was not reported: %+v", report)
			}
		})
	}
}

func TestPassiveActiveSessionStillExpires(t *testing.T) {
	repository := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repository)
	sessionID := "passive-stale"
	for _, path := range []string{
		filepath.Join(project, "sessions", sessionID+".json"),
		filepath.Join(project, "reports", sessionID+".json"),
		filepath.Join(project, "locks", sessionID+".lock"),
	} {
		writeTimed(t, path, nil, now.Add(-48*time.Hour))
	}
	writeTimed(t, filepath.Join(project, "active-session.txt"), []byte(sessionID+"\n"), now.Add(-48*time.Hour))
	policy := DefaultPolicy()
	policy.Sessions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.Reports = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.Locks = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Hour}
	policy.StateTotalBytes = 0

	report := Run(Options{RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if len(report.Errors) != 0 {
		t.Fatalf("passive retention errors: %v", report.Errors)
	}
	for _, path := range []string{
		filepath.Join(project, "sessions", sessionID+".json"),
		filepath.Join(project, "reports", sessionID+".json"),
		filepath.Join(project, "locks", sessionID+".lock"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale passive artifact survived: %s: %v", path, err)
		}
	}
}

func TestProjectRootRetentionUsesCrossProcessLockLiveness(t *testing.T) {
	for _, lockRelative := range []string{".retention.lock", filepath.Join("locks", "live.lock")} {
		t.Run(lockRelative, func(t *testing.T) {
			repository := t.TempDir()
			stateRoot := t.TempDir()
			now := time.Now().UTC()
			root := filepath.Join(stateRoot, "projects", "1111111111111111")
			lockPath := filepath.Join(root, lockRelative)
			writeTimed(t, lockPath, nil, now.Add(-60*24*time.Hour))
			if err := os.Chtimes(root, now.Add(-60*24*time.Hour), now.Add(-60*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			holder := startRetentionLockHolder(t, lockPath)
			policy := DefaultPolicy()
			policy.ProjectRoots = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Hour}

			report := Run(Options{RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
			if _, err := os.Lstat(root); err != nil {
				t.Fatalf("aged root with live lock was removed: %v; report=%+v", err, report)
			}
			if !strings.Contains(strings.Join(report.Errors, "\n"), "protected project state uses") {
				t.Fatalf("live over-budget root was not reported: %+v", report)
			}
			holder.release(t)

			report = Run(Options{RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
			if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("released stale root survived: %v; report=%+v", err, report)
			}
		})
	}
}

func TestProjectRootRetentionRemovesStalePassivePointer(t *testing.T) {
	repository := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	root := filepath.Join(stateRoot, "projects", "1111111111111111")
	writeTimed(t, filepath.Join(root, "sessions", "stale.json"), nil, now.Add(-60*24*time.Hour))
	writeTimed(t, filepath.Join(root, "active-session.txt"), []byte("stale\n"), now.Add(-60*24*time.Hour))
	if err := os.Chtimes(root, now.Add(-60*24*time.Hour), now.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Hour}
	policy.Locks.MaxAge = time.Hour

	report := Run(Options{RepoRoot: repository, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root protected only by stale passive pointer survived: %v; report=%+v", err, report)
	}
}

type retentionLockHolder struct {
	command  *exec.Cmd
	stdin    io.WriteCloser
	cancel   context.CancelFunc
	released bool
}

func startRetentionLockHolder(t *testing.T, path string) *retentionLockHolder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRetentionLockHolderProcess$")
	command.Env = append(os.Environ(), retentionLockHolderEnvironment+"=1", "RECONC_RETENTION_LOCK_PATH="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	holder := &retentionLockHolder{command: command, stdin: stdin, cancel: cancel}
	t.Cleanup(func() {
		if !holder.released {
			_ = stdin.Close()
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		cancel()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("lock holder startup: line=%q err=%v", line, err)
	}
	return holder
}

func (holder *retentionLockHolder) release(t *testing.T) {
	t.Helper()
	if err := holder.releaseError(); err != nil {
		t.Fatal(err)
	}
}

func (holder *retentionLockHolder) releaseError() error {
	if holder.released {
		return errors.New("lock holder already released")
	}
	closeErr := holder.stdin.Close()
	waitErr := holder.command.Wait()
	holder.cancel()
	holder.released = true
	return errors.Join(closeErr, waitErr)
}

func TestRetentionLockHolderProcess(t *testing.T) {
	if os.Getenv(retentionLockHolderEnvironment) != "1" {
		return
	}
	path := os.Getenv("RECONC_RETENTION_LOCK_PATH")
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.Lock(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "locked"); err != nil {
		_ = unlock()
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := io.ReadAll(os.Stdin); err != nil {
		_ = unlock()
		_ = file.Close()
		t.Fatal(err)
	}
	if err := errors.Join(unlock(), file.Close()); err != nil {
		t.Fatal(err)
	}
}
