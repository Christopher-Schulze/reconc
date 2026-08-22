package compiler

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
)

func TestCompileCapturesAuthoritativeSourcesWhileHoldingPublicationLock(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# old\n")
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	loaderEntered := make(chan struct{})
	continueLoad := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := compileRepoPolicyWithDiscovery(discovery, "test", func() (*ingest.SourceBundle, error) {
			root := repo
			close(loaderEntered)
			if release, lockErr := AcquireCompileLock(root); lockErr == nil {
				_ = release()
				return nil, errors.New("source loader ran without the compile lock held")
			} else if !strings.Contains(lockErr.Error(), "in progress") {
				return nil, lockErr
			}
			<-continueLoad
			return ingest.LoadPolicySources(repo)
		})
		result <- err
	}()
	<-loaderEntered
	writeFile(t, repo, "AGENTS.md", "# final locked snapshot\n")
	if _, err := CompileRepoPolicy(repo, "test"); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("concurrent compile = %v, want lock contention", err)
	}
	close(continueLoad)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	finalBundle, err := ingest.LoadPolicySources(repo)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := ComputeSourceDigest(finalBundle)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(repo, LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if got, _ := payload["source_digest"].(string); got != wantDigest {
		t.Fatalf("published source digest = %q, want final locked snapshot %q", got, wantDigest)
	}
}

func TestCompileRejectsRepositoryRootDriftAfterLockAcquisition(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# policy\n")
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	_, err = compileRepoPolicyWithDiscovery(discovery, "test", func() (*ingest.SourceBundle, error) {
		return &ingest.SourceBundle{RepoRoot: other}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "root changed") {
		t.Fatalf("repository-root drift error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, LockfileRelativePath)); !os.IsNotExist(err) {
		t.Fatalf("root drift published a lockfile: %v", err)
	}
}
