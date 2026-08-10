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

	assertContainsAll(t, "RECONC-0008", rfc,
		"Status: Draft",
		"Implementation state: partially implemented in unreleased source version",
		"TASK 154 implements strict action authoring",
		"TASK 155 implements the pure",
		"gateway enforcement",
		"unavailable until their owning tasks",
	)
	assertContainsAll(t, "RFC index", index,
		"| RECONC-0008 | Draft | Go-only Action Plane |",
	)
	assertContainsAll(t, "architecture", architecture,
		"## Go-Only Action Plane (Draft)",
		"unreleased v0.9.6 source implements strict action",
		"transport-neutral pure evaluator now implements strict",
		"does not yet enforce live tool calls",
	)
	assertContainsAll(t, "documentation", documentation,
		"## Go-Only Action Plane",
		"Unreleased source version `v0.9.6` implements strict",
		"No current source command routes tool calls through",
		"an enforcing gateway",
		"implemented evaluator is not yet a live tool-call interception boundary",
	)
	assertContainsAll(t, "commands", commands,
		"## Remaining Action Plane commands (Draft, unavailable)",
		"`reconc why action` is implemented in unreleased source version `v0.9.6`",
		"The following enforcement and",
		"retained-evidence surfaces remain unavailable",
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
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Errorf("%s omits exact contract text %q", surface, value)
		}
	}
}
