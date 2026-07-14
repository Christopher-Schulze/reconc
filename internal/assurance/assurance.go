// Package assurance evaluates bounded, native repository assurance gates.
// Source gates are changed-file scoped; authority gates inspect their complete
// configured surface. The package performs no network calls or subprocesses.
package assurance

import (
	"fmt"
	"strings"
	"time"

	"reconc.dev/reconc/internal/policy"
)

const (
	maxChangedPaths = 20_000
	maxScannedFiles = 4_096
	maxFileBytes    = 4 << 20
	maxTotalBytes   = 32 << 20
	maxWalkEntries  = 50_000
	maxFindings     = 50
)

// Inputs is the runtime evidence available to native gates.
type Inputs struct {
	ChangedPaths       []string
	SuccessfulCommands []string
	Now                time.Time
}

// Finding is one precise gate failure and its direct remediation.
type Finding struct {
	GateID      string
	Paths       []string
	Message     string
	Remediation string
}

// Evaluate runs configured gates in declaration order. It returns operational
// errors for unreadable or over-budget authority surfaces so callers fail
// closed instead of silently accepting incomplete evidence.
func Evaluate(repoRoot string, gates []policy.AssuranceGate, inputs Inputs) ([]Finding, error) {
	root, err := canonicalRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	if len(inputs.ChangedPaths) > maxChangedPaths {
		return nil, fmt.Errorf("assurance changed-path budget exceeded: %d > %d", len(inputs.ChangedPaths), maxChangedPaths)
	}
	if inputs.Now.IsZero() {
		inputs.Now = time.Now().UTC()
	}
	state := newEvaluationState()
	findings := []Finding{}
	for _, gate := range gates {
		applies, err := state.applies(root, gate.ApplicableIf)
		if err != nil {
			return nil, fmt.Errorf("assurance gate %s applicability: %w", gate.ID, err)
		}
		if !applies {
			continue
		}
		var gateFindings []Finding
		switch gate.Type {
		case policy.AssuranceRepositoryLayout:
			gateFindings, err = evaluateRepositoryLayout(root, gate, state)
		case policy.AssuranceGeneratedReference, policy.AssuranceLiveVerification:
			gateFindings = evaluateCommands(gate, inputs.SuccessfulCommands)
		case policy.AssuranceLanguageBoundary:
			gateFindings, err = evaluateLanguageBoundary(root, gate, inputs.ChangedPaths, state)
		case policy.AssuranceDependencyPins:
			gateFindings, err = evaluateDependencyPins(root, gate, inputs.ChangedPaths, state)
		case policy.AssuranceNetworkBoundary, policy.AssuranceProcessBoundary:
			gateFindings, err = evaluateGuardBoundary(root, gate, inputs.ChangedPaths, state)
		case policy.AssuranceSubstantiveProof:
			gateFindings, err = evaluateSubstantiveProof(root, gate, inputs, state)
		case policy.AssuranceGoConcurrency:
			gateFindings, err = evaluateGoConcurrencyBoundary(root, gate, inputs.ChangedPaths, state)
		default:
			err = fmt.Errorf("unsupported assurance kind %q", gate.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("assurance gate %s: %w", gate.ID, err)
		}
		findings = append(findings, gateFindings...)
	}
	return limitFindings(findings), nil
}

func normalizeCommand(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

func stringSetNormalized(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[normalizeCommand(value)] = true
	}
	return out
}

func limitFindings(findings []Finding) []Finding {
	if len(findings) <= maxFindings {
		return findings
	}
	omitted := len(findings) - maxFindings
	out := append([]Finding(nil), findings[:maxFindings]...)
	out = append(out, Finding{GateID: "assurance-budget", Message: fmt.Sprintf("%d additional findings omitted after the %d-finding output limit", omitted, maxFindings), Remediation: "Resolve the listed findings, then rerun to expose the remaining bounded set."})
	return out
}
