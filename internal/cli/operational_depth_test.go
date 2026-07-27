package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	reconbootstrap "reconc.dev/reconc/internal/bootstrap"
)

func TestBootstrapRequestRejectsEveryCrossFlagContradiction(t *testing.T) {
	base := bootstrapRequestFlags{repo: t.TempDir(), profile: reconbootstrap.ProfileMinimal, profileSet: true}
	for _, test := range []struct {
		name   string
		mutate func(*bootstrapRequestFlags)
		err    string
	}{
		{name: "missing profile", mutate: func(f *bootstrapRequestFlags) { f.profileSet = false }, err: "--profile is required"},
		{name: "two binary sources", mutate: func(f *bootstrapRequestFlags) {
			f.installBinary = true
			f.binaryPath = "binary"
		}, err: "mutually exclusive"},
		{name: "install checksum", mutate: func(f *bootstrapRequestFlags) {
			f.installBinary = true
			f.checksum = strings.Repeat("a", 64)
		}, err: "apply only to --binary"},
		{name: "checksum without binary", mutate: func(f *bootstrapRequestFlags) {
			f.checksum = strings.Repeat("a", 64)
		}, err: "--checksum requires --binary"},
		{name: "platform without binary", mutate: func(f *bootstrapRequestFlags) {
			f.platform = runtime.GOOS + "/" + runtime.GOARCH
		}, err: "--platform requires --binary"},
		{name: "binary without checksum", mutate: func(f *bootstrapRequestFlags) {
			f.binaryPath = "binary"
		}, err: "--binary requires --checksum"},
		{name: "malformed platform", mutate: func(f *bootstrapRequestFlags) {
			f.binaryPath = "binary"
			f.checksum = strings.Repeat("a", 64)
			f.platform = "darwin"
		}, err: "exactly OS/ARCH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flags := base
			test.mutate(&flags)
			if _, err := flags.request(); err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}

	request, err := base.request()
	if err != nil || request.RepoRoot != base.repo || request.Profile != reconbootstrap.ProfileMinimal || request.Binary != nil {
		t.Fatalf("minimal request = (%+v, %v)", request, err)
	}
}

func TestBootstrapRequestAcceptsVerifiedExplicitBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconc")
	body := []byte("verified binary fixture")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	digest := sha256.Sum256(body)
	flags := bootstrapRequestFlags{
		repo: t.TempDir(), profile: reconbootstrap.ProfileMinimal, profileSet: true,
		binaryPath: path, checksum: hex.EncodeToString(digest[:]),
		platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	request, err := flags.request()
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if request.Binary == nil || request.Binary.SHA256 != hex.EncodeToString(digest[:]) ||
		request.Binary.OS != runtime.GOOS || request.Binary.Arch != runtime.GOARCH {
		t.Fatalf("binary request = %+v", request)
	}
}

func TestBootstrapRequestAcceptsRunningBinarySelection(t *testing.T) {
	flags := bootstrapRequestFlags{
		repo: t.TempDir(), profile: reconbootstrap.ProfileMinimal, profileSet: true,
		installBinary: true,
	}
	request, err := flags.request()
	if err != nil {
		t.Fatalf("request running binary: %v", err)
	}
	if request.Binary == nil || request.Binary.OS != runtime.GOOS || request.Binary.Arch != runtime.GOARCH ||
		len(request.Binary.SHA256) != 64 {
		t.Fatalf("running binary selection = %+v", request.Binary)
	}
}

func TestBootstrapPlanCreatesAndReusesDeterministicOutput(t *testing.T) {
	repo := t.TempDir()
	planPath := filepath.Join(t.TempDir(), "bootstrap-plan.json")
	var output bytes.Buffer
	if err := runBootstrapPlan([]string{
		repo, "--profile", "minimal", "--output", planPath,
	}, "test-version", &output); err != nil {
		t.Fatalf("runBootstrapPlan(text): %v", err)
	}
	for _, expected := range []string{"Bootstrap plan:", "Profile: minimal", "Plan file:", "(created)"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("plan output omitted %q:\n%s", expected, output.String())
		}
	}

	output.Reset()
	if err := runBootstrapPlan([]string{
		repo, "--profile", "minimal", "--output", planPath, "--json",
	}, "test-version", &output); err != nil {
		t.Fatalf("runBootstrapPlan(JSON): %v", err)
	}
	var plan reconbootstrap.Plan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan JSON: %v", err)
	}
	if plan.RepoRoot == "" || plan.PlanDigest == "" || plan.Selection.Profile != reconbootstrap.ProfileMinimal {
		t.Fatalf("incomplete plan JSON: %+v", plan)
	}
}

func TestBootstrapInspectRendersTextAndJSONContracts(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/project\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write Go marker: %v", err)
	}
	for _, test := range []struct {
		name string
		args []string
		json bool
	}{
		{name: "text", args: []string{repo}},
		{name: "json", args: []string{repo, "--json"}, json: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := runBootstrapInspect(test.args, &output); err != nil {
				t.Fatalf("runBootstrapInspect: %v", err)
			}
			if test.json {
				var payload map[string]interface{}
				if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
					t.Fatalf("decode inspect JSON: %v", err)
				}
				if payload["repo_root"] == nil {
					t.Fatalf("inspect JSON omitted repo_root: %s", output.String())
				}
			} else {
				for _, expected := range []string{"Repository:", "Detection:", "Stacks:", "go", "Binary:"} {
					if !strings.Contains(output.String(), expected) {
						t.Fatalf("inspect text omitted %q:\n%s", expected, output.String())
					}
				}
			}
		})
	}
}

func TestRenderWhyMCPAllOutputModes(t *testing.T) {
	contract := map[string]interface{}{
		"unclassified": "deny",
		"tools": []interface{}{
			map[string]interface{}{
				"platform": "cursor", "tool": "write", "effect": "repository_write",
				"source_path": ".reconc.yml", "server_fingerprint": "sha256:abc",
			},
			"invalid-entry",
		},
	}
	for _, test := range []struct {
		name    string
		raw     interface{}
		json    bool
		terse   bool
		want    string
		wantErr string
	}{
		{name: "absent text", want: "not configured"},
		{name: "absent JSON", json: true, want: "null"},
		{name: "text", raw: contract, want: "cursor:write@sha256:abc"},
		{name: "terse", raw: contract, terse: true, want: "unclassified=deny mappings=2"},
		{name: "JSON", raw: contract, json: true, want: `"unclassified": "deny"`},
		{name: "conflicting formats", raw: contract, json: true, terse: true, wantErr: "mutually exclusive"},
		{name: "invalid contract", raw: "invalid", wantErr: "contract is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := renderWhyMCP(test.raw, test.json, test.terse, &output)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected %q error, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil || !strings.Contains(output.String(), test.want) {
				t.Fatalf("renderWhyMCP = output %q, error %v; want %q", output.String(), err, test.want)
			}
		})
	}
}

func TestTaskCheckDoneRendersReadyAndBlockingContracts(t *testing.T) {
	repo := t.TempDir()
	writeCLIFile(t, repo, ".reconc.yml", "task_lifecycle:\n  profile: sections-v1\n")
	writeCLIFile(t, repo, "docs/tasks.md", "# TASK Control Plane\n\n## Active\n\n- [~] 001 Coverage -> tasks/001-coverage.md\n\n## Queue\n\n## Blocked\n\n## Done\n")
	detailPath := "docs/tasks/001-coverage.md"
	detail := "# TASK 001: Coverage\n\n## Why\n\nProve it.\n\n## Acceptance\n\n- Covered.\n\n## Sub-Tasks\n\n- [ ] Add tests\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n"
	writeCLIFile(t, repo, detailPath, detail)

	var output bytes.Buffer
	err := runTaskCheckDone([]string{repo, "--json"}, &output)
	if err == nil || !strings.Contains(output.String(), `"valid": false`) {
		t.Fatalf("incomplete TASK check = output %q, error %v", output.String(), err)
	}

	writeCLIFile(t, repo, detailPath, strings.Replace(detail, "- [ ] Add tests", "- [x] Add tests", 1))
	output.Reset()
	if err := runTaskCheckDone([]string{repo, "--task", "001"}, &output); err != nil {
		t.Fatalf("complete TASK check: %v", err)
	}
	if output.String() != "TASK completion check: ready\n" {
		t.Fatalf("ready output = %q", output.String())
	}
}

func TestTaskRecoverAndArchiveCLIContracts(t *testing.T) {
	t.Run("recover without interrupted transaction", func(t *testing.T) {
		var output bytes.Buffer
		err := runTaskRecover([]string{t.TempDir(), "--json"}, &output)
		if err == nil || !strings.Contains(err.Error(), "task-transaction.json") {
			t.Fatalf("missing-transaction recovery = output %q, error %v", output.String(), err)
		}
	})

	t.Run("archive completed active task", func(t *testing.T) {
		repo := t.TempDir()
		writeCLIFile(t, repo, ".reconc.yml", "task_lifecycle:\n  profile: sections-v1\n")
		writeCLIFile(t, repo, "docs/tasks.md", "# TASK Control Plane\n\n## Active\n\n- [~] 001 Coverage -> tasks/001-coverage.md\n\n## Queue\n\n## Blocked\n\n## Done\n")
		writeCLIFile(t, repo, "docs/tasks/001-coverage.md", "# TASK 001: Coverage\n\n## Why\n\nProve it.\n\n## Acceptance\n\n- Covered.\n\n## Sub-Tasks\n\n- [x] Add tests\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n")

		var output bytes.Buffer
		if err := runTaskArchive([]string{repo, "--json"}, &output); err != nil {
			t.Fatalf("runTaskArchive: %v", err)
		}
		if !strings.Contains(output.String(), `"action": "archive"`) ||
			!strings.Contains(output.String(), `"state": "done"`) {
			t.Fatalf("archive JSON = %q", output.String())
		}
		if _, err := os.Stat(filepath.Join(repo, "docs", "tasks", "done", "001-coverage.md")); err != nil {
			t.Fatalf("archived detail missing: %v", err)
		}
	})
}

func writeCLIFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", relative, err)
	}
}
