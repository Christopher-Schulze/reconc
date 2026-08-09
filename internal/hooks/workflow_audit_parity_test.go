package hooks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestPortableWorkflowAuditRequiresEveryRegisteredRoute(t *testing.T) {
	body, err := os.ReadFile("../../harness/template/audits/workflow-audit.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "workflow-audit.go", body, 0)
	if err != nil {
		t.Fatal(err)
	}
	auditedRoutes := auditAgentHookRoutes(t, parsed)
	for _, platform := range RepositoryAgentPlatforms() {
		got := auditedRoutes[platform.TargetPath]
		want := platformRuntimeEvents(platform)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("portable audit routes for %s (%s) = %v, want registry routes %v", platform.Kind, platform.TargetPath, got, want)
		}
	}
}

func auditAgentHookRoutes(t *testing.T, file *ast.File) map[string][]string {
	t.Helper()
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "auditAgentHooks" {
			function = candidate
			break
		}
	}
	if function == nil {
		t.Fatal("auditAgentHooks is missing")
	}
	routes := map[string][]string{}
	routePrefixes := registeredRuntimeRoutePrefixes()
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		index, ok := assignment.Lhs[0].(*ast.IndexExpr)
		if !ok {
			return true
		}
		identifier, ok := index.X.(*ast.Ident)
		if !ok || identifier.Name != "hooks" {
			return true
		}
		path := joinedAuditPath(index.Index)
		values, ok := assignment.Rhs[0].(*ast.CompositeLit)
		if path == "" || !ok {
			return true
		}
		for _, element := range values.Elts {
			literal, ok := element.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("decode audit token %s: %v", literal.Value, err)
			}
			for prefix := range routePrefixes {
				if strings.HasPrefix(value, prefix) {
					routes[path] = append(routes[path], value)
					break
				}
			}
		}
		return true
	})
	return routes
}

func registeredRuntimeRoutePrefixes() map[string]struct{} {
	prefixes := map[string]struct{}{}
	for _, event := range RuntimeEvents() {
		separator := strings.IndexByte(event, '-')
		if separator > 0 {
			prefixes[event[:separator+1]] = struct{}{}
		}
	}
	return prefixes
}

func joinedAuditPath(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Join" {
		return ""
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "filepath" {
		return ""
	}
	root, ok := call.Args[0].(*ast.Ident)
	if !ok || root.Name != "root" {
		return ""
	}
	literal, ok := call.Args[1].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}
	return value
}
