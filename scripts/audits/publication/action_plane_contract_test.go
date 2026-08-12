package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestDraftActionPlaneContractReportsExactImplementationBoundary(t *testing.T) {
	root := publicSurfaceRoot(t)
	rfc := readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0008-go-only-action-plane.md")
	index := readPublicSurfaceFile(t, root, "docs/rfcs/README.md")
	architecture := readPublicSurfaceFile(t, root, "docs/architecture.md")
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	commands := readPublicSurfaceFile(t, root, "docs/commands.md")
	readme := readPublicSurfaceFile(t, root, "README.md")

	assertContainsAll(t, "RECONC-0008", rfc,
		"Status: Draft",
		"Implementation state: partially implemented in source and release version",
		"TASK 154 implements strict action authoring",
		"TASK 155 implements the pure",
		"TASK 156 implements strict format-2 action scenarios",
		"TASK 159 implements canonical detector policy",
		"TASK 160 implements",
		"the separate private format-1 retained Action Ledger",
		"TASK 161 implements the enforcing Go MCP stdio gateway",
		"TASK 163 implements deterministic privacy-bounded control-evidence export",
		"`reconc mcp gateway` is implemented for explicitly routed tools",
	)
	assertContainsAll(t, "RFC index", index,
		"| RECONC-0008 | Draft | Go-only Action Plane |",
	)
	assertContainsAll(t, "architecture", architecture,
		"## Go-Only Action Plane (Draft)",
		"v0.9.6 implements strict action",
		"transport-neutral pure evaluator now implements strict",
		"`internal/actioninspect` now strictly",
		"Action Ledger records typed payload-free lifecycle evidence",
		"`reconc action log tail|stats|verify|export`",
		"`internal/actionevidence` now derives exact facts",
		"`reconc action evidence export|verify`",
		"`reconc mcp gateway` invokes the enforcement primitives around every routed",
		"The implemented topology is one local, tool-only stdio MCP gateway",
		"Native LangChain tools, clients configured directly against the downstream server",
	)
	assertContainsAll(t, "documentation", documentation,
		"## Go-Only Action Plane",
		"Source and release version `v0.9.6` implements strict",
		"`reconc impact` invokes that production evaluator",
		"`reconc action log tail|stats|verify|export`",
		"`reconc mcp gateway` owns one operator-selected downstream stdio MCP process",
		"only explicitly routed gateway calls cross the live tool-call interception boundary",
		"Only tools configured to use the Reconc gateway are enforced",
	)
	assertContainsAll(t, "commands", commands,
		"## Action Plane commands",
		"`reconc why action` is implemented in source and release version `v0.9.6`",
		"### `reconc action log tail",
		"### `reconc action log stats",
		"### `reconc action log verify",
		"### `reconc action log export",
		"### `reconc mcp gateway",
		"deterministic action-inspection core",
		"controls apply only to calls routed through `reconc mcp gateway`",
		"### `reconc action evidence export",
		"### `reconc action evidence verify",
		"Gateway and evidence commands are registered in dispatch",
	)
	assertContainsAll(t, "README", readme,
		"routes explicitly configured tools through `reconc mcp gateway`",
		"The Go-only `reconc mcp gateway` is live enforcement only for tools explicitly routed through it",
		"native LangChain tools",
		"MCP gateway",
		"Action control evidence",
		"reconc action evidence export|verify",
	)
}

func TestDraftActionPlaneContractOwnsEveryFrozenVectorFamily(t *testing.T) {
	root := publicSurfaceRoot(t)
	rfc := readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0008-go-only-action-plane.md")
	families := []string{
		"EXT-", "OWN-", "TRUST-", "AUTH-", "CFG-", "TOOL-", "DEFAULT-",
		"RULE-", "REQ-", "PHASE-", "AST-", "PRED-EXISTS-", "PRED-EQ-", "PRED-NEQ-",
		"PRED-IN-", "PRED-NOTIN-", "PRED-PREFIX-", "PRED-SUFFIX-",
		"PRED-CONTAINS-", "PRED-GLOB-", "PRED-REGEX-", "PRED-GT-",
		"PRED-GTE-", "PRED-LT-", "PRED-LTE-", "PRED-URL-", "PRED-CIDR-",
		"PRED-PATH-", "DEC-", "ERR-", "ID-", "CACHE-", "BUD-", "APPROVAL-",
		"SCAN-", "LEDGER-", "LIMIT-", "TIME-", "FAIL-", "IMPACT-", "CONTROL-",
		"CLI-", "COMPAT-",
	}
	for _, family := range families {
		if count := strings.Count(rfc, "`"+family); count < 2 {
			t.Errorf("RECONC-0008 vector family %q appears %d times, want owner plus executable vector", family, count)
		}
	}
}

func TestDraftActionPlaneContractTablesHaveExactOwnersVectorsAndEvolution(t *testing.T) {
	root := publicSurfaceRoot(t)
	rfc := readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0008-go-only-action-plane.md")
	lines := strings.Split(rfc, "\n")
	found := make(map[string]bool)
	for index, line := range lines {
		if !strings.HasPrefix(line, "Contract table AP-T") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("malformed contract table declaration %q", line)
		}
		id := strings.TrimSuffix(fields[2], ".")
		if found[id] {
			t.Errorf("duplicate contract table owner declaration %s", id)
		}
		found[id] = true
		end := index + 4
		if end > len(lines) {
			end = len(lines)
		}
		declaration := strings.Join(lines[index:end], " ")
		assertContainsAll(t, id, declaration, "Owner:", "Vectors:", "Evolution:")
	}
	for number := 1; number <= 25; number++ {
		id := fmt.Sprintf("AP-T%02d", number)
		if !found[id] {
			t.Errorf("RECONC-0008 omits contract table ownership declaration %s", id)
		}
	}
	if len(found) != 25 {
		t.Errorf("RECONC-0008 contract table count = %d, want 25", len(found))
	}
}

func assertContainsAll(t *testing.T, surface, body string, values ...string) {
	t.Helper()
	normalizedBody := strings.Join(strings.Fields(body), " ")
	for _, value := range values {
		if !strings.Contains(normalizedBody, strings.Join(strings.Fields(value), " ")) {
			t.Errorf("%s omits exact contract text %q", surface, value)
		}
	}
}
