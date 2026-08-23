package assurance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestWaitGroupAssignmentRecognition(t *testing.T) {
	parseAssignment := func(t *testing.T, source string) *ast.AssignStmt {
		t.Helper()
		tree, err := parser.ParseFile(token.NewFileSet(), "test.go", "package p\nfunc f() { "+source+" }", 0)
		if err != nil {
			t.Fatalf("parse assignment: %v", err)
		}
		return tree.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.AssignStmt)
	}

	for _, test := range []struct {
		name       string
		source     string
		wantGroups []string
	}{
		{name: "value", source: "wg := sync.WaitGroup{}", wantGroups: []string{"wg"}},
		{name: "pointer", source: "wg := &sync.WaitGroup{}", wantGroups: []string{"wg"}},
		{name: "parallel values", source: "first, second := 1, sync.WaitGroup{}", wantGroups: []string{"second"}},
		{name: "assignment is not declaration", source: "wg = sync.WaitGroup{}"},
		{name: "non identifier target", source: "holder.wg := sync.WaitGroup{}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			groups := map[string]bool{}
			collectWaitGroupAssignment(parseAssignment(t, test.source), groups)
			if len(groups) != len(test.wantGroups) {
				t.Fatalf("unexpected recognized groups: %v", groups)
			}
			for _, name := range test.wantGroups {
				if !groups[name] {
					t.Fatalf("expected %q to be recognized: %v", name, groups)
				}
			}
		})
	}
}

func TestSyncWaitGroupValueRecognition(t *testing.T) {
	for _, test := range []struct {
		source string
		want   bool
	}{
		{source: "sync.WaitGroup{}", want: true},
		{source: "&sync.WaitGroup{}", want: true},
		{source: "other.WaitGroup{}", want: false},
		{source: "sync.Mutex{}", want: false},
		{source: "new(sync.WaitGroup)", want: false},
	} {
		expression, err := parser.ParseExpr(test.source)
		if err != nil {
			t.Fatalf("parse %q: %v", test.source, err)
		}
		if got := isSyncWaitGroupValue(expression); got != test.want {
			t.Fatalf("isSyncWaitGroupValue(%q) = %v, want %v", test.source, got, test.want)
		}
	}
}

func TestEscapedQuoteRecognition(t *testing.T) {
	for _, test := range []struct {
		line  string
		index int
		want  bool
	}{
		{line: `a\"`, index: 2, want: true},
		{line: `a\\"`, index: 3, want: false},
		{line: `"`, index: 0, want: false},
	} {
		if got := escapedAt([]byte(test.line), test.index); got != test.want {
			t.Fatalf("escapedAt(%q, %d) = %v, want %v", test.line, test.index, got, test.want)
		}
	}
}

func TestGateAppliesContracts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "service.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("package internal\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, test := range []struct {
		name     string
		patterns []string
		want     bool
		wantErr  bool
	}{
		{name: "no patterns applies globally", want: true},
		{name: "matching file", patterns: []string{"internal/**/*.go"}, want: true},
		{name: "no match", patterns: []string{"docs/**/*.md"}, want: false},
		{name: "invalid pattern", patterns: []string{"["}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := gateApplies(root, test.patterns)
			if (err != nil) != test.wantErr {
				t.Fatalf("gateApplies error = %v, wantErr %v", err, test.wantErr)
			}
			if !test.wantErr && got != test.want {
				t.Fatalf("gateApplies = %v, want %v", got, test.want)
			}
		})
	}
}
