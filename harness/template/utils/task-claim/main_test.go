package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, root string, rel string, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestResolveTaskFromCurrent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "docs/tasks.md", `# Tasks

Current: TASK-0042-Foo-Bar -> tasks/TASK-0042-Foo-Bar.md

- [ ] TASK-0042-Foo-Bar - foo bar -> tasks/TASK-0042-Foo-Bar.md
`)
	got, err := resolveTask(root, "")
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got != "TASK-0042-Foo-Bar" {
		t.Fatalf("expected TASK-0042-Foo-Bar, got %q", got)
	}
}

func TestReconcBinaryRelUsesStablePlatformName(t *testing.T) {
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	want := "tools/reconc/dist/reconc-" + runtime.GOOS + "-" + runtime.GOARCH + extension
	if got := reconcBinaryRel(); got != want {
		t.Fatalf("reconcBinaryRel() = %q, want %q", got, want)
	}
}

func TestResolveTaskExplicitOverride(t *testing.T) {
	root := t.TempDir()
	got, err := resolveTask(root, "TASK-9999-Override")
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got != "TASK-9999-Override" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveTaskMissingFile(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveTask(root, ""); err == nil {
		t.Fatal("expected error for missing tasks.md")
	}
}

func TestResolveTaskMissingCurrent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "docs/tasks.md", "# Tasks\n\n- [x] done -> tasks/done/x.md\n")
	if _, err := resolveTask(root, ""); err == nil || !strings.Contains(err.Error(), "Current") {
		t.Fatalf("expected Current-missing error, got %v", err)
	}
}

func TestLoadBindingsHappy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bindings.yaml")
	writeFixture(t, root, "bindings.yaml", `default_claims:
  - always-on
bindings:
  - match: TASK-0011
    claims:
      - spec-edit-authorized
  - match: README
    claims:
      - readme-edit-authorized
`)
	cfg, err := loadBindings(path)
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	if len(cfg.DefaultClaims) != 1 || cfg.DefaultClaims[0] != "always-on" {
		t.Fatalf("default_claims: %v", cfg.DefaultClaims)
	}
	if len(cfg.Bindings) != 2 {
		t.Fatalf("bindings: %v", cfg.Bindings)
	}
}

func TestLoadBindingsMissing(t *testing.T) {
	if _, err := loadBindings(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadBindingsInvalidYaml(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadBindings(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestClaimsForTaskMatchesSubstring(t *testing.T) {
	cfg := bindingsConfig{
		Bindings: []binding{
			{Match: "TASK-0011", Claims: []string{"spec-edit-authorized"}},
			{Match: "Workflow", Claims: []string{"agents-md-edit-authorized"}},
		},
	}
	got := claimsForTask("TASK-0011-Policy-Compiler-Workflow-Bits", cfg)
	want := []string{"agents-md-edit-authorized", "spec-edit-authorized"}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestClaimsForTaskNoMatchYieldsEmpty(t *testing.T) {
	cfg := bindingsConfig{
		Bindings: []binding{
			{Match: "OnlyForReadme", Claims: []string{"readme-edit-authorized"}},
		},
	}
	got := claimsForTask("TASK-0001-Other", cfg)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestClaimsForTaskAppliesDefaultClaims(t *testing.T) {
	cfg := bindingsConfig{
		DefaultClaims: []string{"always"},
		Bindings:      []binding{},
	}
	got := claimsForTask("TASK-9999-Anything", cfg)
	if len(got) != 1 || got[0] != "always" {
		t.Fatalf("got %v", got)
	}
}

func TestClaimsForTaskDeduplicates(t *testing.T) {
	cfg := bindingsConfig{
		DefaultClaims: []string{"X"},
		Bindings: []binding{
			{Match: "TASK", Claims: []string{"X", "Y"}},
		},
	}
	got := claimsForTask("TASK-0001-Whatever", cfg)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "X" || got[1] != "Y" {
		t.Fatalf("got %v", got)
	}
}

func TestClaimsForTaskIgnoresEmptyMatch(t *testing.T) {
	cfg := bindingsConfig{
		Bindings: []binding{
			{Match: "", Claims: []string{"should-not-apply"}},
			{Match: "TASK", Claims: []string{"applies"}},
		},
	}
	got := claimsForTask("TASK-0001-X", cfg)
	if len(got) != 1 || got[0] != "applies" {
		t.Fatalf("got %v", got)
	}
}

func TestClaimsForTaskIgnoresEmptyClaimEntries(t *testing.T) {
	cfg := bindingsConfig{
		DefaultClaims: []string{"", "real"},
		Bindings: []binding{
			{Match: "TASK", Claims: []string{"", "matched"}},
		},
	}
	got := claimsForTask("TASK-0001-X", cfg)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "matched" || got[1] != "real" {
		t.Fatalf("got %v", got)
	}
}

func TestAssertClaimsNoOpOnEmpty(t *testing.T) {
	root := t.TempDir()
	if err := assertClaims(root, "TASK-0001-X", nil); err != nil {
		t.Fatalf("expected no-op success on empty claims, got %v", err)
	}
}

func TestAssertClaimsRejectsMissingBinary(t *testing.T) {
	root := t.TempDir()
	err := assertClaims(root, "TASK-0001-X", []string{"some-claim"})
	if err == nil || !strings.Contains(err.Error(), "reconc binary missing") {
		t.Fatalf("expected missing-binary error, got %v", err)
	}
}

func TestAssertClaimsForwardsToBinaryStub(t *testing.T) {
	root := t.TempDir()
	binPath := filepath.Join(root, filepath.FromSlash(reconcBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logPath := filepath.Join(root, "calls.log")
	stub := "#!/bin/sh\necho \"$@\" >> " + logPath + "\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if err := assertClaims(root, "TASK-0001-X", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("assertClaims: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "hook claim "+root+" alpha") || !strings.Contains(log, "hook claim "+root+" beta") {
		t.Fatalf("stub did not see expected calls:\n%s", log)
	}
}

func TestAssertClaimsPropagatesBinaryFailure(t *testing.T) {
	root := t.TempDir()
	binPath := filepath.Join(root, filepath.FromSlash(reconcBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stub := "#!/bin/sh\necho synthetic-claim-failure >&2\nexit 7\n"
	if err := os.WriteFile(binPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	err := assertClaims(root, "TASK-0001-X", []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "reconc hook claim alpha failed") {
		t.Fatalf("expected forwarded failure, got %v", err)
	}
}
