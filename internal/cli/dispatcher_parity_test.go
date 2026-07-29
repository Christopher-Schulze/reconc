package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
)

// TestMetadataMatchesEveryDispatcherCase proves exact bidirectional parity
// between executable top-level commands and the canonical public contract.
// `help` is a usage alias, not a command, and stays exempt.
func TestMetadataMatchesEveryDispatcherCase(t *testing.T) {
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, "cli.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse cli.go: %v", err)
	}
	dispatcherCases := map[string]bool{}
	ast.Inspect(tree, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			literal, ok := expr.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			name := strings.Trim(literal.Value, `"`)
			if name == "" || strings.HasPrefix(name, "-") {
				continue
			}
			dispatcherCases[name] = true
		}
		return true
	})
	if len(dispatcherCases) < 30 {
		t.Fatalf("dispatcher scan looks broken: only %d cases found", len(dispatcherCases))
	}

	metadata := map[string]bool{}
	for _, command := range commandmeta.All() {
		if metadata[command.Name] {
			t.Fatalf("duplicate metadata command %q", command.Name)
		}
		metadata[command.Name] = true
	}
	missing := []string{}
	for name := range dispatcherCases {
		_, hidden := hiddenCompatibilityCommands[name]
		if name != "help" && !hidden && !metadata[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("dispatcher cases missing from command metadata: %v", missing)
	}
	unreachable := []string{}
	for name := range metadata {
		if !dispatcherCases[name] {
			unreachable = append(unreachable, name)
		}
	}
	if len(unreachable) > 0 {
		sort.Strings(unreachable)
		t.Fatalf("metadata commands missing from dispatcher: %v", unreachable)
	}
}
