// Package commandproof records successful commands against the exact staged
// Git state they verified. The receipts live in Reconc-owned state outside the
// repository, so policy gates do not depend on an editor-specific tool hook.
package commandproof

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/retention"
)

const (
	proofSchema       = "reconc.command-proof/v1"
	proofScope        = "staged-index"
	maxProofSize      = 16 * 1024
	maxProofFiles     = 4096
	maxGitOutputBytes = 16 << 20
	gitCommandTimeout = 30 * time.Second
	gitIndexLockWait  = 5 * time.Second
	gitRetryInitial   = 25 * time.Millisecond
	gitRetryMaximum   = 250 * time.Millisecond
)

// Snapshot identifies the exact commit candidate verified by a command.
type Snapshot struct {
	RepoRoot  string `json:"repo_root"`
	Head      string `json:"head"`
	IndexTree string `json:"index_tree"`
}

// Proof is a tamper-evident successful command receipt.
type Proof struct {
	Schema        string    `json:"schema"`
	Scope         string    `json:"scope"`
	RepoRoot      string    `json:"repo_root"`
	Head          string    `json:"head"`
	IndexTree     string    `json:"index_tree"`
	Command       string    `json:"command"`
	ExecutionMode string    `json:"execution_mode"`
	Outcome       string    `json:"outcome"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at"`
	Digest        string    `json:"digest"`
}

// CaptureCurrent captures a stable HEAD and index-tree identity without
// imposing working-tree cleanliness. It is used by read-only proof exporters
// that must bind loaded receipts to the exact candidate they publish.
func CaptureCurrent(repoRoot string) (Snapshot, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return Snapshot{}, err
	}
	first, err := capture(root)
	if err != nil {
		return Snapshot{}, err
	}
	second, err := capture(root)
	if err != nil {
		return Snapshot{}, err
	}
	if first != second {
		return Snapshot{}, errors.New("git HEAD or staged index changed while capturing command proof candidate")
	}
	return first, nil
}

type proofPayload struct {
	Schema        string    `json:"schema"`
	Scope         string    `json:"scope"`
	RepoRoot      string    `json:"repo_root"`
	Head          string    `json:"head"`
	IndexTree     string    `json:"index_tree"`
	Command       string    `json:"command"`
	ExecutionMode string    `json:"execution_mode"`
	Outcome       string    `json:"outcome"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at"`
}

// CaptureStagedClean captures HEAD and the index tree only when the working
// tree contains no tracked or untracked changes outside the staged candidate.
// It checks twice so a concurrent mutation cannot silently race the snapshot.
func CaptureStagedClean(repoRoot string) (Snapshot, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return Snapshot{}, err
	}
	if err := requireCleanAgainstIndex(root); err != nil {
		return Snapshot{}, err
	}
	first, err := capture(root)
	if err != nil {
		return Snapshot{}, err
	}
	if err := requireCleanAgainstIndex(root); err != nil {
		return Snapshot{}, err
	}
	second, err := capture(root)
	if err != nil {
		return Snapshot{}, err
	}
	if first != second {
		return Snapshot{}, errors.New("git HEAD or staged index changed while capturing command proof")
	}
	return first, nil
}

// VerifyStagedClean proves that the command left HEAD, the index, and the
// tracked/untracked working tree unchanged.
func VerifyStagedClean(before Snapshot) error {
	after, err := CaptureStagedClean(before.RepoRoot)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("git HEAD or staged index changed while the command ran")
	}
	return nil
}

// StoreSuccess atomically publishes one successful proof outside the repo.
func StoreSuccess(snapshot Snapshot, command, executionMode string, startedAt, completedAt time.Time) (Proof, error) {
	current, err := CaptureCurrent(snapshot.RepoRoot)
	if err != nil {
		return Proof{}, err
	}
	if current != snapshot {
		return Proof{}, errors.New("git HEAD or staged index changed before command proof publication")
	}
	proof := Proof{
		Schema:        proofSchema,
		Scope:         proofScope,
		RepoRoot:      snapshot.RepoRoot,
		Head:          snapshot.Head,
		IndexTree:     snapshot.IndexTree,
		Command:       strings.TrimSpace(command),
		ExecutionMode: executionMode,
		Outcome:       "success",
		ExitCode:      0,
		StartedAt:     startedAt.UTC(),
		CompletedAt:   completedAt.UTC(),
	}
	if err := validateProof(proof, snapshot, completedAt.UTC(), 0); err != nil {
		return Proof{}, err
	}
	digest, err := proofDigest(proof)
	if err != nil {
		return Proof{}, err
	}
	proof.Digest = digest
	data, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return Proof{}, fmt.Errorf("marshal command proof: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxProofSize {
		return Proof{}, fmt.Errorf("command proof exceeds %d bytes", maxProofSize)
	}
	dir := proofDir(snapshot.RepoRoot)
	if err := privatefs.RepairDirectory(dir); err != nil {
		return Proof{}, fmt.Errorf("secure command proof directory: %w", err)
	}
	path := filepath.Join(dir, proofIdentity(proof)+".json")
	if _, err := privatefs.WritePrivateIfChanged(path, data, 0o600); err != nil {
		return Proof{}, fmt.Errorf("write command proof: %w", err)
	}
	confirmed, err := CaptureCurrent(snapshot.RepoRoot)
	if err != nil {
		return Proof{}, fmt.Errorf("confirm command proof candidate after publication: %w", err)
	}
	if confirmed != snapshot {
		return Proof{}, errors.New("git HEAD or staged index changed after command proof publication")
	}
	retention.RunIfDue(retention.Options{RepoRoot: snapshot.RepoRoot, StateRoot: retention.ResolveStateRoot()})
	return proof, nil
}

// LoadCurrentSuccesses returns only unexpired, untampered successes for the
// current HEAD and staged index. Invalid receipts never become policy evidence.
func LoadCurrentSuccesses(repoRoot string, now time.Time) ([]Proof, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	current, err := capture(root)
	if err != nil {
		return nil, err
	}
	entries, err := boundedio.ReadDirNoSymlink(proofDir(root), maxProofFiles)
	if errors.Is(err, os.ErrNotExist) {
		return []Proof{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read command proofs: %w", err)
	}
	maxAge := retention.DefaultPolicy().CommandProofs.MaxAge
	proofs := make([]Proof, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Size() > maxProofSize {
			continue
		}
		data, readErr := boundedio.ReadRegularFile(filepath.Join(proofDir(root), entry.Name()), maxProofSize)
		if readErr != nil {
			continue
		}
		var proof Proof
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&proof); decodeErr != nil {
			continue
		}
		if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
			continue
		}
		if validateProof(proof, current, now.UTC(), maxAge) != nil {
			continue
		}
		digest, digestErr := proofDigest(proof)
		if digestErr != nil || !equalDigest(proof.Digest, digest) || entry.Name() != proofIdentity(proof)+".json" {
			continue
		}
		proofs = append(proofs, proof)
	}
	confirmed, err := capture(root)
	if err != nil {
		return nil, err
	}
	if confirmed != current {
		return nil, errors.New("git HEAD or staged index changed while loading command proofs")
	}
	return proofs, nil
}

func proofDir(repoRoot string) string {
	return filepath.Join(retention.ProjectDir(retention.ResolveStateRoot(), repoRoot), "command-proofs")
}

func capture(repoRoot string) (Snapshot, error) {
	project := retention.ProjectDir(retention.ResolveStateRoot(), repoRoot)
	if err := privatefs.RepairDirectory(project); err != nil {
		return Snapshot{}, fmt.Errorf("secure command proof state directory: %w", err)
	}
	lock, err := privatefs.OpenLock(filepath.Join(project, "command-proof.snapshot.lock"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("open command proof snapshot lock: %w", err)
	}
	unlock, err := filelock.LockContext(context.Background(), lock, filelock.DefaultTimeout)
	if err != nil {
		return Snapshot{}, errors.Join(fmt.Errorf("lock command proof snapshot: %w", err), lock.Close())
	}
	snapshot, captureErr := captureLocked(repoRoot)
	unlockErr := unlock()
	closeErr := lock.Close()
	if err := errors.Join(captureErr, unlockErr, closeErr); err != nil {
		return Snapshot{}, fmt.Errorf("capture command proof snapshot: %w", err)
	}
	return snapshot, nil
}

func captureLocked(repoRoot string) (Snapshot, error) {
	head, err := gitHead(repoRoot)
	if err != nil {
		return Snapshot{}, err
	}
	indexTree, err := gitWriteTree(repoRoot)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{RepoRoot: repoRoot, Head: head, IndexTree: indexTree}, nil
}

func gitWriteTree(repoRoot string) (string, error) {
	return gitWriteTreeWithTimeout(repoRoot, gitIndexLockWait)
}

func gitWriteTreeWithTimeout(repoRoot string, timeout time.Duration) (string, error) {
	return gitWriteTreeWithRunner(repoRoot, timeout, gitOutputContext)
}

type gitOutputRunner func(context.Context, string, ...string) (string, error)

func gitWriteTreeWithRunner(repoRoot string, timeout time.Duration, output gitOutputRunner) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	lockPath, err := gitIndexLockPath(ctx, repoRoot, output)
	if err != nil {
		return "", err
	}
	var lastContention error
	for backoff := gitRetryInitial; ; backoff = min(backoff*2, gitRetryMaximum) {
		lockObserved := gitIndexLockPresent(lockPath)
		indexTree, err := output(ctx, repoRoot, "write-tree")
		if err == nil {
			return indexTree, nil
		}
		if !gitIndexLockContention(err, lockPath, lockObserved) {
			if ctx.Err() != nil && lastContention != nil && gitIndexLockPresent(lockPath) {
				return "", fmt.Errorf("git index remained locked for command proof snapshot after %s: %w", timeout, lastContention)
			}
			return "", err
		}
		lastContention = err
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("git index remained locked for command proof snapshot after %s: %w", timeout, lastContention)
		case <-timer.C:
		}
	}
}

func gitIndexLockPath(ctx context.Context, repoRoot string, output gitOutputRunner) (string, error) {
	path, err := output(ctx, repoRoot, "rev-parse", "--git-path", "index.lock")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	return filepath.Clean(path), nil
}

func gitIndexLockContention(err error, lockPath string, lockObserved bool) bool {
	var commandErr *gitCommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	return lockObserved || gitIndexLockPresent(lockPath)
}

func gitIndexLockPresent(lockPath string) bool {
	info, statErr := os.Lstat(lockPath)
	return statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func gitHead(repoRoot string) (string, error) {
	head, err := gitOutput(repoRoot, "rev-parse", "--verify", "HEAD")
	if err == nil {
		return head, nil
	}
	if _, symbolicErr := gitOutput(repoRoot, "symbolic-ref", "--quiet", "HEAD"); symbolicErr == nil {
		return "UNBORN", nil
	}
	return "", err
}

func canonicalRepoRoot(repoRoot string) (string, error) {
	resolved, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repo root filesystem identity: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat repo root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo root is not a directory: %s", resolved)
	}
	inside, err := gitOutput(resolved, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return "", err
	}
	if inside != "true" {
		return "", fmt.Errorf("path is not inside a Git working tree: %s", resolved)
	}
	return resolved, nil
}

func requireCleanAgainstIndex(repoRoot string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := gitexec.CommandContext(ctx, repoRoot, nil, "diff", "--quiet", "--")
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("inspect tracked unstaged changes: git diff timed out after %s", gitCommandTimeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return errors.New("tracked unstaged changes exist; stage or revert them before reconc exec --staged")
		}
		return fmt.Errorf("inspect tracked unstaged changes: %w", err)
	}
	untracked, err := gitOutputBytes(repoRoot, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	if len(untracked) != 0 {
		first := string(bytes.SplitN(untracked, []byte{0}, 2)[0])
		return fmt.Errorf("untracked path %q exists; stage or remove it before reconc exec --staged", first)
	}
	return nil
}

func gitOutput(repoRoot string, args ...string) (string, error) {
	out, err := gitOutputBytes(repoRoot, args...)
	return strings.TrimSpace(string(out)), err
}

func gitOutputBytes(repoRoot string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	out, err := gitOutputBytesContext(ctx, repoRoot, args...)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), gitCommandTimeout)
	}
	return out, err
}

func gitOutputContext(ctx context.Context, repoRoot string, args ...string) (string, error) {
	out, err := gitOutputBytesContext(ctx, repoRoot, args...)
	return strings.TrimSpace(string(out)), err
}

func gitOutputBytesContext(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	cmd := gitexec.CommandContext(ctx, repoRoot, nil, args...)
	stdout := &boundedCommandOutput{limit: maxGitOutputBytes}
	stderr := &boundedCommandOutput{limit: maxGitOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("git %s output exceeds %d bytes", strings.Join(args, " "), maxGitOutputBytes)
	}
	if err != nil {
		return nil, &gitCommandError{args: append([]string(nil), args...), cause: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

type gitCommandError struct {
	args   []string
	cause  error
	stderr string
}

func (e *gitCommandError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.cause, e.stderr)
}

func (e *gitCommandError) Unwrap() error { return e.cause }

type boundedCommandOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (w *boundedCommandOutput) Write(data []byte) (int, error) {
	remaining := w.limit - w.Len()
	if remaining > 0 {
		writeCount := len(data)
		if writeCount > remaining {
			writeCount = remaining
		}
		_, _ = w.Buffer.Write(data[:writeCount])
	}
	if len(data) > remaining {
		w.overflow = true
	}
	return len(data), nil
}

func validateProof(proof Proof, snapshot Snapshot, now time.Time, maxAge time.Duration) error {
	switch {
	case proof.Schema != proofSchema:
		return errors.New("unsupported command proof schema")
	case proof.Scope != proofScope:
		return errors.New("unsupported command proof scope")
	case proof.RepoRoot != snapshot.RepoRoot || proof.Head != snapshot.Head || proof.IndexTree != snapshot.IndexTree:
		return errors.New("command proof does not match the current staged state")
	case strings.TrimSpace(proof.Command) == "":
		return errors.New("command proof command is empty")
	case proof.ExecutionMode != "direct" && proof.ExecutionMode != "shell":
		return errors.New("command proof execution mode is invalid")
	case proof.Outcome != "success" || proof.ExitCode != 0:
		return errors.New("command proof is not a successful execution")
	case proof.StartedAt.IsZero() || proof.CompletedAt.IsZero() || proof.CompletedAt.Before(proof.StartedAt):
		return errors.New("command proof timestamps are invalid")
	case proof.CompletedAt.After(now.Add(time.Minute)):
		return errors.New("command proof completion is in the future")
	case maxAge > 0 && now.Sub(proof.CompletedAt) > maxAge:
		return errors.New("command proof expired")
	}
	return nil
}

func proofDigest(proof Proof) (string, error) {
	payload := proofPayload{
		Schema: proof.Schema, Scope: proof.Scope, RepoRoot: proof.RepoRoot,
		Head: proof.Head, IndexTree: proof.IndexTree, Command: proof.Command,
		ExecutionMode: proof.ExecutionMode, Outcome: proof.Outcome, ExitCode: proof.ExitCode,
		StartedAt: proof.StartedAt, CompletedAt: proof.CompletedAt,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal command proof digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func proofIdentity(proof Proof) string {
	data := strings.Join([]string{proof.Schema, proof.Scope, proof.RepoRoot, proof.Head, proof.IndexTree, proof.Command, proof.ExecutionMode}, "\x00")
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func equalDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}
