package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/grokacp"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/yamlbound"
)

type doctorDeepJSON struct {
	RepoRoot string        `json:"repo_root"`
	Deep     bool          `json:"deep"`
	Checks   []doctorCheck `json:"checks"`
}

func TestDoctorGrokRuntimeChecksTrustAndEveryNativeRoute(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
	if _, err := hooks.Install(hooks.KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte(hooks.GenerateWrapper().Content), 0o755); err != nil {
		t.Fatal(err)
	}
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	original := doctorGrokInspect
	originalCapabilityProbe := doctorProbeGrokNativeStop
	defer func() {
		doctorGrokInspect = original
		doctorProbeGrokNativeStop = originalCapabilityProbe
	}()
	doctorProbeGrokNativeStop = func() grokacp.NativeStopGateProbe {
		return grokacp.NativeStopGateProbe{Supported: true, DocumentationPath: "/tmp/grok/docs/user-guide/10-hooks.md"}
	}

	runtimeVersion := "0.2.106"
	doctorGrokInspect = func(_ context.Context, root string) ([]byte, error) {
		loaded := []map[string]interface{}{}
		for _, event := range hooks.GrokRuntimeEvents() {
			loaded = append(loaded, map[string]interface{}{
				"target": "tools/reconc/bin/hook " + event + " .",
				"source": map[string]string{"type": "project", "path": filepath.Join(root, ".grok", "hooks")},
			})
		}
		return json.Marshal(map[string]interface{}{
			"grokVersion":    runtimeVersion,
			"projectTrusted": true,
			"hooks":          loaded,
		})
	}
	check := doctorCheckGrokRuntime(discovery)
	if check.Status != doctorStatusOK || !strings.Contains(check.Detail, "native no-leader Stop enforcement is active") {
		t.Fatalf("Grok doctor check = %+v", check)
	}
	runtimeVersion = "0.2.106"
	doctorProbeGrokNativeStop = func() grokacp.NativeStopGateProbe {
		return grokacp.NativeStopGateProbe{Detail: "installed Grok hook guide does not advertise blocking Stop decision control"}
	}
	check = doctorCheckGrokRuntime(discovery)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Detail, "does not advertise") {
		t.Fatalf("passive Grok runtime capability check = %+v", check)
	}

	doctorGrokInspect = func(_ context.Context, root string) ([]byte, error) {
		loaded := []map[string]interface{}{}
		for _, event := range hooks.GrokRuntimeEvents() {
			if event == "grok-stop" {
				continue
			}
			loaded = append(loaded, map[string]interface{}{
				"target": "tools/reconc/bin/hook " + event + " .",
				"source": map[string]string{"type": "project", "path": filepath.Join(root, ".grok", "hooks")},
			})
		}
		return json.Marshal(map[string]interface{}{
			"grokVersion":    "0.2.101",
			"projectTrusted": true,
			"hooks":          loaded,
		})
	}
	check = doctorCheckGrokRuntime(discovery)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Detail, "grok-stop") {
		t.Fatalf("Grok doctor must reject prefix-collision route coverage: %+v", check)
	}

	doctorGrokInspect = func(context.Context, string) ([]byte, error) {
		return []byte(`{"projectTrusted":false,"hooks":[]}`), nil
	}
	check = doctorCheckGrokRuntime(discovery)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Detail, "/hooks-trust") {
		t.Fatalf("untrusted Grok doctor check = %+v", check)
	}
}

func TestDoctorGrokRuntimeKeepsExecutionFailureBeforeOutputLimit(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if _, err := hooks.Install(hooks.KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	original := doctorGrokInspect
	defer func() { doctorGrokInspect = original }()
	doctorGrokInspect = func(context.Context, string) ([]byte, error) {
		return bytes.Repeat([]byte("x"), doctorGrokInspectMaxBytes+1), errors.New("exit status 23")
	}
	check := doctorCheckGrokRuntime(discovery)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Detail, "exit status 23") ||
		!strings.Contains(check.Detail, "stdout exceeds") {
		t.Fatalf("Grok execution and output failure precedence = %+v", check)
	}
	if strings.Contains(check.Detail, strings.Repeat("x", 1024)) {
		t.Fatal("oversized Grok stdout leaked into diagnostic detail")
	}
}

func TestDoctorGrokLeaderSteering(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	original := doctorProbeGrokLeader
	originalCapabilityProbe := doctorProbeGrokNativeStop
	defer func() {
		doctorProbeGrokLeader = original
		doctorProbeGrokNativeStop = originalCapabilityProbe
	}()
	t.Setenv(grokacp.SteerEnv, "")
	probed := false
	doctorProbeGrokLeader = func(time.Duration) grokacp.LeaderProbe {
		probed = true
		return grokacp.LeaderProbe{}
	}

	check := doctorCheckGrokLeaderSteering(discovery)
	if check.Status != doctorStatusOK || !strings.Contains(check.Detail, "not applicable") || probed {
		t.Fatalf("without Grok hook = %+v probed=%v", check, probed)
	}

	if _, err := hooks.Install(hooks.KindGrok, repo, false); err != nil {
		t.Fatal(err)
	}
	protocolVersion := uint32(1)
	tests := []struct {
		name       string
		probe      grokacp.LeaderProbe
		nativeStop bool
		status     string
		detail     string
	}{
		{name: "no endpoint", probe: grokacp.LeaderProbe{}, status: doctorStatusOK, detail: "optional backward-compatible steering is inactive"},
		{name: "discovery failed", probe: grokacp.LeaderProbe{Detail: "discover Grok leader endpoints: permission denied"}, status: doctorStatusWarn, detail: "discovery failed"},
		{name: "compatible", probe: grokacp.LeaderProbe{Endpoint: "/tmp/leader.sock", Reachable: true, Compatible: true, ProtocolVersion: &protocolVersion, BinaryVersion: "0.2.101"}, status: doctorStatusOK, detail: "protocol 1"},
		{name: "native compatible", probe: grokacp.LeaderProbe{Endpoint: "/tmp/leader.sock", Reachable: true, Compatible: true, ProtocolVersion: &protocolVersion, BinaryVersion: "0.2.106"}, nativeStop: true, status: doctorStatusOK, detail: "duplicate leader interjection is suppressed"},
		{name: "incompatible", probe: grokacp.LeaderProbe{Endpoint: "/tmp/leader.sock", Reachable: true, Detail: "_x.ai/interject missing"}, status: doctorStatusWarn, detail: "incompatible"},
		{name: "handshake failed", probe: grokacp.LeaderProbe{Endpoint: "/tmp/leader.sock", Detail: "connection refused"}, status: doctorStatusWarn, detail: "connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doctorProbeGrokLeader = func(time.Duration) grokacp.LeaderProbe { return test.probe }
			doctorProbeGrokNativeStop = func() grokacp.NativeStopGateProbe {
				return grokacp.NativeStopGateProbe{Supported: test.nativeStop}
			}
			check := doctorCheckGrokLeaderSteering(discovery)
			if check.Status != test.status || !strings.Contains(check.Detail, test.detail) {
				t.Fatalf("check = %+v, want status %s detail containing %q", check, test.status, test.detail)
			}
		})
	}

	t.Run("disabled via env", func(t *testing.T) {
		t.Setenv(grokacp.SteerEnv, "0")
		probed = false
		check := doctorCheckGrokLeaderSteering(discovery)
		if check.Status != doctorStatusOK || !strings.Contains(check.Detail, grokacp.SteerEnv) || probed {
			t.Fatalf("disabled check = %+v probed=%v", check, probed)
		}
	})
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

	t.Run("non-portable lockfile root fails", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		lockfile := filepath.Join(repo, ".reconc", "policy.lock.json")
		rewriteLockfileRoot(t, lockfile, filepath.Join(t.TempDir(), "old-root"))

		report, err := runDoctorDeepJSON(t, repo)
		if ExitCode(err) != 1 {
			t.Fatalf("expected exit 1 for non-portable lockfile root, got %d", ExitCode(err))
		}
		detail := doctorCheckDetail(t, report, "lockfile freshness")
		if !strings.Contains(detail, "portable '.' marker") {
			t.Fatalf("expected portable marker detail, got %q", detail)
		}
	})

	t.Run("oversized audit warns", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		auditPath := filepath.Join(repo, ".reconc", "audit.jsonl")
		if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
			t.Fatalf("mkdir .reconc: %v", err)
		}
		payload := bytes.Repeat([]byte("x"), int(doctorAuditWarnBytes)+1)
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

	t.Run("linked audit member warns without following target", func(t *testing.T) {
		repo := makeCheckRepo(t,
			"rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: warn\n    message: generated files are read-only\n")
		auditPath := filepath.Join(repo, ".reconc", "audit.jsonl")
		if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
			t.Fatalf("mkdir .reconc: %v", err)
		}
		target := filepath.Join(repo, "foreign-audit.jsonl")
		if err := os.WriteFile(target, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, auditPath+".1"); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		report, err := runDoctorDeepJSON(t, repo)
		if err != nil {
			t.Fatalf("doctor --deep --json: %v", err)
		}
		if status := doctorCheckStatus(t, report, "audit log size"); status != doctorStatusWarn {
			t.Fatalf("expected linked audit WARN, got %s", status)
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
		for _, raw := range []string{"null\n", "- item\n", "rules: []\n---\nrules: []\n", "base: &base {value: x}\nrules:\n" + strings.Repeat("  - *base\n", yamlbound.MaxAliases+1)} {
			if _, err := decodeDoctorYAML(raw, "bounded.yml"); err == nil || !strings.Contains(err.Error(), "bounded.yml") {
				t.Fatalf("bounded doctor YAML was accepted or lost context: %v", err)
			}
		}
	})

	t.Run("inline blocks and refs", func(t *testing.T) {
		text := "# doc\n```reconc\nrules:\n  - template: docs-follow-code\n```\n"
		blocks, err := ingest.ScanInlinePolicyBlocks("AGENTS.md", text)
		if err != nil {
			t.Fatalf("ScanInlinePolicyBlocks: %v", err)
		}
		if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "template: docs-follow-code") {
			t.Fatalf("unexpected inline blocks: %#v", blocks)
		}
		templatesFound, err := extractTemplateRefs(blocks[0].Content, "inline")
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
		analysis := newDoctorAnalysisContext(discovery)
		if check := doctorCheckAuditSize(discovery); check.Status != doctorStatusWarn {
			t.Fatalf("expected audit size WARN without discovery, got %#v", check)
		}
		if check := doctorCheckSessionClaims(discovery); check.Status != doctorStatusWarn {
			t.Fatalf("expected session claims WARN without discovery, got %#v", check)
		}
		if check := doctorCheckLockfileFreshness(discovery, analysis); check.Status != doctorStatusFail {
			t.Fatalf("expected lockfile freshness FAIL without discovery, got %#v", check)
		}
		if check := doctorCheckConflictCount(discovery, analysis); check.Status != doctorStatusFail {
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

func TestDoctorInlineSourcesMatchAuthoritativeScanner(t *testing.T) {
	var maximum strings.Builder
	for range 512 {
		maximum.WriteString("```reconc\nrules: []\n```\n")
	}
	tests := []struct {
		name string
		text string
	}{
		{name: "plain markdown", text: "# agents\nNo policy here.\n"},
		{name: "lf", text: "before\n```reconc\nrules: []\n```\nafter\n"},
		{name: "crlf and fence whitespace", text: "before\r\n```reconc \t\r\nrules: []\r\n``` \t\r\nafter\r\n"},
		{name: "multiple including empty", text: "```reconc\n```\ntext\n```reconc\nrules: []\n```\n"},
		{name: "unterminated", text: "before\n```reconc\nrules: []\n"},
		{name: "indented opening is prose", text: "  ```reconc\nrules: []\n```\n"},
		{name: "maximum block count", text: maximum.String()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(tt.text), 0o644); err != nil {
				t.Fatal(err)
			}
			path := "AGENTS.md"
			bundle, doctorErr := ingest.LoadPolicySources(repo)
			blocks, scanErr := ingest.ScanInlinePolicyBlocks(path, tt.text)
			if doctorErr != nil || scanErr != nil {
				t.Fatalf("unexpected errors: doctor=%v scanner=%v", doctorErr, scanErr)
			}
			doctorSources := make([]policy.PolicySource, 0, len(blocks))
			for _, source := range bundle.Sources {
				if source.Kind == policy.SourceInlineBlock && source.Path == path {
					doctorSources = append(doctorSources, source)
				}
			}
			if len(doctorSources) != len(blocks) {
				t.Fatalf("doctor found %d blocks; scanner found %d", len(doctorSources), len(blocks))
			}
			for index := range blocks {
				if doctorSources[index].Content != blocks[index].Content {
					t.Fatalf("block %d content differs: doctor=%q scanner=%q", index, doctorSources[index].Content, blocks[index].Content)
				}
				if doctorSources[index].Path != path {
					t.Fatalf("block %d path = %q", index, doctorSources[index].Path)
				}
			}
		})
	}
}

func TestDoctorInlineSourcesRejectExcessiveBlockCount(t *testing.T) {
	var text strings.Builder
	for range 513 {
		text.WriteString("```reconc\nrules: []\n```\n")
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(text.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	path := "AGENTS.md"
	_, doctorErr := ingest.LoadPolicySources(repo)
	_, scanErr := ingest.ScanInlinePolicyBlocks(path, text.String())
	if doctorErr == nil || scanErr == nil {
		t.Fatalf("expected both consumers to reject excessive blocks: doctor=%v scanner=%v", doctorErr, scanErr)
	}
	if !strings.Contains(doctorErr.Error(), scanErr.Error()) {
		t.Fatalf("doctor error %q does not preserve scanner error %q", doctorErr, scanErr)
	}
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
