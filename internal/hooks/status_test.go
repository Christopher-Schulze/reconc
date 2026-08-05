package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInspectPlatformsDetectsJSONContractDriftBeyondRouteStrings(t *testing.T) {
	repo := t.TempDir()
	writeExecutableWrapper(t, repo)
	if _, err := Install(KindCodex, repo, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, filepath.FromSlash(CodexHooksPath))
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	hooksMap := document["hooks"].(map[string]interface{})
	preTool := hooksMap["PreToolUse"].([]interface{})[0].(map[string]interface{})
	preTool["matcher"] = "Read"
	altered, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(altered, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	status := statusForKind(t, repo, KindCodex)
	if status.State != StateDegraded || !strings.Contains(status.Detail, "contract drift") {
		t.Fatalf("matcher-only drift not detected: %+v", status)
	}
}

func TestInspectPlatformsActivationStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		reports, err := InspectPlatforms(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, report := range reports {
			if report.State != StateAbsent {
				t.Fatalf("%s = %s, want absent", report.Kind, report.State)
			}
		}
	})

	t.Run("claude degraded then configured", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindClaudeCode, repo, false); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(repo, filepath.FromSlash(WrapperPath))); err != nil {
			t.Fatal(err)
		}
		if got := statusForKind(t, repo, KindClaudeCode).State; got != StateDegraded {
			t.Fatalf("without wrapper = %s, want degraded", got)
		}
		writeExecutableWrapper(t, repo)
		if got := statusForKind(t, repo, KindClaudeCode).State; got != StateConfigured {
			t.Fatalf("with wrapper = %s, want configured", got)
		}
	})

	t.Run("codex defaults configured and explicit false disables", func(t *testing.T) {
		repo := t.TempDir()
		writeExecutableWrapper(t, repo)
		if _, err := Install(KindCodex, repo, false); err != nil {
			t.Fatal(err)
		}
		if got := statusForKind(t, repo, KindCodex).State; got != StateConfigured {
			t.Fatalf("without flag = %s, want configured", got)
		}
		config := filepath.Join(repo, ".codex", "config.toml")
		if err := os.WriteFile(config, []byte("[features]\nhooks = false\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := statusForKind(t, repo, KindCodex).State; got != StateInstalled {
			t.Fatalf("explicit false = %s, want installed", got)
		}
		if err := os.WriteFile(config, []byte("[features]\nhooks = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := statusForKind(t, repo, KindCodex).State; got != StateConfigured {
			t.Fatalf("with flag = %s, want configured", got)
		}
	})

	t.Run("codex rejects root-level lookalike", func(t *testing.T) {
		repo := t.TempDir()
		writeExecutableWrapper(t, repo)
		if _, err := Install(KindCodex, repo, false); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(repo, ".codex", "config.toml")
		if err := os.WriteFile(config, []byte("hooks=true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindCodex)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "TOML root") {
			t.Fatalf("root-level hooks flag = %+v, want degraded", status)
		}
	})

	t.Run("kilo unsupported in pure mode", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KILO_PURE", "1")
		if got := statusForKind(t, repo, KindKilo).State; got != StateUnsupported {
			t.Fatalf("KILO_PURE = %s, want unsupported", got)
		}
	})

	t.Run("managed plugin drift is degraded", func(t *testing.T) {
		repo := t.TempDir()
		if _, err := Install(KindKilo, repo, false); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repo, filepath.FromSlash(KiloPluginPath))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, []byte("// local drift\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := statusForKind(t, repo, KindKilo).State; got != StateDegraded {
			t.Fatalf("drifted Kilo plugin = %s, want degraded", got)
		}
	})

	t.Run("legacy Kilo plugin is visible but canonical plus legacy is degraded", func(t *testing.T) {
		repo := t.TempDir()
		artifact, err := Generate(KindKilo)
		if err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(repo, ".kilocode", "plugin", "reconc.js")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte(artifact.Content), 0o644); err != nil {
			t.Fatal(err)
		}
		writeExecutableWrapper(t, repo)
		legacyStatus := statusForKind(t, repo, KindKilo)
		if legacyStatus.State != StateConfigured || legacyStatus.TargetPath != ".kilocode/plugin/reconc.js" {
			t.Fatalf("legacy-only Kilo status = %+v", legacyStatus)
		}
		canonical := filepath.Join(repo, filepath.FromSlash(KiloPluginPath))
		if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(canonical, []byte(artifact.Content), 0o644); err != nil {
			t.Fatal(err)
		}
		duplicateStatus := statusForKind(t, repo, KindKilo)
		if duplicateStatus.State != StateDegraded || !strings.Contains(duplicateStatus.Detail, "both exist") {
			t.Fatalf("duplicate Kilo status = %+v", duplicateStatus)
		}
	})

}

func TestInspectGitHookShadowedByCoreHooksPath(t *testing.T) {
	repo := t.TempDir()
	command := exec.Command("git", "init", "-q", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "-C", repo, "config", "core.hooksPath", ".githooks")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, output)
	}
	if got := statusForKind(t, repo, KindGitPreCommit).State; got != StateShadowed {
		t.Fatalf("git hook = %s, want shadowed", got)
	}
	generated, err := Generate(KindGitPreCommit)
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(repo, ".githooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte(generated.Content), 0o755); err != nil {
		t.Fatal(err)
	}
	status := statusForKind(t, repo, KindGitPreCommit)
	if status.State != StateConfigured || status.TargetPath != ".githooks/pre-commit" {
		t.Fatalf("active managed hook = %+v, want configured .githooks/pre-commit", status)
	}
}

func TestInspectGitHookRequiresExecutableArtifact(t *testing.T) {
	repo := t.TempDir()
	command := exec.Command("git", "init", "-q", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if _, err := Install(KindGitPreCommit, repo, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, filepath.FromSlash(GitPreCommitPath))
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if got := statusForKind(t, repo, KindGitPreCommit).State; got != StateConfigured {
			t.Fatalf("regular Windows git hook = %s, want configured", got)
		}
		return
	}
	if got := statusForKind(t, repo, KindGitPreCommit).State; got != StateDegraded {
		t.Fatalf("non-executable git hook = %s, want degraded", got)
	}
}

func statusForKind(t *testing.T, repo, kind string) PlatformStatus {
	t.Helper()
	reports, err := InspectPlatforms(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range reports {
		if report.Kind == kind {
			return report
		}
	}
	t.Fatalf("status for %s missing", kind)
	return PlatformStatus{}
}

func writeExecutableWrapper(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, "tools", "reconc", "bin", "hook")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(GenerateWrapper().Content), 0o755); err != nil {
		t.Fatal(err)
	}
}
