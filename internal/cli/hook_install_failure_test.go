package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestHookInstallSuccessIsExplicitInJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runHookInstall([]string{hooks.KindClaudeCode, t.TempDir(), "--json"}, &stdout, &stderr); err != nil {
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
}
