package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestPrintUsageMatchesCommandFirstFlagOrder(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	if !strings.Contains(output.String(), "task-claim <show|assert> [--task TASK-NNNN-Name]") {
		t.Fatalf("usage does not document the accepted command-first flag order:\n%s", output.String())
	}
}

func TestParseCommandDocumentedForms(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want commandOptions
	}{
		{name: "show current", args: []string{"show"}, want: commandOptions{command: "show"}},
		{name: "assert current", args: []string{"assert"}, want: commandOptions{command: "assert"}},
		{
			name: "show override",
			args: []string{"show", "--task", "TASK-0099-Explicit"},
			want: commandOptions{command: "show", taskOverride: "TASK-0099-Explicit"},
		},
		{
			name: "assert override equals",
			args: []string{"assert", "--task=TASK-9999-Explicit"},
			want: commandOptions{command: "assert", taskOverride: "TASK-9999-Explicit"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if err != nil {
				t.Fatalf("parseCommand: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCommandRejectsInvalidInput(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"show", "--unknown"},
		{"show", "trailing"},
		{"assert", "--task", "not-a-task"},
		{"assert", "--task"},
	}
	for _, args := range tests {
		if _, err := parseCommand(args); err == nil {
			t.Fatalf("expected error for args %q", args)
		}
	}
}

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

func copyFixture(t *testing.T, source string, root string, rel string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture source: %v", err)
	}
	writeFixture(t, root, rel, string(content))
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

func TestFindRepoRootFromNestedHarnessDirectory(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, tasksRel, "# Tasks\n")
	writeFixture(t, root, bindingsRel, "default_claims: []\nbindings: []\n")
	nested := filepath.Join(root, "tools", "reconc", "harness", "template", "utils", "task-claim")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	got, err := findRepoRoot(nested)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if got != root {
		t.Fatalf("got %q want %q", got, root)
	}
}

func TestFindRepoRootFailsClosedWithoutBothMarkers(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, tasksRel, "# Tasks\n")
	if _, err := findRepoRoot(root); err == nil || !strings.Contains(err.Error(), bindingsRel) {
		t.Fatalf("expected missing bindings marker error, got %v", err)
	}
}

func TestDocumentedGoCInvocationFromRepoRoot(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	templateModule := filepath.Clean(filepath.Join(workingDir, "..", ".."))
	root := t.TempDir()
	for _, rel := range []string{
		"go.mod",
		"go.sum",
		"utils/task-claim/main.go",
		"config/workflow/task-claim-bindings.yaml",
	} {
		copyFixture(t, filepath.Join(templateModule, filepath.FromSlash(rel)), root, filepath.ToSlash(filepath.Join("tools/reconc/harness/template", rel)))
	}
	writeFixture(t, root, tasksRel, "# Tasks\n")

	cmd := exec.Command(
		"go",
		"-C", filepath.FromSlash("tools/reconc/harness/template"),
		"run", "./utils/task-claim",
		"show", "--task", "TASK-9999-Standalone-Probe",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("documented go -C invocation failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "task: TASK-9999-Standalone-Probe") {
		t.Fatalf("documented invocation ignored TASK override:\n%s", output)
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
	writeClaimStub(t, binPath, `package main

import (
	"os"
	"strings"
)

func main() {
	file, err := os.OpenFile(os.Getenv("RECONC_STUB_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	defer file.Close()
	_, _ = file.WriteString(strings.Join(os.Args[1:], " ") + "\n")
}
`, "#!/bin/sh\necho \"$@\" >> \"$RECONC_STUB_LOG\"\nexit 0\n")
	t.Setenv("RECONC_STUB_LOG", logPath)
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
	writeClaimStub(t, binPath, `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "synthetic-claim-failure")
	os.Exit(7)
}
`, "#!/bin/sh\necho synthetic-claim-failure >&2\nexit 7\n")
	err := assertClaims(root, "TASK-0001-X", []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "reconc hook claim alpha failed") {
		t.Fatalf("expected forwarded failure, got %v", err)
	}
}

func writeClaimStub(t *testing.T, binPath, windowsSource, unixScript string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(binPath, []byte(unixScript), 0o755); err != nil {
			t.Fatalf("write stub: %v", err)
		}
		return
	}
	sourcePath := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(sourcePath, []byte(windowsSource), 0o600); err != nil {
		t.Fatalf("write Windows stub source: %v", err)
	}
	build := exec.Command("go", "build", "-o", binPath, sourcePath)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Windows stub: %v\n%s", err, output)
	}
}
