package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestHookInstallPartialFailureIsExplicit(t *testing.T) {
	makeBlockedTarget := func(t *testing.T) string {
		t.Helper()
		repo := t.TempDir()
		target := filepath.Join(repo, filepath.FromSlash(hooks.ClaudeCodeSettingsPath))
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		return repo
	}

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runHookInstall([]string{hooks.KindClaudeCode, makeBlockedTarget(t), "--json"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("partial install unexpectedly succeeded")
		}
		var response map[string]interface{}
		if decodeErr := json.Unmarshal(stdout.Bytes(), &response); decodeErr != nil {
			t.Fatalf("decode partial install JSON: %v: %q", decodeErr, stdout.String())
		}
		if success, present := response["success"]; !present || success != false {
			t.Fatalf("partial install success = %#v, present=%t", success, present)
		}
		if response["partial"] != true || response["action"] != "not-installed" || response["error"] == "" {
			t.Fatalf("partial install JSON is ambiguous: %#v", response)
		}
	})

	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runHookInstall([]string{hooks.KindClaudeCode, makeBlockedTarget(t)}, &stdout, &stderr)
		if err == nil {
			t.Fatal("partial install unexpectedly succeeded")
		}
		text := stdout.String()
		for _, want := range []string{"Partially installed claude-code hook (not-installed)", "Status:  failed", "Error:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("partial install text misses %q: %q", want, text)
			}
		}
		if strings.HasPrefix(text, "Installed ") {
			t.Fatalf("partial failure claims success: %q", text)
		}
	})
}

func TestHookInstallWrapperSetupPartialFailureIsExplicit(t *testing.T) {
	makeBlockedWrapperTarget := func(t *testing.T) string {
		t.Helper()
		repo := makeHookRepoWithHostBinary(t)
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(hooks.WrapperTargetPath)), 0o755); err != nil {
			t.Fatal(err)
		}
		return repo
	}

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runHookInstall([]string{hooks.KindClaudeCode, makeBlockedWrapperTarget(t), "--json"}, &stdout, &stderr)
		if err == nil {
			t.Fatal("wrapper setup failure unexpectedly succeeded")
		}
		var response map[string]interface{}
		if decodeErr := json.Unmarshal(stdout.Bytes(), &response); decodeErr != nil {
			t.Fatalf("decode wrapper partial JSON: %v: %q", decodeErr, stdout.String())
		}
		if response["success"] != false || response["partial"] != true || response["action"] != "not-installed" ||
			response["wrapper_path"] != hooks.WrapperPath || response["wrapper_action"] != "created" || response["error"] == "" {
			t.Fatalf("wrapper partial JSON is ambiguous: %#v", response)
		}
	})

	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runHookInstall([]string{hooks.KindClaudeCode, makeBlockedWrapperTarget(t)}, &stdout, &stderr)
		if err == nil {
			t.Fatal("wrapper setup failure unexpectedly succeeded")
		}
		text := stdout.String()
		for _, want := range []string{
			"Partially installed claude-code hook (not-installed)",
			"Wrapper: " + hooks.WrapperPath + " (created)",
			"Status:  failed",
			"Resolve the wrapper setup error, then rerun",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("wrapper partial text misses %q: %q", want, text)
			}
		}
	})
}

func TestHookInstallSuccessIsExplicitInJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runHookInstall([]string{hooks.KindClaudeCode, makeHookRepoWithHostBinary(t), "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("successful install: %v", err)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode successful install JSON: %v: %q", err, stdout.String())
	}
	if success, present := response["success"]; !present || success != true {
		t.Fatalf("successful install success = %#v, present=%t", success, present)
	}
	if _, present := response["error"]; present {
		t.Fatalf("successful install unexpectedly includes error: %#v", response)
	}
	if response["wrapper_target_path"] != hooks.WrapperTargetPath || response["wrapper_target_action"] != "created" {
		t.Fatalf("successful install omits direct target: %#v", response)
	}
}

func TestHookInstallSuccessReportsDirectTargetInText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runHookInstall([]string{hooks.KindClaudeCode, makeHookRepoWithHostBinary(t)}, &stdout, &stderr); err != nil {
		t.Fatalf("successful install: %v", err)
	}
	want := "Direct:  " + hooks.WrapperTargetPath + " (created)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("successful install text misses %q: %q", want, stdout.String())
	}
}

func makeHookRepoWithHostBinary(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	artifact, err := hooks.GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	binaryRelative := strings.TrimSuffix(artifact.Content, "\n")
	binaryPath := filepath.Join(repo, filepath.FromSlash(binaryRelative))
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}
