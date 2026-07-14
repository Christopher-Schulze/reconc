package assurance

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"

	"reconc.dev/reconc/internal/policy"
)

func evaluateGoFormat(root string, gate policy.AssuranceGate, changed []string, state *evaluationState) ([]Finding, error) {
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
		formatted, err := format.Source(body)
		if err != nil {
			return nil, fmt.Errorf("format Go source %s: %w", file.relative, err)
		}
		if bytes.Equal(body, formatted) {
			continue
		}
		findings = append(findings, Finding{
			GateID: gate.ID, Paths: []string{file.relative},
			Message:     "changed Go source is not canonically formatted: " + file.relative,
			Remediation: "Run gofmt on " + file.relative + ", then rerun the policy check.",
		})
	}
	return findings, nil
}
