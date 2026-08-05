package assurance

import (
	"fmt"

	"reconc.dev/reconc/internal/policy"
)

func evaluateGoFormat(root string, gate policy.AssuranceGate, state *evaluationState) ([]Finding, error) {
	files, err := changedGoFiles(root, gate.ScanPaths, gate.ExcludePaths, gate.Exemptions, state)
	if err != nil {
		return nil, err
	}
	state.prepareGoFacts(files, false, true)
	findings := []Finding{}
	for _, file := range files {
		formatted, err := state.goFormatMatches(file)
		if err != nil {
			return nil, fmt.Errorf("format Go source %s: %w", file.relative, err)
		}
		if formatted {
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
