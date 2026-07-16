package agentsession

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoExecCommandTakesNonLiteralBinary is the source-scan guard cited
// by the threat model (docs/architecture.md "Command-injection"):
// payload command strings are data, never executed. Every
// exec.Command/exec.CommandContext call in this package must name its
// binary as a string literal ("git"), so no payload-derived value can
// ever become the executed program.
//
// The policy script runner in internal/runtime (buildScriptCommand)
// deliberately executes a variable path - that path comes from the
// parser-validated lockfile (repo-relative, no escapes), never from a
// hook payload, and is out of this package's scope.
func TestNoExecCommandTakesNonLiteralBinary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(tree, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			var binaryArg ast.Expr
			switch selector.Sel.Name {
			case "Command":
				if len(call.Args) > 0 {
					binaryArg = call.Args[0]
				}
			case "CommandContext":
				if len(call.Args) > 1 {
					binaryArg = call.Args[1]
				}
			default:
				return true
			}
			checked++
			position := fset.Position(call.Pos())
			literal, ok := binaryArg.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Errorf("%s: exec.%s binary must be a string literal, got %T", position, selector.Sel.Name, binaryArg)
				return true
			}
			return true
		})
	}
	if checked == 0 {
		t.Log("no exec.Command call sites in package (guard still active for future additions)")
	}
	// Guard the guard: the scan must actually see the known git call.
	if _, err := os.Stat(filepath.Join(".", "stop_cache.go")); err == nil && checked == 0 {
		t.Fatal("expected at least the stop_cache git invocation to be scanned")
	}
}
