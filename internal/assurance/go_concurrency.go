package assurance

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

func evaluateGoConcurrencyBoundary(root string, gate policy.AssuranceGate, state *evaluationState) ([]Finding, error) {
	files, err := changedGoFiles(root, gate.ScanPaths, gate.ExcludePaths, gate.Exemptions, state)
	if err != nil {
		return nil, err
	}
	state.prepareGoFacts(files, true, false)
	findings := []Finding{}
	for _, file := range files {
		set, tree, err := state.goSyntax(file)
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
	workers := indexNamedWorkers(tree)
	ast.Inspect(tree, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			markWaitGroupOwnedLaunches(function.Body, owned, workers)
		case *ast.FuncLit:
			markWaitGroupOwnedLaunches(function.Body, owned, workers)
		}
		return true
	})
	return owned
}

type namedWorkerIndex struct {
	functions        map[string][]*ast.FuncDecl
	methods          map[string][]*ast.FuncDecl
	importedPackages map[string]bool
	waitGroupAliases map[string]bool
}

func indexNamedWorkers(tree *ast.File) namedWorkerIndex {
	index := namedWorkerIndex{
		functions:        map[string][]*ast.FuncDecl{},
		methods:          map[string][]*ast.FuncDecl{},
		importedPackages: map[string]bool{},
		waitGroupAliases: map[string]bool{},
	}
	for _, declaration := range tree.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil {
			continue
		}
		if function.Recv == nil {
			index.functions[function.Name.Name] = append(index.functions[function.Name.Name], function)
		} else {
			index.methods[function.Name.Name] = append(index.methods[function.Name.Name], function)
		}
	}
	for _, imported := range tree.Imports {
		name := ""
		if imported.Name != nil {
			name = imported.Name.Name
		} else {
			path := strings.Trim(imported.Path.Value, `"`)
			if separator := strings.LastIndexByte(path, '/'); separator >= 0 {
				path = path[separator+1:]
			}
			name = path
		}
		if name != "" && name != "_" && name != "." {
			index.importedPackages[name] = true
		}
	}
	for _, declaration := range tree.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || !typeSpec.Assign.IsValid() || typeSpec.Name == nil {
				continue
			}
			if isSyncWaitGroupType(typeSpec.Type) {
				index.waitGroupAliases[typeSpec.Name.Name] = true
			}
		}
	}
	return index
}

func markWaitGroupOwnedLaunches(body *ast.BlockStmt, owned map[*ast.GoStmt]bool, workerIndexes ...namedWorkerIndex) {
	if body == nil {
		return
	}
	workers := namedWorkerIndex{}
	if len(workerIndexes) > 0 {
		workers = workerIndexes[0]
	}
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
			collectWaitGroupValueSpecWithAliases(statement, waitGroups, workers.waitGroupAliases)
		case *ast.AssignStmt:
			collectWaitGroupAssignmentWithAliases(statement, waitGroups, workers.waitGroupAliases)
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
			var argumentIndex int
			receiver, argumentIndex, ok = waitGroupArgument(launch, waitGroups)
			if ok {
				functionAliases := namedWorkerFunctionAliases(body, workers, launch.Pos())
				worker, resolved := resolveNamedWorker(launch.Call, workers, functionAliases)
				ok = resolved && namedWorkerCompletes(worker, argumentIndex, workers.waitGroupAliases)
			}
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

func namedWorkerFunctionAliases(body *ast.BlockStmt, workers namedWorkerIndex, boundary token.Pos) map[string]string {
	aliases := map[string]string{}
	ambiguous := map[string]bool{}
	for _, statement := range body.List {
		if boundary.IsValid() && statement.Pos() >= boundary {
			break
		}
		switch statement := statement.(type) {
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok || declaration.Tok != token.VAR {
				continue
			}
			for _, specification := range declaration.Specs {
				valueSpec, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range valueSpec.Names {
					if index < len(valueSpec.Values) {
						setNamedWorkerAlias(name.Name, valueSpec.Values[index], workers, aliases, ambiguous)
					}
				}
			}
		case *ast.AssignStmt:
			for index, left := range statement.Lhs {
				if index < len(statement.Rhs) {
					if name, ok := left.(*ast.Ident); ok {
						setNamedWorkerAlias(name.Name, statement.Rhs[index], workers, aliases, ambiguous)
					}
				}
			}
		}
	}
	for name := range ambiguous {
		delete(aliases, name)
	}
	return aliases
}

func setNamedWorkerAlias(name string, expression ast.Expr, workers namedWorkerIndex, aliases map[string]string, ambiguous map[string]bool) {
	target, ok := expression.(*ast.Ident)
	if !ok || len(workers.functions[target.Name]) != 1 {
		if _, exists := aliases[name]; exists {
			delete(aliases, name)
			ambiguous[name] = true
		}
		return
	}
	if previous, exists := aliases[name]; exists && previous != target.Name {
		delete(aliases, name)
		ambiguous[name] = true
		return
	}
	if !ambiguous[name] {
		aliases[name] = target.Name
	}
}

func resolveNamedWorker(call *ast.CallExpr, workers namedWorkerIndex, aliases map[string]string) (*ast.FuncDecl, bool) {
	if call == nil {
		return nil, false
	}
	switch function := call.Fun.(type) {
	case *ast.Ident:
		name := function.Name
		if target, ok := aliases[name]; ok {
			name = target
		}
		declarations := workers.functions[name]
		if len(declarations) == 1 {
			return declarations[0], true
		}
	case *ast.SelectorExpr:
		packageName, isIdentifier := function.X.(*ast.Ident)
		if !isIdentifier || workers.importedPackages[packageName.Name] {
			return nil, false
		}
		declarations := workers.methods[function.Sel.Name]
		if len(declarations) == 1 {
			return declarations[0], true
		}
	}
	return nil, false
}

func namedWorkerCompletes(function *ast.FuncDecl, argumentIndex int, aliases map[string]bool) bool {
	if function == nil || function.Body == nil {
		return false
	}
	parameter, isWaitGroup := namedWorkerParameter(function, argumentIndex, aliases)
	if !isWaitGroup {
		return false
	}
	receivers := map[string]bool{parameter: true}
	for _, statement := range function.Body.List {
		switch statement := statement.(type) {
		case *ast.DeclStmt:
			if !collectNamedWorkerAliasesFromDecl(statement, receivers) {
				return false
			}
		case *ast.AssignStmt:
			if !collectNamedWorkerAliasesFromAssignment(statement, receivers) {
				return false
			}
		case *ast.DeferStmt:
			name, method, ok := waitGroupMethod(statement.Call)
			if ok && method == "Done" && receivers[name] {
				return true
			}
		case *ast.EmptyStmt:
			continue
		default:
			return false
		}
	}
	return false
}

func namedWorkerParameter(function *ast.FuncDecl, argumentIndex int, aliases map[string]bool) (string, bool) {
	if function.Type == nil || function.Type.Params == nil || argumentIndex < 0 {
		return "", false
	}
	position := 0
	for _, field := range function.Type.Params.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		if argumentIndex < position+count {
			nameIndex := argumentIndex - position
			if len(field.Names) == 0 || nameIndex >= len(field.Names) {
				return "", false
			}
			name := field.Names[nameIndex].Name
			return name, isSyncWaitGroupPointer(field.Type, aliases)
		}
		position += count
	}
	return "", false
}

func isSyncWaitGroupPointer(expression ast.Expr, aliases map[string]bool) bool {
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	if isSyncWaitGroupType(star.X) {
		return true
	}
	name, ok := star.X.(*ast.Ident)
	return ok && aliases[name.Name]
}

func collectNamedWorkerAliasesFromDecl(statement *ast.DeclStmt, receivers map[string]bool) bool {
	declaration, ok := statement.Decl.(*ast.GenDecl)
	if !ok || declaration.Tok != token.VAR {
		return true
	}
	for _, specification := range declaration.Specs {
		valueSpec, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for index, name := range valueSpec.Names {
			if index >= len(valueSpec.Values) {
				continue
			}
			if !setNamedWorkerReceiver(name.Name, valueSpec.Values[index], receivers) {
				return false
			}
		}
	}
	return true
}

func collectNamedWorkerAliasesFromAssignment(statement *ast.AssignStmt, receivers map[string]bool) bool {
	for index, left := range statement.Lhs {
		name, ok := left.(*ast.Ident)
		if !ok || index >= len(statement.Rhs) {
			continue
		}
		if !setNamedWorkerReceiver(name.Name, statement.Rhs[index], receivers) {
			return false
		}
	}
	return true
}

func setNamedWorkerReceiver(name string, expression ast.Expr, receivers map[string]bool) bool {
	source, aliases := expression.(*ast.Ident)
	if receivers[name] && (!aliases || !receivers[source.Name]) {
		return false
	}
	if aliases && receivers[source.Name] {
		receivers[name] = true
	}
	return true
}

func waitGroupArgument(launch *ast.GoStmt, waitGroups map[string]bool) (string, int, bool) {
	if _, literal := launch.Call.Fun.(*ast.FuncLit); literal {
		return "", 0, false
	}
	for index, argument := range launch.Call.Args {
		address, ok := argument.(*ast.UnaryExpr)
		if !ok || address.Op != token.AND {
			continue
		}
		name, ok := address.X.(*ast.Ident)
		if !ok || !waitGroups[name.Name] {
			continue
		}
		return name.Name, index, true
	}
	return "", 0, false
}

func collectWaitGroupValueSpecWithAliases(spec *ast.ValueSpec, waitGroups map[string]bool, aliases map[string]bool) {
	if isSyncWaitGroupTypeWithAliases(spec.Type, aliases) {
		for _, name := range spec.Names {
			waitGroups[name.Name] = true
		}
		return
	}
	for index, value := range spec.Values {
		if index < len(spec.Names) && isSyncWaitGroupValueWithAliases(value, aliases) {
			waitGroups[spec.Names[index].Name] = true
		}
	}
}

func collectWaitGroupAssignment(assignment *ast.AssignStmt, waitGroups map[string]bool) {
	collectWaitGroupAssignmentWithAliases(assignment, waitGroups, nil)
}

func collectWaitGroupAssignmentWithAliases(assignment *ast.AssignStmt, waitGroups map[string]bool, aliases map[string]bool) {
	if assignment.Tok != token.DEFINE {
		return
	}
	for index, value := range assignment.Rhs {
		if index >= len(assignment.Lhs) || !isSyncWaitGroupValueWithAliases(value, aliases) {
			continue
		}
		name, ok := assignment.Lhs[index].(*ast.Ident)
		if ok {
			waitGroups[name.Name] = true
		}
	}
}

func isSyncWaitGroupValue(expression ast.Expr) bool {
	return isSyncWaitGroupValueWithAliases(expression, nil)
}

func isSyncWaitGroupValueWithAliases(expression ast.Expr, aliases map[string]bool) bool {
	if address, ok := expression.(*ast.UnaryExpr); ok && address.Op == token.AND {
		expression = address.X
	}
	composite, ok := expression.(*ast.CompositeLit)
	return ok && isSyncWaitGroupTypeWithAliases(composite.Type, aliases)
}

func isSyncWaitGroupType(expression ast.Expr) bool {
	return isSyncWaitGroupTypeWithAliases(expression, nil)
}

func isSyncWaitGroupTypeWithAliases(expression ast.Expr, aliases map[string]bool) bool {
	if name, ok := expression.(*ast.Ident); ok {
		return aliases[name.Name]
	}
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
