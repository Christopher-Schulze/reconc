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
	for _, want := range []string{"cursor=1", "opencode=1", "omp=0", "strict unclassified deny is unavailable", "redacted", "external client configuration is not inspected", "direct downstream configurations are unenforced"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("MCP doctor detail misses %q: %s", want, detail)
		}
	}

	var policyStatusOut bytes.Buffer
	if err := runStatus([]string{repo, "--json"}, &policyStatusOut, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var policyStatus struct {
		GatewayScope          string `json:"mcp_gateway_scope"`
		ExternalConfiguration string `json:"mcp_external_configuration"`
		BypassRoutes          string `json:"mcp_bypass_routes"`
	}
	if err := json.Unmarshal(policyStatusOut.Bytes(), &policyStatus); err != nil {
		t.Fatal(err)
	}
	if policyStatus.GatewayScope != "explicit_routes_only" || policyStatus.ExternalConfiguration != "not_inspected" || policyStatus.BypassRoutes != "unenforced" {
		t.Fatalf("policy status MCP boundary = %#v", policyStatus)
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
		if status.Kind != "cursor" && status.Kind != "opencode" && status.Kind != "kilo" && status.Kind != "omp" && status.Kind != "pi" {
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
	for _, kind := range []string{"cursor", "opencode", "kilo", "omp", "pi"} {
		if !found[kind] {
			t.Fatalf("%s MCP status missing", kind)
		}
	}
}

// TestHookStatusReportsStrictMCPDenyOnlyWhereADiscriminatorExists pins the
// status claim per host. Cursor has a dedicated MCP event; Claude Code and
// Codex publish the `mcp__<server>__<tool>` namespace and install a matcher for
// it, so strict unclassified deny is real there. The generic-tool hosts cannot
// tell an unconfigured MCP call from a built-in tool, so status must report
// that limitation instead of claiming enforcement.
func TestHookStatusReportsStrictMCPDenyOnlyWhereADiscriminatorExists(t *testing.T) {
	repo := setupMCPStatusRepo(t)

	var statusOut bytes.Buffer
	if err := runHookStatus([]string{repo, "--json"}, &statusOut); err != nil {
		t.Fatal(err)
	}
	var statuses []struct {
		Kind string `json:"kind"`
		MCP  *struct {
			StrictUnclassifiedDeny bool   `json:"strict_unclassified_deny_available"`
			Limitation             string `json:"limitation"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(statusOut.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	strict := map[string]bool{"cursor": true, "claude-code": true, "codex": true}
	generic := map[string]bool{"opencode": true, "kilo": true, "omp": true, "pi": true, "zcode": true}
	seen := map[string]bool{}
	for _, status := range statuses {
		if status.MCP == nil {
			continue
		}
		switch {
		case strict[status.Kind]:
			seen[status.Kind] = true
			if !status.MCP.StrictUnclassifiedDeny {
				t.Errorf("%s must report strict unclassified deny as available", status.Kind)
			}
			if status.MCP.Limitation != "" {
				t.Errorf("%s must not report an MCP discriminator limitation, got %q", status.Kind, status.MCP.Limitation)
			}
		case generic[status.Kind]:
			seen[status.Kind] = true
			if status.MCP.StrictUnclassifiedDeny {
				t.Errorf("%s has no MCP discriminator and must not claim strict deny", status.Kind)
			}
			if status.MCP.Limitation == "" {
				t.Errorf("%s must report the missing-discriminator limitation", status.Kind)
			}
		}
	}
	for kind := range strict {
		if !seen[kind] {
			t.Errorf("%s reported no MCP status at all", kind)
		}
	}
	for kind := range generic {
		if !seen[kind] {
			t.Errorf("%s reported no MCP status at all", kind)
		}
	}
}

func setupMCPStatusRepo(t *testing.T) string {
	t.Helper()
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `mcp:
  unclassified: deny
  tools:
    - platform: claude-code
      tool: mcp__filesystem__write_file
      effect: repository_write
      path_fields: [/path]
`
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo
}
