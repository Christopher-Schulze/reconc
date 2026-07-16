package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/completion"
)

// TestCompletionCoversEveryDispatcherCase parses cli.go's dispatch
// switch and asserts every subcommand case has a completion entry, so
// a new `case "foo":` cannot ship without shell-completion coverage.
// (`help` is a usage alias, not a subcommand, and stays exempt.)
func TestCompletionCoversEveryDispatcherCase(t *testing.T) {
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

	covered := map[string]bool{"help": true}
	for _, sub := range completion.Subcommands {
		covered[sub.Name] = true
	}
	missing := []string{}
	for name := range dispatcherCases {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("dispatcher cases missing from completion.Subcommands: %v", missing)
	}
}
