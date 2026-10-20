package assurance

import (
	"fmt"
	"time"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/stackdetect"
)

// Evaluation shares observations across native rules in one synchronous policy
// evaluation. It must never be retained across separate policy observations.
type Evaluation struct {
	root      string
	inputs    Inputs
	state     *evaluationState
	detection stackdetect.Result
	detected  bool
	modules   map[string]*evaluationState
}

// NewEvaluation binds one immutable input set. Discovery, module state and
// file facts are shared; each Evaluate call charges its own scan budget.
func NewEvaluation(repoRoot string, inputs Inputs) (*Evaluation, error) {
	return newEvaluation(repoRoot, inputs, maxAnalysisWorkers)
}

func newEvaluation(repoRoot string, inputs Inputs, workers int) (*Evaluation, error) {
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
	inputs.ChangedPaths = append([]string(nil), inputs.ChangedPaths...)
	inputs.SuccessfulCommands = append([]string(nil), inputs.SuccessfulCommands...)
	inputs.SuccessfulCommandEvidence = append([]CommandEvidence(nil), inputs.SuccessfulCommandEvidence...)
	epochs := make(map[string]uint64, len(inputs.WriteEpochs))
	for path, epoch := range inputs.WriteEpochs {
		epochs[path] = epoch
	}
	inputs.WriteEpochs = epochs
	state := newEvaluationState(inputs.ChangedPaths, workers)
	state.commandEvidence = commandEvidenceForInputs(inputs)
	state.writeEpochs = inputs.WriteEpochs
	return &Evaluation{root: root, inputs: inputs, state: state, modules: map[string]*evaluationState{}}, nil
}

// Evaluate runs this rule's gates while reusing root detection and file facts.
// The rule gets a fresh scan budget that is charged for every file it uses,
// so its outcome does not depend on which rules ran before it.
func (evaluation *Evaluation) Evaluate(gates []policy.AssuranceGate) ([]Finding, error) {
	evaluation.state.budget = newScanBudget()
	findings := []Finding{}
	for _, gate := range gates {
		gateFindings, err := evaluation.evaluate(gate)
		if err != nil {
			return nil, err
		}
		findings = append(findings, gateFindings...)
	}
	return limitFindings(findings), nil
}

func (evaluation *Evaluation) evaluate(gate policy.AssuranceGate) ([]Finding, error) {
	if len(gate.ApplicableIf) > 0 && !evaluation.detected {
		detection, err := detectModuleRoots(evaluation.root)
		if err != nil {
			return nil, fmt.Errorf("detect assurance module roots: %w", err)
		}
		evaluation.detection, evaluation.detected = detection, true
	}
	scopes, moduleAware, err := selectModuleScopes(evaluation.root, evaluation.detection, gate, evaluation.inputs.ChangedPaths)
	if err != nil {
		return nil, fmt.Errorf("assurance gate %s module scope: %w", gate.ID, err)
	}
	if moduleAware {
		return evaluation.evaluateScopes(gate, scopes)
	}
	applies, err := evaluation.state.applies(evaluation.root, gate.ApplicableIf)
	if err != nil {
		return nil, fmt.Errorf("assurance gate %s applicability: %w", gate.ID, err)
	}
	if !applies {
		return nil, nil
	}
	findings, err := evaluateGate(evaluation.root, gate, evaluation.inputs, evaluation.state, nil)
	if err != nil {
		return nil, fmt.Errorf("assurance gate %s: %w", gate.ID, err)
	}
	return findings, nil
}

func (evaluation *Evaluation) evaluateScopes(gate policy.AssuranceGate, scopes []moduleScope) ([]Finding, error) {
	findings := []Finding{}
	for _, scope := range scopes {
		state := evaluation.moduleState(scope)
		inputs := evaluation.inputs
		inputs.ChangedPaths = scope.changedPaths
		scopedGate := gate
		scopedGate.ApplicableIf = nil
		gateFindings, err := evaluateGate(scope.rootAbs, scopedGate, inputs, state, &scope)
		if err != nil {
			return nil, fmt.Errorf("assurance gate %s module %s: %w", gate.ID, scope.rootRel, err)
		}
		findings = append(findings, rebaseScopedFindings(gateFindings, scope)...)
	}
	return findings, nil
}

func (evaluation *Evaluation) moduleState(scope moduleScope) *evaluationState {
	key := stringSlicesKey([]string{scope.rootAbs}, scope.changedPaths)
	if state, present := evaluation.modules[key]; present {
		state.budget = evaluation.state.budget
		return state
	}
	state := newEvaluationState(scope.changedPaths, evaluation.state.analysisWorkers)
	state.budget = evaluation.state.budget
	state.commandEvidence = evaluation.state.commandEvidence
	state.writeEpochs = evaluation.inputs.WriteEpochs
	state.scopePrefix = scope.rootRel
	evaluation.modules[key] = state
	evaluation.state.scoped = append(evaluation.state.scoped, state)
	return state
}
