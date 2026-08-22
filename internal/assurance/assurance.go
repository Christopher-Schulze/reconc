// Package assurance evaluates bounded, native repository assurance gates.
// Source gates are changed-file scoped; authority gates inspect their complete
// configured surface. The package performs no network calls or subprocesses.
package assurance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	findings, _, err := evaluateWithStats(repoRoot, gates, inputs)
	return findings, err
}

// EvaluateWithInputIdentity evaluates the same native gates as Evaluate and
// returns a deterministic identity of every filesystem fact and derived
// directory observation that influenced the result. Completion callers use
// it before and after the policy evaluation to reject concurrent assurance
// input drift without executing scripts or network operations.
func EvaluateWithInputIdentity(repoRoot string, gates []policy.AssuranceGate, inputs Inputs) ([]Finding, string, error) {
	findings, state, err := evaluateWithState(repoRoot, gates, inputs, maxAnalysisWorkers)
	if err != nil {
		return nil, "", err
	}
	identity, err := state.inputIdentity(findings)
	if err != nil {
		return nil, "", fmt.Errorf("encode assurance input identity: %w", err)
	}
	return findings, identity, nil
}

func evaluateWithStats(repoRoot string, gates []policy.AssuranceGate, inputs Inputs) ([]Finding, analysisStats, error) {
	return evaluateWithWorkerLimit(repoRoot, gates, inputs, maxAnalysisWorkers)
}

func evaluateWithWorkerLimit(repoRoot string, gates []policy.AssuranceGate, inputs Inputs, workerLimit int) ([]Finding, analysisStats, error) {
	findings, state, err := evaluateWithState(repoRoot, gates, inputs, workerLimit)
	return findings, state.analysisStats(), err
}

func evaluateWithState(repoRoot string, gates []policy.AssuranceGate, inputs Inputs, workerLimit int) ([]Finding, *evaluationState, error) {
	root, err := canonicalRoot(repoRoot)
	if err != nil {
		return nil, newEvaluationState(nil, workerLimit), err
	}
	if len(inputs.ChangedPaths) > maxChangedPaths {
		return nil, newEvaluationState(nil, workerLimit), fmt.Errorf("assurance changed-path budget exceeded: %d > %d", len(inputs.ChangedPaths), maxChangedPaths)
	}
	if inputs.Now.IsZero() {
		inputs.Now = time.Now().UTC()
	}
	state := newEvaluationState(inputs.ChangedPaths, workerLimit)
	findings := []Finding{}
	for _, gate := range gates {
		applies, err := state.applies(root, gate.ApplicableIf)
		if err != nil {
			return nil, state, fmt.Errorf("assurance gate %s applicability: %w", gate.ID, err)
		}
		if !applies {
			continue
		}
		var gateFindings []Finding
		switch gate.Type {
		case policy.AssuranceRepositoryLayout:
			gateFindings, err = evaluateRepositoryLayout(root, gate, state)
		case policy.AssuranceGeneratedReference, policy.AssuranceLiveVerification:
			gateFindings, err = evaluateCommands(gate, inputs.SuccessfulCommands)
		case policy.AssuranceLanguageBoundary:
			gateFindings, err = evaluateLanguageBoundary(root, gate, state)
		case policy.AssuranceDependencyPins:
			gateFindings, err = evaluateDependencyPins(root, gate, state)
		case policy.AssurancePackageScripts:
			gateFindings, err = evaluatePackageScripts(root, gate, inputs.SuccessfulCommands, state)
		case policy.AssuranceNetworkBoundary, policy.AssuranceProcessBoundary:
			gateFindings, err = evaluateGuardBoundary(root, gate, state)
		case policy.AssuranceSubstantiveProof:
			gateFindings, err = evaluateSubstantiveProof(root, gate, inputs, state)
		case policy.AssuranceGoConcurrency:
			gateFindings, err = evaluateGoConcurrencyBoundary(root, gate, state)
		case policy.AssuranceGoFormat:
			gateFindings, err = evaluateGoFormat(root, gate, state)
		case policy.AssuranceSourceHygiene:
			gateFindings, err = evaluateSourceHygiene(root, gate, state)
		default:
			err = fmt.Errorf("unsupported assurance kind %q", gate.Type)
		}
		if err != nil {
			return nil, state, fmt.Errorf("assurance gate %s: %w", gate.ID, err)
		}
		findings = append(findings, gateFindings...)
	}
	return limitFindings(findings), state, nil
}

type assuranceIdentityPath struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Mode   uint32 `json:"mode,omitempty"`
}

type assuranceIdentityFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type assuranceIdentityManifests struct {
	Selector string   `json:"selector"`
	Paths    []string `json:"paths"`
}

type assuranceIdentityChangedPath struct {
	Path      string `json:"path"`
	Extension string `json:"extension"`
}

type assuranceIdentityInput struct {
	ChangedPaths     []assuranceIdentityChangedPath `json:"changed_paths"`
	Paths            []assuranceIdentityPath        `json:"paths"`
	Files            []assuranceIdentityFile        `json:"files"`
	Applicability    map[string]bool                `json:"applicability"`
	PackageManifests []assuranceIdentityManifests   `json:"package_manifests"`
	ManifestMarkers  map[string]bool                `json:"manifest_markers"`
	Observations     map[string]string              `json:"observations"`
	Findings         []Finding                      `json:"findings"`
}

func (state *evaluationState) inputIdentity(findings []Finding) (string, error) {
	changedPaths := make([]assuranceIdentityChangedPath, len(state.changedPaths))
	for index, changed := range state.changedPaths {
		changedPaths[index] = assuranceIdentityChangedPath{Path: changed.relative, Extension: changed.extension}
	}
	paths := make([]assuranceIdentityPath, 0, len(state.paths))
	for path, resolved := range state.paths {
		paths = append(paths, assuranceIdentityPath{Path: path, Exists: resolved.exists, Mode: uint32(resolved.mode)})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })

	files := make([]assuranceIdentityFile, 0, len(state.facts))
	for path, fact := range state.facts {
		if !fact.bodyLoaded || fact.bodyErr != nil {
			continue
		}
		sum := sha256.Sum256(fact.bodyBytes)
		files = append(files, assuranceIdentityFile{Path: path, Hash: hex.EncodeToString(sum[:])})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	manifests := make([]assuranceIdentityManifests, 0, len(state.packageManifests))
	for selector, matched := range state.packageManifests {
		paths := make([]string, len(matched))
		for index, file := range matched {
			paths[index] = file.relative
		}
		manifests = append(manifests, assuranceIdentityManifests{Selector: selector, Paths: paths})
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Selector < manifests[j].Selector })

	body, err := json.Marshal(assuranceIdentityInput{
		ChangedPaths: changedPaths,
		Paths:        paths, Files: files,
		Applicability: cloneBoolMap(state.applicability), PackageManifests: manifests,
		ManifestMarkers: cloneBoolMap(state.manifestMarkers), Observations: cloneStringMap(state.observations),
		Findings: append([]Finding(nil), findings...),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	out := make(map[string]bool, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
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
