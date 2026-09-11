package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

// The scenario corpus is deliberately small. Each entry starts from a real
// repository, expands one or more named templates, and drives the public hook
// boundary. The effect is performed only after the pre decision so a denied
// route has a measurable before/after filesystem invariant.
type templateHookScenario struct {
	name      string
	policy    string
	initial   map[string]string
	caseName  string
	protected string
	allowed   string
	sessionID string
}

const generatedRecipeScenarioPolicy = `rules:
  - id: deny-generated
    template: no-generated-writes
  - id: generated-gate
    template: generated-artifact-consistency
    script: scripts/check-generated.sh
    when_paths: ['src/**']
    args: ['--source', '0123456789abcdef0123456789abcdef01234567', '--outputs', 'generated/out.txt']
    cache_inputs: ['scripts/check-generated.sh']
`

var templateHookScenarioCorpus = []templateHookScenario{
	{
		name:      "prevention-before-effect",
		policy:    "rules:\n  - id: deny-generated\n    template: no-generated-writes\n",
		initial:   map[string]string{"generated/blocked.go": "original\n"},
		caseName:  "prevention",
		protected: "generated/blocked.go",
		sessionID: "scenario-prevent",
	},
	{
		name:      "stop-only-recipe-detection",
		policy:    generatedRecipeScenarioPolicy,
		initial:   map[string]string{"scripts/check-generated.sh": "#!/bin/sh\nprintf '%s\\n' '{\"contract\":\"generated-artifact-consistency\",\"result\":\"block\",\"source\":\"0123456789abcdef0123456789abcdef01234567\",\"outputs\":[\"generated/out.txt\"],\"evidence\":\"stale-generator-proof\"}'\nexit 2\n"},
		caseName:  "stop-only",
		allowed:   "src/app.go",
		sessionID: "scenario-stop-only",
	},
	{
		name: "prior-authorization",
		policy: `rules:
  - id: ci-claim
    template: ci-green-before-merge
    mode: block
    when_paths: ['docs/**']
`,
		initial:   map[string]string{"docs/README.md": "before\n"},
		caseName:  "claim",
		allowed:   "docs/README.md",
		sessionID: "scenario-claim",
	},
	{
		name: "composite-prevention",
		policy: `rules:
  - id: deny-generated
    template: no-generated-writes
  - id: generated-composite
    kind: all_of
    when_paths: ['generated/**']
    mode: block
    message: composite generated boundary
    checks:
      - kind: deny_write
        paths: ['generated/**']
      - kind: require_script
        script: scripts/missing-generator.sh
`,
		initial:   map[string]string{"generated/blocked.go": "original\n"},
		caseName:  "composite",
		protected: "generated/blocked.go",
		sessionID: "scenario-composite",
	},
	{
		name:      "stale-repeated-tool-id",
		policy:    "rules:\n  - id: deny-generated\n    template: no-generated-writes\n",
		initial:   map[string]string{"docs/first.md": "before\n"},
		caseName:  "stale-tool-id",
		allowed:   "docs/first.md",
		protected: "generated/replayed.go",
		sessionID: "scenario-replay",
	},
	{
		name:      "long-session-evidence",
		policy:    "rules:\n  - id: deny-generated\n    template: no-generated-writes\n",
		initial:   map[string]string{},
		caseName:  "long-session",
		sessionID: "scenario-long",
	},
}

func TestHookTemplateScenarioCorpus(t *testing.T) {
	for _, scenario := range templateHookScenarioCorpus {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			repo := newTask499ScenarioRepo(t, scenario.policy, scenario.initial)
			assertTemplateExpansion(t, repo, scenario.caseName)
			switch scenario.caseName {
			case "prevention":
				runTask499PreventionScenario(t, repo, scenario)
			case "stop-only":
				runTask499StopOnlyScenario(t, repo, scenario)
			case "claim":
				runTask499ClaimScenario(t, repo, scenario)
			case "composite":
				runTask499CompositeScenario(t, repo, scenario)
			case "stale-tool-id":
				runTask499StaleToolIDScenario(t, repo, scenario)
			case "long-session":
				runTask499LongSessionScenario(t, repo, scenario)
			default:
				t.Fatalf("unknown scenario %q", scenario.caseName)
			}
		})
	}
}

func newTask499ScenarioRepo(t *testing.T, policyText string, initial map[string]string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	repo := t.TempDir()
	writeTask499File(t, repo, "AGENTS.md", "# scenario repository\n")
	writeTask499File(t, repo, "policies/rules.yml", policyText)
	for path, content := range initial {
		writeTask499File(t, repo, path, content)
	}
	if strings.Contains(policyText, "scripts/check-generated.sh") {
		path := filepath.Join(repo, "scripts", "check-generated.sh")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatalf("make scenario script executable: %v", err)
		}
	}
	if _, err := compiler.CompileRepoPolicy(repo, "task-499-scenarios"); err != nil {
		t.Fatalf("compile scenario policy: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		return resolved
	}
	return repo
}

func writeTask499File(t *testing.T, repo, relative, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func assertTemplateExpansion(t *testing.T, repo, scenario string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatalf("read compiled policy for %s: %v", scenario, err)
	}
	if !strings.Contains(string(body), `"template_dependencies"`) {
		t.Fatalf("scenario %s lock omitted template provenance", scenario)
	}
	lock := string(body)
	if scenario == "stop-only" && !strings.Contains(lock, `"kind": "require_script"`) {
		t.Fatalf("scenario %s did not expand require_script template", scenario)
	}
	if (scenario == "prevention" || scenario == "composite" || scenario == "stale-tool-id") && !strings.Contains(lock, `"kind": "deny_write"`) {
		t.Fatalf("scenario %s did not expand deny_write template", scenario)
	}
}

func runTask499PreventionScenario(t *testing.T, repo string, scenario templateHookScenario) {
	runTask499SessionStart(t, repo, scenario.sessionID)
	before := readTask499File(t, repo, scenario.protected)
	stdout, stderr, code := runWithStdin(t,
		task499WritePayload(scenario.sessionID, scenario.protected, "blocked-call"),
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "deny-generated") {
		t.Fatalf("denied pre-action = code %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after := readTask499File(t, repo, scenario.protected)
	if after != before {
		t.Fatalf("denied pre-action changed target from %q to %q", before, after)
	}
}

func runTask499StopOnlyScenario(t *testing.T, repo string, scenario templateHookScenario) {
	runTask499SessionStart(t, repo, scenario.sessionID)
	preOut, preErr, preCode := runWithStdin(t,
		task499WritePayload(scenario.sessionID, scenario.allowed, "stop-only-call"),
		"hook", "runtime", "claude-pre-tool-use", repo)
	if preCode != 0 || preOut != "" || preErr != "" {
		t.Fatalf("require_script pre-action unexpectedly blocked or emitted output: code=%d stdout=%q stderr=%q", preCode, preOut, preErr)
	}
	writeTask499File(t, repo, scenario.allowed, "candidate\n")
	postOut, postErr, postCode := runWithStdin(t,
		task499WritePayload(scenario.sessionID, scenario.allowed, "stop-only-call"),
		"hook", "runtime", "claude-post-tool-use", repo)
	if postCode != 0 || postOut != "" || postErr != "" {
		t.Fatalf("post evidence failed: code=%d stdout=%q stderr=%q", postCode, postOut, postErr)
	}
	state, err := agentsession.LoadSessionState(repo, scenario.sessionID)
	if err != nil {
		t.Fatalf("load post evidence: %v", err)
	}
	if !task499Contains(state.WritePaths, scenario.allowed) {
		t.Fatalf("post evidence omitted allowed effect %q: %#v", scenario.allowed, state.WritePaths)
	}
	stopOut, stopErr, stopCode := runWithStdin(t,
		fmt.Sprintf(`{"session_id":%q}`, scenario.sessionID),
		"hook", "runtime", "claude-stop", repo)
	if stopCode != 0 || stopErr != "" || !strings.Contains(stopOut, `"decision":"block"`) || !strings.Contains(stopOut, "generated-gate") {
		t.Fatalf("Stop-only gate result = code %d stdout=%q stderr=%q", stopCode, stopOut, stopErr)
	}
	if got := readTask499File(t, repo, scenario.allowed); got != "candidate\n" {
		t.Fatalf("Stop-only detection lost the already-performed effect: %q", got)
	}
}

func runTask499ClaimScenario(t *testing.T, repo string, scenario templateHookScenario) {
	runTask499SessionStart(t, repo, scenario.sessionID)
	preOut, preErr, preCode := runWithStdin(t,
		task499WritePayload(scenario.sessionID, scenario.allowed, "claim-call"),
		"hook", "runtime", "claude-pre-tool-use", repo)
	if preCode != 0 || preOut != "" || preErr != "" {
		t.Fatalf("claim scenario pre-action = code %d stdout=%q stderr=%q", preCode, preOut, preErr)
	}
	writeTask499File(t, repo, scenario.allowed, "after-write\n")
	if _, stderr, code := runWithStdin(t, task499WritePayload(scenario.sessionID, scenario.allowed, "claim-call"), "hook", "runtime", "claude-post-tool-use", repo); code != 0 || stderr != "" {
		t.Fatalf("claim scenario post-action = code %d stderr=%q", code, stderr)
	}
	blocked, stderr, code := runWithStdin(t, fmt.Sprintf(`{"session_id":%q}`, scenario.sessionID), "hook", "runtime", "claude-stop", repo)
	if code != 0 || stderr != "" || !strings.Contains(blocked, `"decision":"block"`) || !strings.Contains(blocked, "ci-claim") {
		t.Fatalf("missing prior authorization was not blocked: code=%d stdout=%q stderr=%q", code, blocked, stderr)
	}
	claimOut, claimErr, claimCode := runWithStdin(t, "", "hook", "claim", repo, "ci-green", "--session", scenario.sessionID)
	if claimCode != 0 || claimErr != "" || !strings.Contains(claimOut, "ci-green") {
		t.Fatalf("record prior authorization claim = code %d stdout=%q stderr=%q", claimCode, claimOut, claimErr)
	}
	allowed, allowedErr, allowedCode := runWithStdin(t, fmt.Sprintf(`{"session_id":%q}`, scenario.sessionID), "hook", "runtime", "claude-stop", repo)
	if allowedCode != 0 || allowed != "" || allowedErr != "" {
		t.Fatalf("authorized Stop = code %d stdout=%q stderr=%q", allowedCode, allowed, allowedErr)
	}
}

func runTask499CompositeScenario(t *testing.T, repo string, scenario templateHookScenario) {
	runTask499SessionStart(t, repo, scenario.sessionID)
	before := readTask499File(t, repo, scenario.protected)
	stdout, stderr, code := runWithStdin(t,
		task499WritePayload(scenario.sessionID, scenario.protected, "composite-call"),
		"hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "deny-generated") {
		t.Fatalf("composite pre-action = code %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if after := readTask499File(t, repo, scenario.protected); after != before {
		t.Fatalf("composite deny changed target from %q to %q", before, after)
	}
}

func runTask499StaleToolIDScenario(t *testing.T, repo string, scenario templateHookScenario) {
	runTask499SessionStart(t, repo, scenario.sessionID)
	firstPayload := task499WritePayload(scenario.sessionID, scenario.allowed, "repeated-tool")
	if stdout, stderr, code := runWithStdin(t, firstPayload, "hook", "runtime", "claude-pre-tool-use", repo); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("first repeated tool id decision = code %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	writeTask499File(t, repo, scenario.allowed, "written\n")
	if _, stderr, code := runWithStdin(t, firstPayload, "hook", "runtime", "claude-post-tool-use", repo); code != 0 || stderr != "" {
		t.Fatalf("first repeated tool id evidence = code %d stderr=%q", code, stderr)
	}
	secondPayload := task499WritePayload(scenario.sessionID, scenario.protected, "repeated-tool")
	stdout, stderr, code := runWithStdin(t, secondPayload, "hook", "runtime", "claude-pre-tool-use", repo)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "deny-generated") {
		t.Fatalf("stale repeated tool id reused an allow: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(scenario.protected))); !os.IsNotExist(err) {
		t.Fatalf("stale repeated tool id caused the protected effect, stat error=%v", err)
	}
}

func runTask499LongSessionScenario(t *testing.T, repo string, scenario templateHookScenario) {
	runTask499SessionStart(t, repo, scenario.sessionID)
	const events = 72
	for index := 0; index < events; index++ {
		path := fmt.Sprintf("docs/session-%03d.md", index)
		payload := task499WritePayload(scenario.sessionID, path, fmt.Sprintf("long-call-%03d", index))
		if stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "claude-pre-tool-use", repo); code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("long-session pre %d = code %d stdout=%q stderr=%q", index, code, stdout, stderr)
		}
		writeTask499File(t, repo, path, "session evidence\n")
		if stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "claude-post-tool-use", repo); code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("long-session post %d = code %d stdout=%q stderr=%q", index, code, stdout, stderr)
		}
	}
	state, err := agentsession.LoadSessionState(repo, scenario.sessionID)
	if err != nil {
		t.Fatalf("load long-session state: %v", err)
	}
	if len(state.WritePaths) != events {
		t.Fatalf("long-session write evidence count = %d, want %d", len(state.WritePaths), events)
	}
	stdout, stderr, code := runWithStdin(t, fmt.Sprintf(`{"session_id":%q}`, scenario.sessionID), "hook", "runtime", "claude-stop", repo)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("long-session Stop = code %d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func runTask499SessionStart(t *testing.T, repo, sessionID string) {
	t.Helper()
	if stdout, stderr, code := runWithStdin(t, fmt.Sprintf(`{"session_id":%q}`, sessionID), "hook", "runtime", "claude-session-start", repo); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("session start = code %d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func task499WritePayload(sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"session_id":%q,"tool_use_id":%q,"tool_name":"Write","tool_input":{"file_path":%q,"content":"candidate"}}`, sessionID, toolID, path)
}

func readTask499File(t *testing.T, repo, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(body)
}

func task499Contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type task499HostContract struct {
	kind             string
	transport        string
	executeGenerated bool
	prePayload       func(repo, sessionID, path, toolID string) string
	response         string
}

func TestHookScenarioHostCapabilityMatrix(t *testing.T) {
	contracts := task499HostContracts()
	seen := map[string]bool{}
	for _, contract := range contracts {
		contract := contract
		t.Run(contract.kind, func(t *testing.T) {
			seen[contract.kind] = true
			platform, ok := hooks.PlatformForKind(contract.kind)
			if !ok {
				t.Fatalf("host contract has no registry platform")
			}
			preRoute, preOK := hooks.RuntimeEventFor(contract.kind, hooks.EventPreToolUse)
			if !preOK || preRoute == "" {
				t.Fatalf("host contract has no pre-action runtime route")
			}
			stopRoute, stopOK := hooks.RuntimeEventFor(contract.kind, hooks.EventStop)
			if !stopOK || stopRoute == "" {
				t.Fatalf("host contract has no Stop runtime route")
			}
			artifact, err := hooks.Generate(contract.kind)
			if err != nil {
				t.Fatalf("generate adapter: %v", err)
			}
			if !strings.Contains(artifact.Content, preRoute) || !strings.Contains(artifact.Content, stopRoute) {
				t.Fatalf("generated %s adapter lost routes pre=%q stop=%q", contract.kind, preRoute, stopRoute)
			}
			if contract.transport == "bun" && !strings.Contains(artifact.Content, "reconc hook worker") {
				t.Fatalf("Bun adapter %s does not expose the bounded worker transport", contract.kind)
			}
			if contract.transport == "global-receipt" && !strings.Contains(artifact.Content, "receipt-v1") {
				t.Fatalf("global adapter %s does not expose its receipt contract", contract.kind)
			}
			preCapability := capabilityForTask499(platform, hooks.EventPreToolUse)
			if preCapability.Support == hooks.SupportUnsupported {
				t.Fatalf("host contract unexpectedly marks pre-action unsupported")
			}
		})
	}
	for _, platform := range hooks.AgentPlatforms() {
		if !seen[platform.Kind] {
			t.Errorf("registry platform %s has no explicit TASK 499 host contract", platform.Kind)
		}
	}
}

func capabilityForTask499(platform hooks.Platform, event hooks.Event) hooks.Capability {
	for _, capability := range platform.Capabilities {
		if capability.Event == event {
			return capability
		}
	}
	return hooks.Capability{Support: hooks.SupportUnsupported}
}

func task499HostContracts() []task499HostContract {
	return []task499HostContract{
		{kind: hooks.KindClaudeCode, transport: "native-json", executeGenerated: true, prePayload: task499ClaudePayload, response: "exit-block"},
		{kind: hooks.KindCodex, transport: "native-json", executeGenerated: true, prePayload: task499ClaudePayload, response: "exit-block"},
		{kind: hooks.KindGitHubCopilot, transport: "native-json", executeGenerated: true, prePayload: task499CopilotPayload, response: "copilot-deny"},
		{kind: hooks.KindCursor, transport: "native-json", executeGenerated: true, prePayload: task499CursorPayload, response: "cursor-deny"},
		{kind: hooks.KindOpenCode, transport: "bun", executeGenerated: false, prePayload: task499PluginPayload, response: "exit-block"},
		{kind: hooks.KindDevinCLI, transport: "native-json", executeGenerated: true, prePayload: task499DevinPayload, response: "exit-block"},
		{kind: hooks.KindAntigravity, transport: "native-json", executeGenerated: true, prePayload: task499AntigravityPayload, response: "antigravity-deny"},
		{kind: hooks.KindKilo, transport: "bun", executeGenerated: false, prePayload: task499PluginPayload, response: "exit-block"},
		{kind: hooks.KindGrok, transport: "native-json", executeGenerated: true, prePayload: task499GrokPayload, response: "grok-deny"},
		{kind: hooks.KindOMP, transport: "bun", executeGenerated: false, prePayload: task499OMPPayload, response: "exit-block"},
		{kind: hooks.KindPi, transport: "bun", executeGenerated: false, prePayload: task499PiPayload, response: "exit-block"},
		{kind: hooks.KindZCode, transport: "native-json", executeGenerated: true, prePayload: task499ZCodePayload, response: "exit-block"},
		{kind: hooks.KindKimiCode, transport: "global-receipt", executeGenerated: false, prePayload: task499KimiPayload, response: "exit-block"},
	}
}

func TestHookScenarioHostPreDenialUsesEachEnvelope(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	for _, contract := range task499HostContracts() {
		contract := contract
		t.Run(contract.kind, func(t *testing.T) {
			repo := newTask499ScenarioRepo(t, "rules:\n  - id: deny-generated\n    template: no-generated-writes\n", map[string]string{"generated/blocked.go": "original\n"})
			if contract.kind == hooks.KindGrok {
				t.Setenv("GROK_SESSION_ID", "task499-grok")
			}
			payload := contract.prePayload(repo, "task499-"+contract.kind, "generated/blocked.go", "task499-pre")
			before := readTask499File(t, repo, "generated/blocked.go")
			stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", mustTask499PreRoute(t, contract.kind), repo)
			assertTask499PreResponse(t, contract, stdout, stderr, code)
			if after := readTask499File(t, repo, "generated/blocked.go"); after != before {
				t.Fatalf("%s denied pre-action changed the target", contract.kind)
			}
		})
	}
}

func mustTask499PreRoute(t *testing.T, kind string) string {
	t.Helper()
	route, ok := hooks.RuntimeEventFor(kind, hooks.EventPreToolUse)
	if !ok {
		t.Fatalf("missing pre route for %s", kind)
	}
	return route
}

func assertTask499PreResponse(t *testing.T, contract task499HostContract, stdout, stderr string, code int) {
	t.Helper()
	switch contract.response {
	case "copilot-deny":
		if code != 0 || stderr != "" || !strings.Contains(stdout, `"permissionDecision":"deny"`) || !strings.Contains(stdout, "deny-generated") {
			t.Fatalf("Copilot envelope = code %d stdout=%q stderr=%q", code, stdout, stderr)
		}
	case "cursor-deny":
		if code != 0 || !strings.Contains(stdout, `"permission":"deny"`) || !strings.Contains(stdout, "deny-generated") {
			t.Fatalf("Cursor envelope = code %d stdout=%q stderr=%q", code, stdout, stderr)
		}
	case "grok-deny", "antigravity-deny":
		if code != 0 || stderr != "" || !strings.Contains(stdout, `"decision":"deny"`) || !strings.Contains(stdout, "deny-generated") {
			t.Fatalf("%s envelope = code %d stdout=%q stderr=%q", contract.kind, code, stdout, stderr)
		}
	default:
		if code != 2 || stdout != "" || !strings.Contains(stderr, "deny-generated") {
			t.Fatalf("%s exit envelope = code %d stdout=%q stderr=%q", contract.kind, code, stdout, stderr)
		}
	}
}

func TestGeneratedAdaptersExecuteTemplateDenial(t *testing.T) {
	for _, contract := range task499HostContracts() {
		contract := contract
		if !contract.executeGenerated {
			continue
		}
		t.Run(contract.kind, func(t *testing.T) {
			repo := newTask499ScenarioRepo(t, "rules:\n  - id: deny-generated\n    template: no-generated-writes\n", map[string]string{"generated/blocked.go": "original\n"})
			artifact, err := hooks.Generate(contract.kind)
			if err != nil {
				t.Fatalf("generate adapter: %v", err)
			}
			if artifact.TargetPath != "" && !strings.HasPrefix(artifact.TargetPath, "~/") {
				path := filepath.Join(repo, filepath.FromSlash(artifact.TargetPath))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create generated artifact directory: %v", err)
				}
				if err := os.WriteFile(path, []byte(artifact.Content), 0o644); err != nil {
					t.Fatalf("write generated artifact: %v", err)
				}
			}
			command := task499GeneratedRouteCommand(t, artifact.Content, mustTask499PreRoute(t, contract.kind))
			writeTask499ChildWrapper(t, repo)
			if contract.kind == hooks.KindGrok {
				t.Setenv("GROK_SESSION_ID", "task499-generated-grok")
			}
			cmd := exec.Command("/bin/sh", "-c", command)
			cmd.Dir = repo
			cmd.Stdin = strings.NewReader(contract.prePayload(repo, "task499-generated-"+contract.kind, "generated/blocked.go", "generated-pre"))
			cmd.Env = append(os.Environ(),
				"RECONC_SCENARIO_TEST_BINARY="+os.Args[0],
				"CLAUDE_PROJECT_DIR="+repo,
				"DEVIN_PROJECT_DIR="+repo,
			)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
			code := task499CommandExitCode(err)
			assertTask499PreResponse(t, contract, stdout.String(), stderr.String(), code)
			if after := readTask499File(t, repo, "generated/blocked.go"); after != "original\n" {
				t.Fatalf("generated %s adapter allowed the side effect: %q", contract.kind, after)
			}
		})
	}
}

// TestHookScenarioChild is a real child-process boundary used by generated
// adapters. It is inert during the ordinary package run.
func TestHookScenarioChild(t *testing.T) {
	if os.Getenv("RECONC_SCENARIO_CHILD") != "1" {
		return
	}
	event := os.Getenv("RECONC_SCENARIO_EVENT")
	repo := os.Getenv("RECONC_SCENARIO_REPO")
	err := Run([]string{"hook", "runtime", event, repo}, "task-499-child", os.Stdout, os.Stderr)
	if code := ExitCode(err); code != 0 {
		os.Exit(code)
	}
}

func writeTask499ChildWrapper(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create generated wrapper directory: %v", err)
	}
	const wrapper = `#!/bin/sh
set -eu
export RECONC_SCENARIO_CHILD=1
export RECONC_SCENARIO_EVENT="$1"
export RECONC_SCENARIO_REPO="$2"
exec "$RECONC_SCENARIO_TEST_BINARY" -test.run '^TestHookScenarioChild$' -test.v=false
`
	if err := os.WriteFile(path, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write generated wrapper: %v", err)
	}
}

func task499CommandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func task499GeneratedRouteCommand(t *testing.T, content, route string) string {
	t.Helper()
	var document interface{}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("native generated artifact is not JSON: %v", err)
	}
	command := findTask499GeneratedCommand(document, route)
	if command == "" {
		t.Fatalf("generated artifact has no executable command for route %q", route)
	}
	return command
}

func findTask499GeneratedCommand(value interface{}, route string) string {
	switch typed := value.(type) {
	case map[string]interface{}:
		if bash, ok := typed["bash"].(string); ok && strings.Contains(bash, route) {
			return bash
		}
		if command, ok := typed["command"].(string); ok {
			if args, ok := typed["args"].([]interface{}); ok {
				parts := []string{command}
				for _, raw := range args {
					arg, ok := raw.(string)
					if !ok {
						break
					}
					parts = append(parts, task499ShellArg(arg))
				}
				candidate := strings.Join(parts, " ")
				if strings.Contains(candidate, route) {
					return candidate
				}
			}
			if strings.Contains(command, route) {
				return command
			}
		}
		for _, child := range typed {
			if found := findTask499GeneratedCommand(child, route); found != "" {
				return found
			}
		}
	case []interface{}:
		for _, child := range typed {
			if found := findTask499GeneratedCommand(child, route); found != "" {
				return found
			}
		}
	}
	return ""
}

func task499ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func task499ShellArg(value string) string {
	if strings.Contains(value, "$") {
		return value
	}
	return task499ShellQuote(value)
}

func task499ClaudePayload(repo, sessionID, path, toolID string) string {
	return task499WritePayload(sessionID, path, toolID)
}

func task499CopilotPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"cwd":%q,"tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"original","new_string":"candidate"},"tool_use_id":%q}`, sessionID, repo, path, toolID)
}

func task499CursorPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"conversation_id":%q,"tool_name":"Write","tool_input":{"filePath":%q},"tool_use_id":%q}`, sessionID, path, toolID)
}

func task499PluginPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"session_id":%q,"reconc_runtime":"plugin","tool_name":"Write","tool_input":{"file_path":%q},"tool_use_id":%q}`, sessionID, path, toolID)
}

func task499DevinPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"cwd":%q,"tool_name":"edit","tool_input":{"file_path":%q},"tool_use_id":%q}`, sessionID, repo, path, toolID)
}

func task499AntigravityPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"conversationId":%q,"stepIdx":7,"toolCall":{"name":"write_to_file","args":{"TargetFile":%q,"内容":"candidate"}}}`, sessionID, path)
}

func task499GrokPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hookEventName":"pre_tool_use","sessionId":%q,"workspaceRoot":%q,"toolName":"search_replace","toolUseId":%q,"toolInput":{"path":%q,"old_string":"original","new_string":"candidate"},"toolInputTruncated":false}`, sessionID, repo, toolID, path)
}

func task499OMPPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":%q,"cwd":%q,"tool_name":"write","tool_input":{"path":%q},"tool_call_id":%q}`, sessionID, repo, path, toolID)
}

func task499PiPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hook_event_name":"tool_call","session_id":%q,"cwd":%q,"tool_name":"write","tool_input":{"path":%q},"tool_call_id":%q}`, sessionID, repo, path, toolID)
}

func task499ZCodePayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"cwd":%q,"tool_name":"Write","tool_input":{"file_path":%q},"tool_use_id":%q}`, sessionID, repo, path, toolID)
}

func task499KimiPayload(repo, sessionID, path, toolID string) string {
	return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"cwd":%q,"tool_name":"Write","tool_input":{"path":%q},"tool_call_id":%q}`, sessionID, repo, path, toolID)
}
