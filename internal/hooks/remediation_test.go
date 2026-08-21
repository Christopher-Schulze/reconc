package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPOSIXRemediationCommandRoundTripsAdversarialArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell contract")
	}
	arguments := []string{
		"space path", `double"quote`, "single'quote", "$HOME", "`touch nope`",
		"semi;colon", `back\\slash`, "line\nbreak", "/tmp/path:with=separators", "",
	}
	command := remediationCommand{Program: "printf", Args: append([]string{"%s\\000"}, arguments...)}
	rendered, language := renderRemediationCommand(command, "darwin")
	if language != "sh" {
		t.Fatalf("language = %q", language)
	}
	output, err := exec.Command("/bin/sh", "-c", rendered).Output()
	if err != nil {
		t.Fatalf("execute rendered command: %v", err)
	}
	want := []byte{}
	for _, argument := range arguments {
		want = append(want, argument...)
		want = append(want, 0)
	}
	if !bytes.Equal(output, want) {
		t.Fatalf("argv did not round-trip:\n got %q\nwant %q\ncommand: %s", output, want, rendered)
	}
}

func TestPowerShellRemediationUsesLiteralArgumentsAndInvocationOperator(t *testing.T) {
	command := remediationCommand{
		Program: `C:\\Program Files\\reconc'edge.exe`,
		Args:    []string{"hook", "install", "codex", "C:\\repo $x; `boom`\nnext", "--force"},
	}
	rendered, language := renderRemediationCommand(command, "windows")
	if language != "powershell" || !strings.HasPrefix(rendered, "& '") {
		t.Fatalf("PowerShell rendering = %q (%s)", rendered, language)
	}
	for _, required := range []string{`reconc''edge.exe'`, `$x; ` + "`boom`" + "\nnext'", "'--force'"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("PowerShell rendering missing %q: %s", required, rendered)
		}
	}
}

func TestPowerShellRemediationRoundTripsInNativeShell(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("RECONC_POWERSHELL_ARG_HELPER") == "1" {
		return
	}
	arguments := []string{"space path", `double"quote`, "single'quote", "$HOME", "`literal`", "semi;colon", `back\\slash`, "line\nbreak", ""}
	command := remediationCommand{Program: os.Args[0], Args: append([]string{"-test.run=TestPowerShellRemediationArgHelper", "--"}, arguments...)}
	rendered, _ := renderRemediationCommand(command, "windows")
	process := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", rendered)
	process.Env = append(os.Environ(), "RECONC_POWERSHELL_ARG_HELPER=1")
	output, err := process.Output()
	if err != nil {
		t.Fatalf("execute PowerShell rendering: %v", err)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode child argv: %v (%q)", err, output)
	}
	if strings.Join(got, "\x00") != strings.Join(arguments, "\x00") {
		t.Fatalf("PowerShell argv = %#v, want %#v", got, arguments)
	}
}

func TestPowerShellRemediationArgHelper(t *testing.T) {
	if os.Getenv("RECONC_POWERSHELL_ARG_HELPER") != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(os.Args[separator+1:]); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestRemediationMarkdownFenceCannotBeClosedByArguments(t *testing.T) {
	plan := hostRemediation("Run the exact command:", remediationCommand{Program: "reconc", Args: []string{"hook", "status", "repo```\nnot-code"}})
	rendered := renderRemediation(plan, "darwin")
	if !strings.Contains(rendered, "````sh\n") || strings.Count(rendered, "````") != 2 {
		t.Fatalf("dynamic Markdown fence is not injection-safe: %q", rendered)
	}
}

func TestHookRemediationTypedStateMatrix(t *testing.T) {
	t.Run("absent uses normal install", func(t *testing.T) {
		status := statusForKind(t, t.TempDir(), KindClaudeCode)
		assertRemediation(t, status, remediationInstall, false)
	})

	t.Run("malformed merge config uses force and succeeds", func(t *testing.T) {
		repo := t.TempDir()
		path := filepath.Join(repo, filepath.FromSlash(ClaudeCodeSettingsPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindClaudeCode)
		assertRemediation(t, status, remediationForceRepair, true)
		if _, err := Install(KindClaudeCode, repo, true); err != nil {
			t.Fatalf("advertised force repair failed: %v", err)
		}
		if repaired := statusForKind(t, repo, KindClaudeCode); repaired.State != StateConfigured {
			t.Fatalf("repaired status = %+v", repaired)
		}
	})

	t.Run("managed drift uses non-force install", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repo, filepath.FromSlash(KiloPluginPath))
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("// managed drift\n"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindKilo)
		assertRemediation(t, status, remediationInstall, false)
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatalf("advertised managed repair failed: %v", err)
		}
	})

	t.Run("foreign plugin is manual and never advertises force", func(t *testing.T) {
		repo := t.TempDir()
		path := filepath.Join(repo, filepath.FromSlash(KiloPluginPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// user plugin\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindKilo)
		assertRemediation(t, status, remediationManualConflict, false)
		if status.remediation.Command.Program != "" || strings.Contains(status.Remediation, "--force") {
			t.Fatalf("foreign content received executable force advice: %+v", status)
		}
		if _, err := Install(KindKilo, repo, false); err == nil {
			t.Fatal("non-force install unexpectedly replaced foreign plugin")
		}
	})

	t.Run("disabled Codex activation requires force", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindCodex, repo, false); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, codexActivationPath), []byte("[features]\nhooks = false\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindCodex)
		assertRemediation(t, status, remediationForceRepair, true)
	})

	t.Run("invalid activation syntax is manual", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindCodex, repo, false); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, codexActivationPath), []byte("hooks = maybe\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindCodex)
		assertRemediation(t, status, remediationManualConflict, false)
	})

	t.Run("foreign shared wrapper blocks automatic advice", func(t *testing.T) {
		repo := t.TempDir()
		wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
		if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n# user wrapper\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindClaudeCode)
		assertRemediation(t, status, remediationManualConflict, false)
	})

	t.Run("missing managed wrapper uses normal install", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(repo, filepath.FromSlash(WrapperPath))); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindKilo)
		assertRemediation(t, status, remediationInstall, false)
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatalf("advertised wrapper repair failed: %v", err)
		}
	})

	t.Run("legacy Kilo path requires ownership review", func(t *testing.T) {
		repo := t.TempDir()
		artifact, err := Generate(KindKilo)
		if err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(repo, filepath.FromSlash(".kilocode/plugin/reconc.js"))
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte(artifact.Content), 0o644); err != nil {
			t.Fatal(err)
		}
		writeExecutableWrapper(t, repo)
		status := statusForKind(t, repo, KindKilo)
		if status.State != StateConfigured || status.TargetPath != ".kilocode/plugin/reconc.js" {
			t.Fatalf("legacy status = %+v", status)
		}
		assertRemediation(t, status, remediationManualConflict, false)
	})

	t.Run("shadowed Git hook installs at active path", func(t *testing.T) {
		repo := t.TempDir()
		if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, output)
		}
		if _, err := Install(KindGitPreCommit, repo, false); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("git", "-C", repo, "config", "core.hooksPath", ".githooks").CombinedOutput(); err != nil {
			t.Fatalf("git config: %v: %s", err, output)
		}
		status := statusForKind(t, repo, KindGitPreCommit)
		if status.State != StateShadowed {
			t.Fatalf("shadowed status = %+v", status)
		}
		assertRemediation(t, status, remediationInstall, false)
		if _, err := Install(KindGitPreCommit, repo, false); err != nil {
			t.Fatalf("advertised active-path install failed: %v", err)
		}
		if repaired := statusForKind(t, repo, KindGitPreCommit); repaired.State != StateConfigured {
			t.Fatalf("active-path repair status = %+v", repaired)
		}
	})

	t.Run("host environment disablement is not an install", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KILO_PURE", "1")
		status := statusForKind(t, repo, KindKilo)
		assertRemediation(t, status, remediationHostAction, false)
	})
}

func TestEveryPlatformStatusHasTypedRemediationDisposition(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	reports, err := InspectPlatforms(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != len(Platforms()) {
		t.Fatalf("reports = %d, platforms = %d", len(reports), len(Platforms()))
	}
	for _, report := range reports {
		switch report.remediation.Disposition {
		case remediationNone, remediationInstall, remediationForceRepair, remediationManualConflict, remediationHostAction:
		default:
			t.Fatalf("%s has untyped remediation %+v", report.Kind, report.remediation)
		}
	}
}

func assertRemediation(t *testing.T, status PlatformStatus, want remediationDisposition, wantForce bool) {
	t.Helper()
	if status.remediation.Disposition != want {
		t.Fatalf("remediation disposition = %q, want %q: %+v", status.remediation.Disposition, want, status)
	}
	hasForce := false
	for _, argument := range status.remediation.Command.Args {
		if argument == "--force" {
			hasForce = true
		}
	}
	if hasForce != wantForce {
		t.Fatalf("remediation force=%t, want %t: %+v", hasForce, wantForce, status.remediation)
	}
}
