package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestMCPContractIsVisibleInWhyDoctorAndHookStatus(t *testing.T) {
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `mcp:
  unclassified: deny
  tools:
    - platform: cursor
      tool: repo_write
      effect: repository_write
      path_fields: [/path]
    - platform: opencode
      tool: run_check
      effect: command
      command_field: /command
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}

	var whyOut, whyErr bytes.Buffer
	if err := Run([]string{"why", "mcp", repo, "--json"}, "test", &whyOut, &whyErr); err != nil {
		t.Fatalf("why mcp: %v stderr=%s", err, whyErr.String())
	}
	var why map[string]interface{}
	if err := json.Unmarshal(whyOut.Bytes(), &why); err != nil {
		t.Fatal(err)
	}
	if why["unclassified"] != "deny" || len(why["tools"].([]interface{})) != 2 {
		t.Fatalf("why MCP contract = %#v", why)
	}

	report, err := runDoctorDeepJSON(t, repo)
	if err != nil {
		t.Fatalf("deep doctor: %v", err)
	}
	if status := doctorCheckStatus(t, report, "MCP side-effect policy"); status != doctorStatusWarn {
		t.Fatalf("MCP doctor status = %s", status)
	}
	detail := doctorCheckDetail(t, report, "MCP side-effect policy")
	for _, want := range []string{"cursor=1", "opencode=1", "strict unclassified deny is unavailable", "redacted"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("MCP doctor detail misses %q: %s", want, detail)
		}
	}

	var statusOut bytes.Buffer
	if err := runHookStatus([]string{repo, "--json"}, &statusOut); err != nil {
		t.Fatal(err)
	}
	var statuses []struct {
		Kind string `json:"kind"`
		MCP  struct {
			UnclassifiedMode       string `json:"unclassified_mode"`
			StrictUnclassifiedDeny bool   `json:"strict_unclassified_deny_available"`
			Limitation             string `json:"limitation"`
			Mappings               []struct {
				Tool string `json:"tool"`
			} `json:"mappings"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(statusOut.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, status := range statuses {
		if status.Kind != "cursor" && status.Kind != "opencode" && status.Kind != "kilo" {
			continue
		}
		found[status.Kind] = true
		if status.MCP.UnclassifiedMode != "deny" {
			t.Fatalf("%s MCP mode = %q", status.Kind, status.MCP.UnclassifiedMode)
		}
		if status.Kind == "cursor" && (!status.MCP.StrictUnclassifiedDeny || len(status.MCP.Mappings) != 1) {
			t.Fatalf("Cursor MCP status = %#v", status.MCP)
		}
		if status.Kind == "opencode" && (status.MCP.StrictUnclassifiedDeny || status.MCP.Limitation == "" || len(status.MCP.Mappings) != 1) {
			t.Fatalf("OpenCode MCP status = %#v", status.MCP)
		}
	}
	for _, kind := range []string{"cursor", "opencode", "kilo"} {
		if !found[kind] {
			t.Fatalf("%s MCP status missing", kind)
		}
	}
}
