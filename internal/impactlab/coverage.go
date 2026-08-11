package impactlab

import (
	"fmt"
	"sort"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

const (
	maxActionCoverageClasses     = 2
	maxActionCoveragePhases      = 2
	maxActionCoverageDecisions   = 4
	maxActionCoverageProvenance  = 4
	maxActionCoverageOutcomes    = 7
	maxActionCoverageApprovals   = 4
	maxActionApprovalTransitions = 8
)

func emptyActionDimensions() ActionDimensions {
	return ActionDimensions{
		Classes: []CaseKind{}, Tools: []string{}, Phases: []action.Phase{},
		Decisions: []action.Decision{}, Provenance: []action.Provenance{},
		Outcomes:            []action.PhaseOutcome{},
		Approvals:           []action.ApprovalStatus{},
		ApprovalTransitions: []actionapproval.Status{},
	}
}

func actionDimensionsNonNil(value ActionDimensions) bool {
	return value.Classes != nil && value.Tools != nil && value.Phases != nil &&
		value.Decisions != nil && value.Provenance != nil && value.Outcomes != nil &&
		value.Approvals != nil && value.ApprovalTransitions != nil
}

func normalizeActionDimensions(input ActionDimensions) (ActionDimensions, error) {
	if !actionDimensionsNonNil(input) {
		return ActionDimensions{}, fmt.Errorf("action coverage collections must not be null")
	}
	if len(input.Classes) > maxActionCoverageClasses || len(input.Tools) > action.MaxTools ||
		len(input.Phases) > maxActionCoveragePhases || len(input.Decisions) > maxActionCoverageDecisions ||
		len(input.Provenance) > maxActionCoverageProvenance ||
		len(input.Outcomes) > maxActionCoverageOutcomes || len(input.Approvals) > maxActionCoverageApprovals ||
		len(input.ApprovalTransitions) > maxActionApprovalTransitions {
		return ActionDimensions{}, fmt.Errorf("action coverage exceeds a natural dimension bound")
	}
	out := ActionDimensions{
		Classes: append([]CaseKind(nil), input.Classes...), Tools: append([]string(nil), input.Tools...),
		Phases: append([]action.Phase(nil), input.Phases...), Decisions: append([]action.Decision(nil), input.Decisions...),
		Provenance: append([]action.Provenance(nil), input.Provenance...), Outcomes: append([]action.PhaseOutcome(nil), input.Outcomes...),
		Approvals:           append([]action.ApprovalStatus(nil), input.Approvals...),
		ApprovalTransitions: append([]actionapproval.Status(nil), input.ApprovalTransitions...),
	}
	for _, value := range out.Classes {
		if value != CaseActionPre && value != CaseActionPost {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid class %q", value)
		}
	}
	for _, value := range out.Tools {
		if !action.SafeLabel(value) || unsafeActionMetadata(value) {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid tool id %q", value)
		}
	}
	for _, value := range out.Phases {
		if value != action.PhasePreCall && value != action.PhasePostResult {
			return ActionDimensions{}, fmt.Errorf("action coverage contains unsupported phase %q", value)
		}
	}
	for _, value := range out.Decisions {
		if !value.Valid() {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid decision %q", value)
		}
	}
	for _, value := range out.Provenance {
		if !value.Valid() {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid provenance %q", value)
		}
	}
	for _, value := range out.Outcomes {
		if !value.Valid() {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid outcome %q", value)
		}
	}
	for _, value := range out.Approvals {
		if !value.Valid() {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid approval status %q", value)
		}
	}
	for _, value := range out.ApprovalTransitions {
		if !value.Valid() {
			return ActionDimensions{}, fmt.Errorf("action coverage contains invalid approval transition %q", value)
		}
	}
	sort.Slice(out.Classes, func(i, j int) bool { return out.Classes[i] < out.Classes[j] })
	sort.Strings(out.Tools)
	sort.Slice(out.Phases, func(i, j int) bool { return phaseIndex(out.Phases[i]) < phaseIndex(out.Phases[j]) })
	sort.Slice(out.Decisions, func(i, j int) bool { return decisionIndex(out.Decisions[i]) < decisionIndex(out.Decisions[j]) })
	sort.Slice(out.Provenance, func(i, j int) bool { return out.Provenance[i].Rank() < out.Provenance[j].Rank() })
	sort.Slice(out.Outcomes, func(i, j int) bool { return outcomeIndex(out.Outcomes[i]) < outcomeIndex(out.Outcomes[j]) })
	sort.Slice(out.Approvals, func(i, j int) bool { return approvalIndex(out.Approvals[i]) < approvalIndex(out.Approvals[j]) })
	sort.Slice(out.ApprovalTransitions, func(i, j int) bool {
		return approvalTransitionIndex(out.ApprovalTransitions[i]) < approvalTransitionIndex(out.ApprovalTransitions[j])
	})
	if duplicateActionDimension(out) {
		return ActionDimensions{}, fmt.Errorf("action coverage dimensions contain duplicates")
	}
	return out, nil
}

func duplicateActionDimension(value ActionDimensions) bool {
	return duplicateOrdered(value.Classes) || duplicateOrdered(value.Tools) ||
		duplicateOrdered(value.Phases) || duplicateOrdered(value.Decisions) ||
		duplicateOrdered(value.Provenance) || duplicateOrdered(value.Outcomes) ||
		duplicateOrdered(value.Approvals) || duplicateOrdered(value.ApprovalTransitions)
}

func duplicateOrdered[T comparable](values []T) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

func buildActionCoverage(cases []Case, required ActionDimensions, redactions int) ActionCoverage {
	observed := emptyActionDimensions()
	for _, replayCase := range cases {
		if replayCase.Action == nil {
			continue
		}
		observed.Classes = append(observed.Classes, replayCase.Kind)
		observed.Tools = append(observed.Tools, replayCase.Action.ToolID)
		observed.Phases = append(observed.Phases, replayCase.Action.Request.Phase)
		observed.Decisions = append(observed.Decisions, replayCase.Action.Expected.Decision)
		observed.Outcomes = append(observed.Outcomes, replayCase.Action.Expected.PhaseOutcome)
		if replayCase.Action.Expected.Approval != nil {
			observed.Approvals = append(observed.Approvals, replayCase.Action.Expected.Approval.Status)
			if replayCase.Action.Expected.Approval.Transition != "" {
				observed.ApprovalTransitions = append(
					observed.ApprovalTransitions, replayCase.Action.Expected.Approval.Transition,
				)
			}
		}
		for _, context := range replayCase.Action.Request.Context {
			observed.Provenance = append(observed.Provenance, context.Provenance)
		}
	}
	observed = canonicalActionDimensions(observed)
	required = canonicalActionDimensions(required)
	missing := subtractActionDimensions(required, observed)
	complete := actionDimensionsPopulated(required) && actionDimensionsEmpty(missing) && redactions == 0
	return ActionCoverage{Observed: observed, Required: required, Missing: missing, Complete: complete}
}

func canonicalActionDimensions(input ActionDimensions) ActionDimensions {
	input.Classes = uniqueSorted(input.Classes, func(left, right CaseKind) bool { return left < right })
	input.Tools = uniqueSorted(input.Tools, func(left, right string) bool { return left < right })
	input.Phases = uniqueSorted(input.Phases, func(left, right action.Phase) bool { return phaseIndex(left) < phaseIndex(right) })
	input.Decisions = uniqueSorted(input.Decisions, func(left, right action.Decision) bool { return decisionIndex(left) < decisionIndex(right) })
	input.Provenance = uniqueSorted(input.Provenance, func(left, right action.Provenance) bool { return left.Rank() < right.Rank() })
	input.Outcomes = uniqueSorted(input.Outcomes, func(left, right action.PhaseOutcome) bool { return outcomeIndex(left) < outcomeIndex(right) })
	input.Approvals = uniqueSorted(input.Approvals, func(left, right action.ApprovalStatus) bool { return approvalIndex(left) < approvalIndex(right) })
	input.ApprovalTransitions = uniqueSorted(input.ApprovalTransitions, func(left, right actionapproval.Status) bool {
		return approvalTransitionIndex(left) < approvalTransitionIndex(right)
	})
	return input
}

func uniqueSorted[T comparable](input []T, less func(T, T) bool) []T {
	out := append([]T(nil), input...)
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	kept := out[:0]
	for _, value := range out {
		if len(kept) == 0 || kept[len(kept)-1] != value {
			kept = append(kept, value)
		}
	}
	if kept == nil {
		return []T{}
	}
	return kept
}

func subtractActionDimensions(required, observed ActionDimensions) ActionDimensions {
	return ActionDimensions{
		Classes: difference(required.Classes, observed.Classes), Tools: difference(required.Tools, observed.Tools),
		Phases: difference(required.Phases, observed.Phases), Decisions: difference(required.Decisions, observed.Decisions),
		Provenance: difference(required.Provenance, observed.Provenance), Outcomes: difference(required.Outcomes, observed.Outcomes),
		Approvals:           difference(required.Approvals, observed.Approvals),
		ApprovalTransitions: difference(required.ApprovalTransitions, observed.ApprovalTransitions),
	}
}

func difference[T comparable](required, observed []T) []T {
	set := make(map[T]struct{}, len(observed))
	for _, value := range observed {
		set[value] = struct{}{}
	}
	out := make([]T, 0)
	for _, value := range required {
		if _, ok := set[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func mergeActionDimensions(left, right ActionDimensions) ActionDimensions {
	return canonicalActionDimensions(ActionDimensions{
		Classes:    append(append([]CaseKind{}, left.Classes...), right.Classes...),
		Tools:      append(append([]string{}, left.Tools...), right.Tools...),
		Phases:     append(append([]action.Phase{}, left.Phases...), right.Phases...),
		Decisions:  append(append([]action.Decision{}, left.Decisions...), right.Decisions...),
		Provenance: append(append([]action.Provenance{}, left.Provenance...), right.Provenance...),
		Outcomes:   append(append([]action.PhaseOutcome{}, left.Outcomes...), right.Outcomes...),
		Approvals:  append(append([]action.ApprovalStatus{}, left.Approvals...), right.Approvals...),
		ApprovalTransitions: append(
			append([]actionapproval.Status{}, left.ApprovalTransitions...), right.ApprovalTransitions...,
		),
	})
}

func actionDimensionsPopulated(value ActionDimensions) bool {
	return len(value.Classes) > 0 && len(value.Tools) > 0 && len(value.Phases) > 0 &&
		len(value.Decisions) > 0 && len(value.Provenance) > 0 && len(value.Outcomes) > 0 &&
		len(value.Approvals) > 0
}

func actionDimensionsEmpty(value ActionDimensions) bool {
	return len(value.Classes)+len(value.Tools)+len(value.Phases)+len(value.Decisions)+
		len(value.Provenance)+len(value.Outcomes)+len(value.Approvals)+len(value.ApprovalTransitions) == 0
}

func equalActionCoverage(left, right ActionCoverage) bool {
	return equalActionDimensions(left.Observed, right.Observed) &&
		equalActionDimensions(left.Required, right.Required) &&
		equalActionDimensions(left.Missing, right.Missing) && left.Complete == right.Complete
}

func equalActionDimensions(left, right ActionDimensions) bool {
	return equalComparable(left.Classes, right.Classes) && equalComparable(left.Tools, right.Tools) &&
		equalComparable(left.Phases, right.Phases) && equalComparable(left.Decisions, right.Decisions) &&
		equalComparable(left.Provenance, right.Provenance) && equalComparable(left.Outcomes, right.Outcomes) &&
		equalComparable(left.Approvals, right.Approvals) &&
		equalComparable(left.ApprovalTransitions, right.ApprovalTransitions)
}

func equalComparable[T comparable](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func phaseIndex(value action.Phase) int {
	for index, candidate := range action.AllPhases() {
		if value == candidate {
			return index
		}
	}
	return len(action.AllPhases())
}

func decisionIndex(value action.Decision) int {
	return value.Strength()
}

func outcomeIndex(value action.PhaseOutcome) int {
	values := []action.PhaseOutcome{
		action.OutcomeDispatchEligible, action.OutcomeDispatchBlocked,
		action.OutcomeDeliveryEligible, action.OutcomeWithheld,
		action.OutcomeProgressEligible, action.OutcomeSuppressed, action.OutcomeRecorded,
	}
	for index, candidate := range values {
		if value == candidate {
			return index
		}
	}
	return len(values)
}

func approvalIndex(value action.ApprovalStatus) int {
	values := []action.ApprovalStatus{
		action.ApprovalNone, action.ApprovalPending,
		action.ApprovalCurrentUnconsumed, action.ApprovalConsumed,
	}
	for index, candidate := range values {
		if value == candidate {
			return index
		}
	}
	return len(values)
}

func approvalTransitionIndex(value actionapproval.Status) int {
	values := []actionapproval.Status{
		actionapproval.StatusPending, actionapproval.StatusApproved,
		actionapproval.StatusRejected, actionapproval.StatusExpired,
		actionapproval.StatusCancelled, actionapproval.StatusUnavailable,
		actionapproval.StatusMalformed, actionapproval.StatusReplayed,
	}
	for index, candidate := range values {
		if value == candidate {
			return index
		}
	}
	return len(values)
}
