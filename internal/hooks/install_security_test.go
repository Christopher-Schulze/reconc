package hooks

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestInstallRejectsSymlinkedManagedParentDirectories(t *testing.T) {
	tests := []struct {
		kind   string
		target string
	}{
		{kind: KindClaudeCode, target: ClaudeCodeSettingsPath},
		{kind: KindCodex, target: CodexHooksPath},
		{kind: KindGitHubCopilot, target: GitHubCopilotHooksPath},
		{kind: KindCursor, target: CursorHooksPath},
		{kind: KindOpenCode, target: OpenCodePluginPath},
		{kind: KindDevinCLI, target: DevinHooksPath},
		{kind: KindGrok, target: GrokHooksPath},
		{kind: KindAntigravity, target: AntigravityHooksPath},
		{kind: KindKilo, target: KiloPluginPath},
		{kind: KindOMP, target: OMPExtensionPath},
		{kind: KindPi, target: PiExtensionPath},
		{kind: KindZCode, target: ZCodeConfigPath},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			outside := t.TempDir()
			firstComponent := strings.Split(filepath.ToSlash(test.target), "/")[0]
			createDirectoryLinkForTest(t, outside, filepath.Join(repo, firstComponent))
			if _, err := Install(test.kind, repo, true); err == nil || !strings.Contains(err.Error(), "resolves outside") {
				t.Fatalf("expected parent-symlink rejection, got %v", err)
			}
			outsideTarget := filepath.Join(outside, filepath.FromSlash(strings.TrimPrefix(test.target, firstComponent+"/")))
			if _, err := os.Lstat(outsideTarget); !os.IsNotExist(err) {
				t.Fatalf("external target was created or became unreadable: %v", err)
			}
		})
	}
}

func TestScaffoldSyncPreflightsEveryManagedParentBeforeWriting(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	createDirectoryLinkForTest(t, outside, filepath.Join(root, ".claude"))
	if _, err := SyncRepoRootScaffold(root); err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("expected scaffold parent-symlink rejection, got %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("scaffold sync wrote outside its root: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, GitPreCommitScaffoldPath)); !os.IsNotExist(err) {
		t.Fatalf("scaffold sync wrote before completing preflight: %v", err)
	}
}

func TestInstallRejectsOversizedManagedTargets(t *testing.T) {
	for _, test := range []struct {
		kind   string
		target string
	}{
		{kind: KindClaudeCode, target: ClaudeCodeSettingsPath},
		{kind: KindGitHubCopilot, target: GitHubCopilotHooksPath},
		{kind: KindOpenCode, target: OpenCodePluginPath},
		{kind: KindGrok, target: GrokHooksPath},
		{kind: KindOMP, target: OMPExtensionPath},
		{kind: KindPi, target: PiExtensionPath},
		{kind: KindZCode, target: ZCodeConfigPath},
	} {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			target := filepath.Join(repo, filepath.FromSlash(test.target))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			file, err := os.Create(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(maxManagedArtifactBytes + 1); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(test.kind, repo, true); err == nil || !strings.Contains(err.Error(), "managed-artifact limit") {
				t.Fatalf("expected bounded-read rejection, got %v", err)
			}
		})
	}
}

func TestMalformedConfigBackupFullDigestCollisionFailsClosed(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	source := []byte("malformed")
	backup, err := backupMalformedConfig(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if suffix := strings.TrimPrefix(backup, target+".reconc-backup-"); len(suffix) != sha256.Size*2 {
		t.Fatalf("backup digest length = %d, want %d", len(suffix), sha256.Size*2)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(backup)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("malformed-config backup mode = %o, want 600", info.Mode().Perm())
		}
	}
	if err := os.WriteFile(backup, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backupMalformedConfig(target, source); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected colliding backup rejection, got %v", err)
	}
}

func TestMalformedConfigBackupSkipsCollidingLegacyPrefix(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	source := []byte("malformed")
	legacy, full := malformedConfigBackupPaths(target, sha256.Sum256(source))
	foreign := []byte("different legacy content")
	if err := os.WriteFile(legacy, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := backupMalformedConfig(target, source)
	if err != nil || got != full {
		t.Fatalf("collision-resistant backup = %q, %v; want %q", got, err, full)
	}
	legacyBody, err := os.ReadFile(legacy)
	if err != nil || string(legacyBody) != string(foreign) {
		t.Fatalf("legacy collision changed: body=%q err=%v", legacyBody, err)
	}
}

func TestMalformedConfigBackupPathsSeparateSyntheticPrefixCollision(t *testing.T) {
	var left, right [sha256.Size]byte
	copy(left[:4], []byte{1, 2, 3, 4})
	copy(right[:4], left[:4])
	left[4] = 5
	right[4] = 6
	leftLegacy, leftFull := malformedConfigBackupPaths("settings.json", left)
	rightLegacy, rightFull := malformedConfigBackupPaths("settings.json", right)
	if leftLegacy != rightLegacy || leftFull == rightFull {
		t.Fatalf("synthetic collision paths = (%q, %q), (%q, %q)", leftLegacy, leftFull, rightLegacy, rightFull)
	}
}

func TestMalformedConfigBackupConcurrentIdenticalAttemptsConverge(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	source := []byte("malformed")
	const attempts = 16
	results := make(chan string, attempts)
	errorsByAttempt := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			backup, err := backupMalformedConfig(target, source)
			results <- backup
			errorsByAttempt <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsByAttempt)
	for err := range errorsByAttempt {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := ""
	for backup := range results {
		if want == "" {
			want = backup
		}
		if backup != want {
			t.Fatalf("concurrent backup path = %q, want %q", backup, want)
		}
	}
	body, err := os.ReadFile(want)
	if err != nil || string(body) != string(source) {
		t.Fatalf("concurrent backup body = %q, %v", body, err)
	}
}

func TestMalformedConfigBackupConcurrentLegacyPrefixCollisionSeparates(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	left, right := findSHA256PrefixCollision(t)
	start := make(chan struct{})
	results := make(chan string, 2)
	errorsByAttempt := make(chan error, 2)
	var wait sync.WaitGroup
	for _, source := range [][]byte{left, right} {
		wait.Add(1)
		go func(source []byte) {
			defer wait.Done()
			<-start
			backup, err := backupMalformedConfig(target, source)
			results <- backup
			errorsByAttempt <- err
		}(source)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsByAttempt)
	for err := range errorsByAttempt {
		if err != nil {
			t.Fatal(err)
		}
	}
	paths := make([]string, 0, 2)
	for backup := range results {
		paths = append(paths, backup)
	}
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatalf("colliding prefix backups = %v", paths)
	}
	for _, source := range [][]byte{left, right} {
		_, full := malformedConfigBackupPaths(target, sha256.Sum256(source))
		body, err := os.ReadFile(full)
		if err != nil || string(body) != string(source) {
			t.Fatalf("full-digest collision backup %q = %q, %v", full, body, err)
		}
	}
}

func findSHA256PrefixCollision(t *testing.T) ([]byte, []byte) {
	t.Helper()
	seen := make(map[[4]byte]uint32)
	for index := uint32(0); index < 1<<20; index++ {
		body := []byte(strconv.FormatUint(uint64(index), 10))
		digest := sha256.Sum256(body)
		var prefix [4]byte
		copy(prefix[:], digest[:4])
		if previous, ok := seen[prefix]; ok {
			return []byte(strconv.FormatUint(uint64(previous), 10)), body
		}
		seen[prefix] = index
	}
	t.Fatal("no SHA-256 32-bit prefix collision found within deterministic search bound")
	return nil, nil
}
