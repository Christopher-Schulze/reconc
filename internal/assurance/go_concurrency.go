package assurance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

func evaluateGoConcurrencyBoundary(root string, gate policy.AssuranceGate, changed []string, state *evaluationState) ([]Finding, error) {
	files, err := changedFiles(root, changed, gate.ScanPaths, gate.ExcludePaths, gate.Exemptions, state)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	for _, file := range files {
		if filepath.Ext(file.relative) != ".go" {
			continue
		}
		body, err := state.read(file.full)
		if err != nil {
			return nil, err
		}
		set := token.NewFileSet()
		tree, err := parser.ParseFile(set, file.relative, body, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse Go source %s: %w", file.relative, err)
		}
		owned := staticallyOwnedWaitGroupLaunches(tree)
		ast.Inspect(tree, func(node ast.Node) bool {
			statement, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}
			if owned[statement] {
				return true
			}
			position := set.Position(statement.Go)
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{file.relative},
				Message:     fmt.Sprintf("bare goroutine launch at %s:%d has no statically visible lifecycle owner", file.relative, position.Line),
				Remediation: "Route concurrency through an owned errgroup/supervisor boundary or add a narrowly documented path exemption.",
			})
			return true
		})
	}
	return findings, nil
}

func staticallyOwnedWaitGroupLaunches(tree *ast.File) map[*ast.GoStmt]bool {
	owned := make(map[*ast.GoStmt]bool)
	ast.Inspect(tree, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			markWaitGroupOwnedLaunches(function.Body, owned)
		case *ast.FuncLit:
			markWaitGroupOwnedLaunches(function.Body, owned)
		}
		return true
	})
	return owned
}

func markWaitGroupOwnedLaunches(body *ast.BlockStmt, owned map[*ast.GoStmt]bool) {
	waitGroups := make(map[string]bool)
	adds := make(map[string][]token.Pos)
	waits := make(map[string][]token.Pos)
	launches := make([]*ast.GoStmt, 0)
	ast.Inspect(body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.GoStmt:
			launches = append(launches, statement)
			return false
		case *ast.ValueSpec:
			collectWaitGroupValueSpec(statement, waitGroups)
		case *ast.AssignStmt:
			collectWaitGroupAssignment(statement, waitGroups)
		case *ast.CallExpr:
			receiver, method, ok := waitGroupMethod(statement)
			if !ok {
				return true
			}
			switch method {
			case "Add":
				adds[receiver] = append(adds[receiver], statement.Pos())
			case "Wait":
				waits[receiver] = append(waits[receiver], statement.Pos())
			}
		}
		return true
	})
	for _, launch := range launches {
		receiver, ok := deferredWaitGroupDoneReceiver(launch)
		if !ok {
			receiver, ok = waitGroupArgumentReceiver(launch, waitGroups)
		}
		if !ok {
			continue
		}
		// Local declarations prove the sync.WaitGroup type. A dotted
		// receiver (struct field like s.wg) cannot be type-checked
		// statically here, but the full Add-before / deferred-Done /
		// Wait-after signature on one receiver path is accepted as
		// ownership evidence.
		if !waitGroups[receiver] && !strings.Contains(receiver, ".") {
			continue
		}
		if !positionBefore(adds[receiver], launch.Pos()) || !positionAfter(waits[receiver], launch.Pos()) {
			continue
		}
		owned[launch] = true
	}
}

// waitGroupArgumentReceiver recognizes the delegation shape
// `go worker(&wg)` for a locally declared WaitGroup: the callee owns
// Done while the caller keeps Add and Wait.
func waitGroupArgumentReceiver(launch *ast.GoStmt, waitGroups map[string]bool) (string, bool) {
	if _, literal := launch.Call.Fun.(*ast.FuncLit); literal {
		return "", false
	}
	for _, argument := range launch.Call.Args {
		address, ok := argument.(*ast.UnaryExpr)
		if !ok || address.Op != token.AND {
			continue
		}
		name, ok := address.X.(*ast.Ident)
		if !ok || !waitGroups[name.Name] {
			continue
		}
		return name.Name, true
	}
	return "", false
}

func collectWaitGroupValueSpec(spec *ast.ValueSpec, waitGroups map[string]bool) {
	if isSyncWaitGroupType(spec.Type) {
		for _, name := range spec.Names {
			waitGroups[name.Name] = true
		}
		return
	}
	for index, value := range spec.Values {
		if index < len(spec.Names) && isSyncWaitGroupValue(value) {
			waitGroups[spec.Names[index].Name] = true
		}
	}
}

func collectWaitGroupAssignment(assignment *ast.AssignStmt, waitGroups map[string]bool) {
	if assignment.Tok != token.DEFINE {
		return
	}
	for index, value := range assignment.Rhs {
		if index >= len(assignment.Lhs) || !isSyncWaitGroupValue(value) {
			continue
		}
		name, ok := assignment.Lhs[index].(*ast.Ident)
		if ok {
			waitGroups[name.Name] = true
		}
	}
}

func isSyncWaitGroupValue(expression ast.Expr) bool {
	if address, ok := expression.(*ast.UnaryExpr); ok && address.Op == token.AND {
		expression = address.X
	}
	composite, ok := expression.(*ast.CompositeLit)
	return ok && isSyncWaitGroupType(composite.Type)
}

func isSyncWaitGroupType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "WaitGroup" {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == "sync"
}

func deferredWaitGroupDoneReceiver(launch *ast.GoStmt) (string, bool) {
	literal, ok := launch.Call.Fun.(*ast.FuncLit)
	if !ok {
		return "", false
	}
	receiver := ""
	ast.Inspect(literal.Body, func(node ast.Node) bool {
		if receiver != "" {
			return false
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		deferred, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		name, method, ok := waitGroupMethod(deferred.Call)
		if ok && method == "Done" {
			receiver = name
		}
		return true
	})
	return receiver, receiver != ""
}

func waitGroupMethod(call *ast.CallExpr) (string, string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	receiver, ok := receiverPath(selector.X)
	if !ok {
		return "", "", false
	}
	return receiver, selector.Sel.Name, true
}

// receiverPath renders an identifier or identifier-only selector chain
// (wg, s.wg, s.inner.wg) as a stable dotted path for receiver matching.
func receiverPath(expression ast.Expr) (string, bool) {
	switch v := expression.(type) {
	case *ast.Ident:
		return v.Name, true
	case *ast.SelectorExpr:
		prefix, ok := receiverPath(v.X)
		if !ok {
			return "", false
		}
		return prefix + "." + v.Sel.Name, true
	}
	return "", false
}

func positionBefore(positions []token.Pos, boundary token.Pos) bool {
	for _, position := range positions {
		if position < boundary {
			return true
		}
	}
	return false
}

func positionAfter(positions []token.Pos, boundary token.Pos) bool {
	for _, position := range positions {
		if position > boundary {
			return true
		}
	}
	return false
}
