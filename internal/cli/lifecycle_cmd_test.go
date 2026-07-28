package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/usercli"
)

func TestCanonicalUpdateSourceInstallEmitsOneApplyRefusalDocument(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := usercli.InstallCurrentWithReceipt("", usercli.InstallOptions{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"update", "--json"}, "1.0.0", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 || stderr.Len() != 0 {
		t.Fatalf("source update refusal: err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var report usercli.LifecycleReport
	if err := decoder.Decode(&report); err != nil {
		t.Fatal(err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("update emitted trailing JSON: %v", err)
	}
	if report.Operation != "update.apply" ||
		report.Status != usercli.LifecycleRefused ||
		!strings.Contains(report.NextAction, "install-cli") {
		t.Fatalf("source update report = %+v", report)
	}
}

func TestLifecycleCLIRejectsInvalidSelectionWithoutMutation(t *testing.T) {
	tests := [][]string{
		{"update", "--channel", "stable", "--version", "1.0.0"},
		{"update", "check", "--allow-downgrade"},
		{"update", "--channel"},
		{"uninstall", "--unknown"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(args, "1.0.0", &stdout, &stderr); err == nil {
				t.Fatalf("%v unexpectedly passed", args)
			}
			if stdout.Len() != 0 {
				t.Fatalf("%v emitted mutation-looking output: %s", args, stdout.String())
			}
		})
	}
}

func TestLifecycleCLIInvalidJSONRequestEmitsOneFailureDocument(t *testing.T) {
	for _, args := range [][]string{
		{"update", "--json", "--unknown"},
		{"uninstall", "--json", "--unknown"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(args, "1.0.0", &stdout, &stderr)
			if err == nil || ExitCode(err) != 1 || stderr.Len() != 0 {
				t.Fatalf("%v: err=%v stdout=%s stderr=%s", args, err, stdout.String(), stderr.String())
			}
			decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
			var report usercli.LifecycleReport
			if err := decoder.Decode(&report); err != nil {
				t.Fatal(err)
			}
			var trailing json.RawMessage
			if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				t.Fatalf("%v emitted trailing JSON: %v", args, err)
			}
			if report.Status != usercli.LifecycleFailed || report.Changed {
				t.Fatalf("%v failure report = %+v", args, report)
			}
		})
	}
}

func TestLifecycleTextEndsWithExactlyOneNextLine(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := usercli.InstallCurrentWithReceipt("", usercli.InstallOptions{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"update"}, "1.0.0", &stdout, &stderr)
	if err == nil {
		t.Fatal("source update unexpectedly passed")
	}
	text := stdout.String()
	if strings.Count(text, "Next:") != 1 || !strings.HasSuffix(text, "\n") {
		t.Fatalf("lifecycle text = %q", text)
	}
}

func TestUpdateCompatibilityAliasesRetainOperations(t *testing.T) {
	installDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	if _, err := usercli.InstallCurrentWithReceipt("", usercli.InstallOptions{Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		args      []string
		operation string
	}{
		{args: []string{"update", "check", "--json"}, operation: "update.check"},
		{args: []string{"update", "apply", "--json"}, operation: "update.apply"},
	} {
		t.Run(test.operation, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(test.args, "1.0.0", &stdout, &stderr)
			if err == nil || ExitCode(err) != 1 || stderr.Len() != 0 {
				t.Fatalf("%v: err=%v stdout=%s stderr=%s", test.args, err, stdout.String(), stderr.String())
			}
			var report usercli.LifecycleReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Operation != test.operation {
				t.Fatalf("%v operation = %q", test.args, report.Operation)
			}
		})
	}
}

func TestUpdateHelpTeachesCanonicalCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"update", "--help"}, "1.0.0", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "Usage: reconc update [") ||
		!strings.Contains(stdout.String(), "already current") ||
		stderr.Len() != 0 {
		t.Fatalf("update help: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestUninstallRemovesOnlySourceOwnedGlobalBytes(t *testing.T) {
	installDirectory := t.TempDir()
	home := t.TempDir()
	repository := t.TempDir()
	policy := repository + "/.reconc.yml"
	if err := os.WriteFile(policy, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	installed, err := usercli.InstallCurrentWithReceipt("", usercli.InstallOptions{Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"uninstall", "--json"}, "1.0.0", &stdout, &stderr); err != nil {
		t.Fatalf("uninstall: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(installed.Status.TargetPath); !os.IsNotExist(err) {
		t.Fatalf("owned binary still exists: %v", err)
	}
	if body, err := os.ReadFile(policy); err != nil || string(body) != "version: 1\n" {
		t.Fatalf("repository policy changed: %q err=%v", body, err)
	}
}
