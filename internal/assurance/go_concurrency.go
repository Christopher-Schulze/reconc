package assurance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"

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
		ast.Inspect(tree, func(node ast.Node) bool {
			statement, ok := node.(*ast.GoStmt)
			if !ok {
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
