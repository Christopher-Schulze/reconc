package compiler

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
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

func TestCompileRejectsUndiscoveredLoaderContradictionBeforeLock(t *testing.T) {
	repo := t.TempDir()
	discovery := ingest.DiscoveryResult{StartPath: repo, RepoRoot: repo, Discovered: false}
	loaderCalled := false
	compiled, err := compileRepoPolicyWithDiscovery(discovery, "test", func() (*ingest.SourceBundle, error) {
		loaderCalled = true
		return &ingest.SourceBundle{RepoRoot: repo, Discovery: discovery}, nil
	})
	if compiled != nil || err == nil {
		t.Fatalf("discovery/load contradiction = compiled=%#v err=%v, want typed error", compiled, err)
	}
	var sourceErr *rerrors.PolicySourceError
	if !errors.As(err, &sourceErr) || !strings.Contains(err.Error(), "discovery reported no policy markers") || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("discovery/load contradiction error = %T %v, want actionable *PolicySourceError", err, err)
	}
	if !loaderCalled {
		t.Fatal("contradiction loader was not called")
	}
	if _, statErr := os.Lstat(filepath.Join(repo, ".reconc")); !os.IsNotExist(statErr) {
		t.Fatalf("undiscovered compile acquired the publication lock: %v", statErr)
	}
}

func TestPortableSourcePathIsPlatformIndependent(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "AGENTS.md", want: true},
		{path: ".reconc.yml", want: true},
		{path: "policies/rules.yml", want: true},
		{path: ".reconc/runtimes/custom.json", want: true},
		{path: "preset:go-assurance", want: true},
		{path: ingest.GlobalPolicySourcePath, want: true},
		{path: ""},
		{path: " AGENTS.md"},
		{path: "AGENTS.md "},
		{path: "."},
		{path: "./AGENTS.md"},
		{path: "policies/./rules.yml"},
		{path: "policies//rules.yml"},
		{path: "policies/../rules.yml"},
		{path: "../rules.yml"},
		{path: `policies\rules.yml`},
		{path: `..\rules.yml`},
		{path: "/etc/passwd"},
		{path: `\etc\passwd`},
		{path: `C:\Windows\policy.yml`},
		{path: "C:/Windows/policy.yml"},
		{path: "C:relative.yml"},
		{path: `\\server\share\policy.yml`},
		{path: "//server/share/policy.yml"},
		{path: "policies/rules.yml/"},
	}
	for _, test := range tests {
		if got := portableSourcePath(test.path); got != test.want {
			t.Errorf("portableSourcePath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestValidateLockfileEnvelopeRejectsPlatformSpecificSourcePaths(t *testing.T) {
	for _, sourcePath := range []string{
		`C:\Windows\policy.yml`, "C:relative.yml", `\\server\share\policy.yml`,
		`policies\rules.yml`, "policies/./rules.yml", "policies/../rules.yml",
	} {
		payload := map[string]interface{}{
			"$schema":        LockfileSchema(),
			"format_version": LockfileFormatVersion,
			"repo_root":      PortableRepoRoot,
			"discovery": map[string]interface{}{
				"repo_root": PortableRepoRoot, "start_path": PortableRepoRoot,
			},
			"sources": []interface{}{map[string]interface{}{
				"kind": string(policy.SourcePolicyFile), "path": sourcePath,
				"content_sha256": strings.Repeat("a", 64),
			}},
		}
		digest, err := ComputeLockDigest(payload)
		if err != nil {
			t.Fatal(err)
		}
		payload["lock_digest"] = digest
		if err := ValidateLockfileEnvelope(payload); err == nil || !strings.Contains(err.Error(), "path must be portable") {
			t.Errorf("ValidateLockfileEnvelope accepted %q: %v", sourcePath, err)
		}
	}
}
