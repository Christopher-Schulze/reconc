package actionevidence

import (
	"fmt"
	"sort"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

func EvaluateScenarios(
	repository string,
	corpora []impactlab.Corpus,
	current *runtime.CompiledPolicyEvaluator,
	actionRuntime runtime.CompiledActionRuntime,
) (ScenarioEvidence, error) {
	if len(corpora) == 0 {
		return ScenarioEvidence{CorpusIDs: []string{}, MissingDimensions: []string{}, ObservedPlatforms: []action.Platform{}, MissingPlatforms: []action.Platform{}}, nil
	}
	merged, err := impactlab.MergeCorpora(corpora)
	if err != nil {
		return ScenarioEvidence{}, fmt.Errorf("merge evidence scenarios: %w", err)
	}
	report, err := impactlab.CompareCurrent(repository, merged, current)
	if err != nil {
		return ScenarioEvidence{}, fmt.Errorf("verify evidence scenarios against current policy: %w", err)
	}
	if report.Candidate.SourceDigest != actionRuntime.SourceDigest ||
		report.Candidate.LockDigest != actionRuntime.LockDigest ||
		report.Candidate.ActionPlanIdentity != actionRuntime.Evaluator.PlanIdentity() {
		return ScenarioEvidence{}, fmt.Errorf("scenario evaluator and action runtime identities drifted")
	}
	return scenarioEvidence(corpora, merged, report, actionRuntime.Plan.Plan()), nil
}

func scenarioEvidence(
	corpora []impactlab.Corpus,
	merged impactlab.Corpus,
	report impactlab.Report,
	plan action.Plan,
) ScenarioEvidence {
	corpusIDs := make([]string, len(corpora))
	for index, corpus := range corpora {
		corpusIDs[index] = corpus.CorpusID
	}
	sort.Strings(corpusIDs)
	observed := observedScenarioPlatforms(merged)
	missing := missingPlanPlatforms(plan, observed)
	missingDimensions := missingActionDimensions(merged.Completeness.Action.Missing)
	for _, ruleID := range report.ActionCorpusUnmatchedRules {
		missingDimensions = append(missingDimensions, "rule:"+ruleID)
	}
	sort.Strings(missingDimensions)
	return ScenarioEvidence{
		Evaluated: true, CorpusIDs: corpusIDs, CaseCount: len(report.Cases),
		ActionCaseCount: report.Summary.ActionCaseCount, ResultsCurrent: true,
		Complete: merged.Completeness.Action.Complete && report.Summary.ActionCaseCount > 0 &&
			len(report.ActionCorpusUnmatchedRules) == 0 && len(missing) == 0,
		MissingDimensions: missingDimensions,
		ObservedPlatforms: observed, MissingPlatforms: missing,
	}
}

func observedScenarioPlatforms(corpus impactlab.Corpus) []action.Platform {
	seen := make(map[action.Platform]bool)
	for _, replayCase := range corpus.Cases {
		if replayCase.Action != nil && replayCase.Action.Request.Platform != "" {
			seen[replayCase.Action.Request.Platform] = true
		}
	}
	out := make([]action.Platform, 0, len(seen))
	for platform := range seen {
		out = append(out, platform)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func missingPlanPlatforms(plan action.Plan, observed []action.Platform) []action.Platform {
	seen := make(map[action.Platform]bool, len(observed))
	for _, platform := range observed {
		seen[platform] = true
	}
	missingSet := make(map[action.Platform]bool)
	for _, tool := range plan.Tools {
		if tool.Transport == action.TransportHostMCP && tool.Platform != "" && !seen[tool.Platform] {
			missingSet[tool.Platform] = true
		}
	}
	out := make([]action.Platform, 0, len(missingSet))
	for platform := range missingSet {
		out = append(out, platform)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func missingActionDimensions(value impactlab.ActionDimensions) []string {
	out := []string{}
	out = appendDimension(out, "class", value.Classes)
	out = appendDimension(out, "tool", value.Tools)
	out = appendDimension(out, "phase", value.Phases)
	out = appendDimension(out, "decision", value.Decisions)
	out = appendDimension(out, "provenance", value.Provenance)
	out = appendDimension(out, "outcome", value.Outcomes)
	out = appendDimension(out, "approval", value.Approvals)
	out = appendDimension(out, "approval-transition", value.ApprovalTransitions)
	sort.Strings(out)
	return out
}

func appendDimension[T ~string](target []string, name string, values []T) []string {
	for _, value := range values {
		target = append(target, name+":"+string(value))
	}
	return target
}
