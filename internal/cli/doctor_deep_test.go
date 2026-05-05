package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type doctorDeepJSON struct {
	RepoRoot string        `json:"repo_root"`
	Deep     bool          `json:"deep"`
	Checks   []doctorCheck `json:"checks"`
}

func TestRunDoctorDeepOK(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", repo, "--deep", "--json"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("doctor --deep --json: %v\nstderr: %s", err, stderr.String())
	}

	report := decodeDoctorDeepJSON(t, stdout.Bytes())
	if !report.Deep {
		t.Fatalf("expected deep=true payload, got %#v", report)
	}
	if status := doctorCheckStatus(t, report, "lockfile freshness"); status != doctorStatusOK {
		t.Fatalf("expected lockfile freshness OK, got %s", status)
	}
	if status := doctorCheckStatus(t, report, "rule conflicts"); status != doctorStatusOK {
		t.Fatalf("expected rule conflicts OK, got %s", status)
	}
}

func TestRunDoctorDeepChecks(t *testing.T) {
	t.Run("stale hook install warns", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
			t.Fatalf("mkdir .claude: %v", err)
		}
		stale := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"reconc check . --json"}]}]}}`
		if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(stale), 0o644); err != nil {
			t.Fatalf("write stale hook config: %v", err)
		}

		report, err := runDoctorDeepJSON(t, repo)
		if err != nil {
			t.Fatalf("doctor --deep --json: %v", err)
		}
		if status := doctorCheckStatus(t, report, "hook runtime compatibility"); status != doctorStatusWarn {
			t.Fatalf("expected hook runtime compatibility WARN, got %s", status)
		}
	})

	t.Run("stale lockfile fails", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		rulesPath := filepath.Join(repo, "policies", "rules.yml")
		if err := os.WriteFile(rulesPath, []byte("rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: block\n    message: generated files are read-only\n"), 0o644); err != nil {
			t.Fatalf("rewrite rules: %v", err)
		}

		report, err := runDoctorDeepJSON(t, repo)
		if ExitCode(err) != 1 {
			t.Fatalf("expected exit 1 for stale lockfile, got %d", ExitCode(err))
		}
		if status := doctorCheckStatus(t, report, "lockfile freshness"); status != doctorStatusFail {
			t.Fatalf("expected lockfile freshness FAIL, got %s", status)
		}
	})

	t.Run("moved lockfile root fails", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		lockfile := filepath.Join(repo, ".reconc", "policy.lock.json")
		data, err := os.ReadFile(lockfile)
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}
		moved := strings.ReplaceAll(string(data), repo, filepath.Join(t.TempDir(), "old-root"))
		if err := os.WriteFile(lockfile, []byte(moved), 0o644); err != nil {
			t.Fatalf("rewrite lockfile root: %v", err)
		}

		report, err := runDoctorDeepJSON(t, repo)
		if ExitCode(err) != 1 {
			t.Fatalf("expected exit 1 for moved lockfile root, got %d", ExitCode(err))
		}
		detail := doctorCheckDetail(t, report, "lockfile freshness")
		if !strings.Contains(detail, "repo_root does not match") {
			t.Fatalf("expected repo_root mismatch detail, got %q", detail)
		}
	})

	t.Run("oversized audit warns", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		auditPath := filepath.Join(repo, ".reconc", "audit.jsonl")
		if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
			t.Fatalf("mkdir .reconc: %v", err)
		}
		payload := bytes.Repeat([]byte("x"), doctorAuditWarnBytes+1)
		if err := os.WriteFile(auditPath, payload, 0o644); err != nil {
			t.Fatalf("write audit log: %v", err)
		}

		report, err := runDoctorDeepJSON(t, repo)
		if err != nil {
			t.Fatalf("doctor --deep --json: %v", err)
		}
		if status := doctorCheckStatus(t, report, "audit log size"); status != doctorStatusWarn {
			t.Fatalf("expected audit log size WARN, got %s", status)
		}
	})

	t.Run("unknown preset and template fail", func(t *testing.T) {
		t.Setenv("RECONC_HOME", t.TempDir())
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(repo, "policies"), 0o755); err != nil {
			t.Fatalf("mkdir policies: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("extends:\n  - does-not-exist\nrules: []\n"), 0o644); err != nil {
			t.Fatalf("write .reconc.yml: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte("rules:\n  - id: t1\n    template: missing-template\n    paths: ['src/**']\n"), 0o644); err != nil {
			t.Fatalf("write rules.yml: %v", err)
		}

		report, err := runDoctorDeepJSON(t, repo)
		if ExitCode(err) != 1 {
			t.Fatalf("expected exit 1 for unknown refs, got %d", ExitCode(err))
		}
		if status := doctorCheckStatus(t, report, "preset/template references"); status != doctorStatusFail {
			t.Fatalf("expected preset/template references FAIL, got %s", status)
		}
		detail := doctorCheckDetail(t, report, "preset/template references")
		if !strings.Contains(detail, "unknown presets") || !strings.Contains(detail, "unknown templates") {
			t.Fatalf("expected both unknown preset and template detail, got %q", detail)
		}
	})

	t.Run("stale session claims warn", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: require-ci\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: warn\n    message: ci required\n")
		t.Setenv(agentsession.StateRootEnv, t.TempDir())
		if _, err := agentsession.InitializeSessionState(repo, "s1"); err != nil {
			t.Fatalf("InitializeSessionState: %v", err)
		}
		claimReport, err := agentsession.RecordClaim(repo, "ci-green", "s1")
		if err != nil {
			t.Fatalf("RecordClaim: %v", err)
		}
		old := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(claimReport.StatePath, old, old); err != nil {
			t.Fatalf("chtimes state file: %v", err)
		}

		report, err := runDoctorDeepJSON(t, repo)
		if err != nil {
			t.Fatalf("doctor --deep --json: %v", err)
		}
		if status := doctorCheckStatus(t, report, "session claim age"); status != doctorStatusWarn {
			t.Fatalf("expected session claim age WARN, got %s", status)
		}
	})

	t.Run("conflict count warns", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: dup-a\n    kind: deny_write\n    paths: ['src/**']\n    mode: warn\n    message: a\n  - id: dup-b\n    kind: deny_write\n    paths: ['src/**']\n    mode: warn\n    message: b\n")

		report, err := runDoctorDeepJSON(t, repo)
		if err != nil {
			t.Fatalf("doctor --deep --json: %v", err)
		}
		if status := doctorCheckStatus(t, report, "rule conflicts"); status != doctorStatusWarn {
			t.Fatalf("expected rule conflicts WARN, got %s", status)
		}
	})
}

func TestRunDoctorHelpMentionsDeep(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", "--help"}, "0.1.0-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "--deep") {
		t.Fatalf("expected --deep in help output, got %q", stdout.String())
	}
}

func TestDoctorDeepTextRenderingAndWarnings(t *testing.T) {
	report := &doctorDeepReport{
		RepoRoot: "/tmp/repo",
		Deep:     true,
		Checks: []doctorCheck{
			{Name: "lockfile freshness", Status: doctorStatusOK, Detail: "fresh"},
			{Name: "session claim age", Status: doctorStatusWarn, Detail: "stale"},
		},
	}

	var out bytes.Buffer
	renderDoctorDeepText(report, &out)
	text := out.String()
	if !strings.Contains(text, "reconc doctor --deep") {
		t.Fatalf("expected header in deep doctor text, got %q", text)
	}
	if !strings.Contains(text, "[OK  ] lockfile freshness") {
		t.Fatalf("expected OK row in deep doctor text, got %q", text)
	}
	if !strings.Contains(text, "[WARN] session claim age") {
		t.Fatalf("expected WARN row in deep doctor text, got %q", text)
	}

	discovery := ingest.DiscoveryResult{Warnings: []string{"custom warning"}}
	if got := firstDiscoveryWarning(discovery, "fallback"); got != "custom warning" {
		t.Fatalf("firstDiscoveryWarning should prefer discovery warning, got %q", got)
	}
	if got := firstDiscoveryWarning(ingest.DiscoveryResult{}, "fallback"); got != "fallback" {
		t.Fatalf("firstDiscoveryWarning should fall back, got %q", got)
	}
}

func TestDoctorDeepHelperCoverage(t *testing.T) {
	t.Run("yaml normalization and decode", func(t *testing.T) {
		raw := "rules:\n  - template: docs-follow-code\nextends:\n  - preset: strict\n"
		doc, err := decodeDoctorYAML(raw, "test.yml")
		if err != nil {
			t.Fatalf("decodeDoctorYAML: %v", err)
		}
		root, ok := doc.(map[string]interface{})
		if !ok {
			t.Fatalf("expected mapping root, got %#v", doc)
		}
		if _, ok := root["rules"].([]interface{}); !ok {
			t.Fatalf("expected rules list, got %#v", root["rules"])
		}
		if normalized := normalizeDoctorValue(map[interface{}]interface{}{"k": []interface{}{map[interface{}]interface{}{"template": "x"}}}); normalized == nil {
			t.Fatal("expected normalizeDoctorValue to keep nested values")
		}
		if _, err := decodeDoctorYAML("{", "broken.yml"); err == nil {
			t.Fatal("expected invalid YAML error")
		}
	})

	t.Run("inline blocks and refs", func(t *testing.T) {
		text := "# doc\n```reconc\nrules:\n  - template: docs-follow-code\n```\n"
		blocks := extractDoctorInlineBlocks(text)
		if len(blocks) != 1 || !strings.Contains(blocks[0], "template: docs-follow-code") {
			t.Fatalf("unexpected inline blocks: %#v", blocks)
		}
		templatesFound, err := extractTemplateRefs(blocks[0], "inline")
		if err != nil {
			t.Fatalf("extractTemplateRefs: %v", err)
		}
		if len(templatesFound) != 1 || templatesFound[0] != "docs-follow-code" {
			t.Fatalf("unexpected template refs: %#v", templatesFound)
		}
		presetsFound, err := extractPresetRefs("extends:\n  - strict\n  - default\n", ".reconc.yml")
		if err != nil {
			t.Fatalf("extractPresetRefs: %v", err)
		}
		if got := strings.Join(presetsFound, ","); got != "default,strict" {
			t.Fatalf("unexpected preset refs: %q", got)
		}
		if _, err := extractPresetRefs("extends: nope\n", ".reconc.yml"); err == nil {
			t.Fatal("expected extends type validation error")
		}
	})

	t.Run("doctor checks without discovered repo", func(t *testing.T) {
		discovery := ingest.DiscoveryResult{}
		if check := doctorCheckAuditSize(discovery); check.Status != doctorStatusWarn {
			t.Fatalf("expected audit size WARN without discovery, got %#v", check)
		}
		if check := doctorCheckSessionClaims(discovery); check.Status != doctorStatusWarn {
			t.Fatalf("expected session claims WARN without discovery, got %#v", check)
		}
		if check := doctorCheckLockfileFreshness(discovery); check.Status != doctorStatusFail {
			t.Fatalf("expected lockfile freshness FAIL without discovery, got %#v", check)
		}
		if check := doctorCheckConflictCount(discovery); check.Status != doctorStatusFail {
			t.Fatalf("expected conflict count FAIL without discovery, got %#v", check)
		}
	})

	t.Run("session claims active but empty", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		t.Setenv(agentsession.StateRootEnv, t.TempDir())
		if _, err := agentsession.InitializeSessionState(repo, "empty-claims"); err != nil {
			t.Fatalf("InitializeSessionState: %v", err)
		}
		check := doctorCheckSessionClaims(ingest.DiscoveryResult{
			Discovered: true,
			RepoRoot:   repo,
		})
		if check.Status != doctorStatusOK || !strings.Contains(check.Detail, "no recorded claims") {
			t.Fatalf("expected empty-claims OK check, got %#v", check)
		}
	})
}

func runDoctorDeepJSON(t *testing.T, repo string) (*doctorDeepJSON, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run([]string{"doctor", repo, "--deep", "--json"}, "0.1.0-test", &stdout, &stderr)
	report := decodeDoctorDeepJSON(t, stdout.Bytes())
	return report, err
}

func decodeDoctorDeepJSON(t *testing.T, data []byte) *doctorDeepJSON {
	t.Helper()
	var report doctorDeepJSON
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("doctor deep output should be valid JSON: %v\n%s", err, string(data))
	}
	return &report
}

func doctorCheckStatus(t *testing.T, report *doctorDeepJSON, name string) string {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	t.Fatalf("missing doctor check %q in %#v", name, report.Checks)
	return ""
}

func doctorCheckDetail(t *testing.T, report *doctorDeepJSON, name string) string {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Detail
		}
	}
	t.Fatalf("missing doctor check %q in %#v", name, report.Checks)
	return ""
}
