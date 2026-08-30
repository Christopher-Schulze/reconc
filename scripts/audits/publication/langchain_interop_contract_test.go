package main

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
)

var pythonStringPattern = regexp.MustCompile(`"(?:\\.|[^"\\])*"`)

func TestLangChainExamplesMatchCanonicalGatewayMetadata(t *testing.T) {
	root := publicSurfaceRoot(t)
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	commands := readPublicSurfaceFile(t, root, "docs/commands.md")

	gateway := findPublicSubcommand(t, "mcp", "gateway")
	if !strings.Contains(commands, "### `"+gateway.Synopsis+"`") {
		t.Fatalf("command reference omits canonical gateway synopsis %q", gateway.Synopsis)
	}
	keyInit := findPublicNestedSubcommand(t, "action", "key", "init")
	if !strings.Contains(commands, "### `"+keyInit.Synopsis+"`") {
		t.Fatalf("command reference omits canonical action-key synopsis %q", keyInit.Synopsis)
	}

	documentationArgs := langChainExampleArgs(t, "docs/documentation.md", documentation)
	commandArgs := langChainExampleArgs(t, "docs/commands.md", commands)
	if !equalStrings(documentationArgs, commandArgs) {
		t.Fatalf("LangChain examples drifted:\ndocumentation=%q\ncommands=%q", documentationArgs, commandArgs)
	}
	want := []string{
		"mcp", "gateway", "/absolute/path/to/repository",
		"--server", "downstream",
		"--expect-lock-digest", "<64-lowercase-hex-lock-digest>",
		"--principal", "langchain-operator",
		"--role", "automation",
		"--environment", "production",
		"--credential", "database-writer",
		"--run", "run-2026-08-12",
		"--session", "session-001",
		"--approval-authorities", "/private/operator/approval-authorities.json",
		"--approval-policy", "default",
		"--timeout", "60s",
		"--reconc-home", "/private/operator/reconc-home",
		"--",
		"/absolute/path/to/downstream-mcp-server",
		"--downstream-flag",
	}
	if !equalStrings(documentationArgs, want) {
		t.Fatalf("canonical LangChain argv = %q, want %q", documentationArgs, want)
	}
	assertExampleFlagsFollowMetadata(t, gateway, documentationArgs)
}

func TestLangChainGatewayIsProminentAndReleaseBlocking(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	releaseWorkflow := readPublicSurfaceFile(t, root, ".github/workflows/reconc-release.yml")

	const heading = "Enforce Agent Tool Calls Before Execution"
	featureIndex := strings.Index(readme, "\n## "+heading+"\n")
	whyIndex := strings.Index(readme, "\n## Why Reconc Exists\n")
	if featureIndex < 0 || whyIndex < 0 || featureIndex > whyIndex {
		t.Fatalf("README must present %q before Why Reconc Exists", heading)
	}
	assertContainsAll(t, "README navigation", readme[:whyIndex],
		"[MCP Gateway](#enforce-agent-tool-calls-before-execution)",
	)
	assertContainsAll(t, "README MCP gateway", markdownSection(t, "README.md", readme, heading),
		"LangChain or MCP client",
		"reconc mcp gateway",
		"policy + trusted context + budgets + approvals + input/result inspection + ledger",
		"operator-selected downstream MCP server",
		"Every explicitly routed tool call is checked before execution.",
		"Reconc remains one Go binary",
		"copy-paste LangChain configuration",
		"Direct downstream MCP connections, native LangChain tools, and host-native tools bypass Reconc",
		"reported as unenforced, never inferred safe",
	)

	assertContainsAll(t, "documentation release contract", documentation,
		"The `LangChain MCP interoperability` check is required for protected `main`",
		"Both exact-tag prerequisite jobs must pass before artifact publication can start",
	)
	gateStart := strings.Index(releaseWorkflow, "\n  langchain-runtime:\n")
	releaseStart := strings.Index(releaseWorkflow, "\n  release:\n")
	if gateStart < 0 || releaseStart < 0 || gateStart > releaseStart {
		t.Fatal("release workflow omits or reorders the LangChain prerequisite job")
	}
	langChainGate := releaseWorkflow[gateStart:releaseStart]
	assertContainsAll(t, "release workflow LangChain job", langChainGate,
		"name: LangChain MCP release gate",
		"actions/setup-python@5fda3b95a4ea91299a34e894583c3862153e4b97",
		"python-version: 3.13.14",
		"python -m pip install --require-hashes -r scripts/tests/langchain-requirements.lock",
		"make test-langchain PYTHON=python",
		"ref: ${{ inputs.tag }}",
	)
	assertContainsAll(t, "release workflow publication job", releaseWorkflow[releaseStart:],
		"needs: [windows-runtime, langchain-runtime]",
		"./scripts/release/publish-github-release.sh",
	)
	if count := strings.Count(releaseWorkflow, "ref: ${{ inputs.tag }}"); count != 3 {
		t.Fatalf("release workflow exact-tag checkouts = %d, want 3", count)
	}
	if count := strings.Count(releaseWorkflow, "make test-langchain PYTHON=python"); count != 1 {
		t.Fatalf("release workflow LangChain gates = %d, want 1", count)
	}
}

func TestLangChainProofPinsVersionsAndUnenforcedBoundary(t *testing.T) {
	root := publicSurfaceRoot(t)
	surfaces := map[string]string{
		"README":        readPublicSurfaceFile(t, root, "README.md"),
		"architecture":  readPublicSurfaceFile(t, root, "docs/architecture.md"),
		"commands":      readPublicSurfaceFile(t, root, "docs/commands.md"),
		"documentation": readPublicSurfaceFile(t, root, "docs/documentation.md"),
		"RFC":           readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0008-go-only-action-plane.md"),
	}
	for name, body := range surfaces {
		assertContainsAll(t, name, body,
			"0.9.8",
			"v1.7.0",
			"0.3.2",
			"1.5.4",
			"1.29.0",
			"2025-11-25",
			"2026-07-28",
		)
	}
	assertContainsAll(t, "documentation", surfaces["documentation"],
		"Python CI runtime",
		"3.13.14",
		"Go downstream fixture",
		"format `1`",
		"unenforced_direct = MultiServerMCPClient",
		"mcp_gateway_scope: \"explicit_routes_only\"",
		"mcp_external_configuration: \"not_inspected\"",
		"mcp_bypass_routes: \"unenforced\"",
		"Transport, session, and conversion failures raise",
	)

	mainSource := readPublicSurfaceFile(t, root, "cmd/reconc/main.go")
	goModule := readPublicSurfaceFile(t, root, "go.mod")
	requirements := readPublicSurfaceFile(t, root, "scripts/tests/langchain-requirements.in")
	integration := readPublicSurfaceFile(t, root, "scripts/tests/langchain-integration.sh")
	workflow := readPublicSurfaceFile(t, root, ".github/workflows/reconc-ci.yml")
	makefile := readPublicSurfaceFile(t, root, "Makefile")
	doctor := readPublicSurfaceFile(t, root, "internal/cli/doctor_deep.go")
	status := readPublicSurfaceFile(t, root, "internal/cli/inspect_cmd.go")

	assertContainsAll(t, "source version", mainSource, `var Version = "0.9.8"`)
	assertContainsAll(t, "Go SDK pin", goModule, "github.com/modelcontextprotocol/go-sdk v1.7.0")
	assertContainsAll(t, "external direct pins", requirements,
		"langchain-core==1.5.4",
		"langchain-mcp-adapters==0.3.2",
		"mcp==1.29.0",
		"typing-extensions==4.16.0",
	)
	assertContainsAll(t, "integration script", integration,
		"reconc_version=0.9.8",
		"go_mcp_sdk_version=v1.7.0",
		`platform.python_version() != "3.13.14"`,
		`LATEST_PROTOCOL_VERSION != "2025-11-25"`,
		`"unenforced-direct"`,
		"direct downstream configuration was incorrectly represented as enforced",
		"socket.socket.connect = deny_network",
		"LangChain Core 1.5.4, MCP Python SDK 1.29.0, Python 3.13.14",
	)
	assertContainsAll(t, "CI", workflow,
		"langchain-integration:",
		"actions/setup-python@5fda3b95a4ea91299a34e894583c3862153e4b97",
		"python-version: 3.13.14",
		"python -m pip install --require-hashes -r scripts/tests/langchain-requirements.lock",
		"make test-langchain PYTHON=python",
	)
	assertContainsAll(t, "Makefile", makefile,
		"test-langchain:",
		`PYTHON="$(PYTHON)" ./scripts/tests/langchain-integration.sh`,
	)
	assertContainsAll(t, "doctor", doctor,
		"external client configuration is not inspected",
		"direct downstream configurations are unenforced",
	)
	assertContainsAll(t, "status", status,
		`"mcp_gateway_scope":          "explicit_routes_only"`,
		`"mcp_external_configuration": "not_inspected"`,
		`"mcp_bypass_routes":          "unenforced"`,
	)
	assertNoLangChainProductAdapter(t, root)
}

func findPublicSubcommand(t *testing.T, commandName, subcommandName string) commandmeta.Subcommand {
	t.Helper()
	command, ok := commandmeta.Lookup(commandName)
	if !ok {
		t.Fatalf("command metadata omits %q", commandName)
	}
	for _, subcommand := range command.Subcommands {
		if subcommand.Name == subcommandName && subcommand.Stability == commandmeta.StabilityStable {
			return subcommand
		}
	}
	t.Fatalf("command metadata omits %s %s", commandName, subcommandName)
	return commandmeta.Subcommand{}
}

func findPublicNestedSubcommand(t *testing.T, commandName, groupName, subcommandName string) commandmeta.Subcommand {
	t.Helper()
	group := findPublicSubcommand(t, commandName, groupName)
	for _, subcommand := range group.Subcommands {
		if subcommand.Name == subcommandName && subcommand.Stability == commandmeta.StabilityStable {
			return subcommand
		}
	}
	t.Fatalf("command metadata omits %s %s %s", commandName, groupName, subcommandName)
	return commandmeta.Subcommand{}
}

func langChainExampleArgs(t *testing.T, name, body string) []string {
	t.Helper()
	section := strings.Index(body, "client = MultiServerMCPClient({")
	if section < 0 {
		t.Fatalf("%s omits the canonical LangChain client", name)
	}
	argsStart := strings.Index(body[section:], `"args": [`)
	if argsStart < 0 {
		t.Fatalf("%s omits the canonical LangChain args", name)
	}
	argsStart += section
	argsEnd := strings.Index(body[argsStart:], "\n        ],")
	if argsEnd < 0 {
		t.Fatalf("%s has no bounded LangChain args block", name)
	}
	block := body[argsStart : argsStart+argsEnd]
	literals := pythonStringPattern.FindAllString(block, -1)
	if len(literals) < 2 || literals[0] != `"args"` {
		t.Fatalf("%s LangChain args block is malformed: %q", name, block)
	}
	values := make([]string, 0, len(literals)-1)
	for _, literal := range literals[1:] {
		value, err := strconv.Unquote(literal)
		if err != nil {
			t.Fatalf("decode %s Python string %q: %v", name, literal, err)
		}
		values = append(values, value)
	}
	return values
}

func assertExampleFlagsFollowMetadata(t *testing.T, gateway commandmeta.Subcommand, args []string) {
	t.Helper()
	indexes := make(map[string]int, len(gateway.Flags))
	for index, flag := range gateway.Flags {
		indexes[flag.Name] = index
	}
	last := -1
	separatorSeen := false
	for _, argument := range args[3:] {
		if argument == "--" {
			separatorSeen = true
			continue
		}
		if !strings.HasPrefix(argument, "--") || separatorSeen {
			continue
		}
		index, ok := indexes[argument]
		if !ok {
			t.Fatalf("LangChain example uses flag absent from command metadata: %s", argument)
		}
		if index <= last {
			t.Fatalf("LangChain example flag %s is out of canonical metadata order", argument)
		}
		last = index
	}
	if !separatorSeen {
		t.Fatal("LangChain example omits the downstream argv separator")
	}
}

func assertNoLangChainProductAdapter(t *testing.T, root string) {
	t.Helper()
	command := exec.Command(
		"git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--",
		"*.py", "*.pyi", "*.ts", "*.tsx",
	)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list possible adapter sources: %v", err)
	}
	for _, path := range strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		body := strings.ToLower(readPublicSurfaceFile(t, root, filepath.ToSlash(path)))
		if strings.Contains(body, "langchain") {
			t.Errorf("Reconc-authored LangChain product adapter source is forbidden: %s", path)
		}
	}
}
