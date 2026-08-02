package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePiExtensionUsesPinnedTypedContract(t *testing.T) {
	artifact, err := Generate(KindPi)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.TargetPath != PiExtensionPath || artifact.Executable {
		t.Fatalf("Pi artifact metadata = %+v", artifact)
	}
	for _, required := range []string{
		`from "@earendil-works/pi-coding-agent"`,
		`pi.on("session_start"`,
		`pi.on("input"`,
		`pi.on("tool_call"`,
		`pi.on("tool_result"`,
		`pi.on("user_bash"`,
		`pi.on("session_before_compact"`,
		`pi.on("session_compact"`,
		`pi.on("agent_settled"`,
		`pi.on("session_shutdown"`,
		`"pi-pre-tool-use":{"timeoutMilliseconds":10000`,
		`"pi-stop":{"timeoutMilliseconds":30000`,
		`"maxContinuations":10`,
		`pi.sendUserMessage(continuation.reason)`,
		`"pi-continuation-requested"`,
	} {
		if !strings.Contains(artifact.Content, required) {
			t.Fatalf("generated Pi extension missing %q", required)
		}
	}
	for _, forbidden := range []string{"session_stop", "tool_approval_requested", "deliveryAcknowledged", "@oh-my-pi"} {
		if strings.Contains(artifact.Content, forbidden) {
			t.Fatalf("generated Pi extension contains foreign or invented contract %q", forbidden)
		}
	}
}

func TestInspectPiProjectTrustStates(t *testing.T) {
	t.Run("default ask remains installed", func(t *testing.T) {
		repo := t.TempDir()
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		if _, err := Install(KindPi, repo, false); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindPi)
		if status.State != StateInstalled || status.Configured || !strings.Contains(status.Detail, "ask for project trust") || !strings.Contains(status.Remediation, "pi --approve") {
			t.Fatalf("Pi default trust status = %+v", status)
		}
	})

	t.Run("global always configures", func(t *testing.T) {
		repo := t.TempDir()
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		writePiSettings(t, agentDir, "always")
		if _, err := Install(KindPi, repo, false); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindPi)
		if status.State != StateConfigured || !status.Configured {
			t.Fatalf("Pi always-trusted status = %+v", status)
		}
	})

	t.Run("nearest saved deny overrides always", func(t *testing.T) {
		repo := t.TempDir()
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		canonicalRepo, err := filepath.EvalSymlinks(repo)
		if err != nil {
			t.Fatal(err)
		}
		canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(repo))
		if err != nil {
			t.Fatal(err)
		}
		writePiSettings(t, agentDir, "always")
		writePiTrust(t, agentDir, map[string]bool{canonicalRepo: false, canonicalParent: true})
		if _, err := Install(KindPi, repo, false); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindPi)
		if status.State != StateInstalled || !strings.Contains(status.Detail, "explicit untrusted decision") {
			t.Fatalf("Pi saved-deny status = %+v", status)
		}
	})

	t.Run("parent saved trust configures", func(t *testing.T) {
		parent := t.TempDir()
		repo := filepath.Join(parent, "repo")
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		canonicalParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			t.Fatal(err)
		}
		writePiSettings(t, agentDir, "never")
		writePiTrust(t, agentDir, map[string]bool{canonicalParent: true})
		if _, err := Install(KindPi, repo, false); err != nil {
			t.Fatal(err)
		}
		if status := statusForKind(t, repo, KindPi); status.State != StateConfigured {
			t.Fatalf("Pi inherited trust status = %+v", status)
		}
	})

	t.Run("invalid trust store degrades", func(t *testing.T) {
		repo := t.TempDir()
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		if err := os.WriteFile(filepath.Join(agentDir, "trust.json"), []byte(`{"repo":"yes"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Install(KindPi, repo, false); err != nil {
			t.Fatal(err)
		}
		status := statusForKind(t, repo, KindPi)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "trust store") || !strings.Contains(status.Remediation, "Repair") {
			t.Fatalf("Pi invalid-trust status = %+v", status)
		}
	})
}

func writePiSettings(t *testing.T, agentDir, trust string) {
	t.Helper()
	body, err := json.Marshal(struct {
		DefaultProjectTrust string `json:"defaultProjectTrust"`
	}{DefaultProjectTrust: trust})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writePiTrust(t *testing.T, agentDir string, trust map[string]bool) {
	t.Helper()
	body, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "trust.json"), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
