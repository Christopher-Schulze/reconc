package action

import (
	"fmt"
	"math"
	"sort"
)

// BudgetContract returns the exact declared gateway tool and every canonical
// budget selected for one normalized pre-call request.
func (p *CompiledPlan) BudgetContract(request Request) (Tool, []Budget, error) {
	if p == nil {
		return Tool{}, nil, fmt.Errorf("compiled action plan is unavailable")
	}
	toolIdentity := Tool{
		Transport: request.Transport, Platform: request.Platform,
		ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
		Tool: request.Tool,
	}
	index, ok := lookupToolIndex(p.toolByExact, toolIdentity)
	if !ok || index < 0 || index >= len(p.plan.Tools) {
		return Tool{}, []Budget{}, nil
	}
	tool := p.plan.Tools[index]
	tool.Effect.PathFields = cloneSlice(tool.Effect.PathFields)
	if tool.CostUnits != nil {
		value := *tool.CostUnits
		tool.CostUnits = &value
	}
	if request.Phase != PhasePreCall {
		return tool, []Budget{}, nil
	}
	budgets := make([]Budget, 0, len(p.budgets))
	for _, budget := range p.budgets {
		if selectorMatches(budget.Selector, request, tool.ID) {
			copy := budget
			cloneSelector(&copy.Selector, budget.Selector)
			budgets = append(budgets, copy)
		}
	}
	return tool, budgets, nil
}

func RequiredBudgetUsage(limits BudgetLimits, tool Tool, request Request) (BudgetUsage, error) {
	return expectedBudgetUsage(limits, tool, request)
}

func BudgetCapacityAvailable(
	limits BudgetLimits,
	consumed BudgetUsage,
	reserved BudgetUsage,
	required BudgetUsage,
	reservationApplied bool,
) bool {
	return budgetCapacityAvailable(limits, consumed, reserved, required, reservationApplied)
}

func (e *Evaluator) matchingBudgets(request Request, toolID string) []Budget {
	if e == nil || request.Phase != PhasePreCall || toolID == "" {
		return []Budget{}
	}
	budgets := make([]Budget, 0, len(e.plan.Budgets))
	for _, budget := range e.plan.Budgets {
		if selectorMatches(budget.Selector, request, toolID) {
			budgets = append(budgets, budget)
		}
	}
	return budgets
}

func (e *Evaluator) normalizeBudgetInput(input *EvaluationInput) *RequestError {
	tool, toolID := e.selectTool(input.Request)
	expected := e.matchingBudgets(input.Request, toolID)
	if len(expected) == 0 {
		if budgetSnapshotZero(input.Budget) {
			input.Budget = BudgetSnapshot{
				StateVersion: input.Request.StateVersion,
				Identity:     "absent", ReservationIdentity: "absent",
				Complete: true, Candidates: []BudgetCandidate{},
			}
			return nil
		}
		if !canonicalAbsentBudget(input.Budget, input.Request.StateVersion) {
			return &RequestError{Code: ReasonStateCorrupt, Message: "unexpected budget state exists for an unbudgeted action"}
		}
		return nil
	}
	if !input.Budget.Complete {
		return &RequestError{Code: ReasonStateUnavailable, Message: "budget state is incomplete"}
	}
	if input.Budget.StateVersion != input.Request.StateVersion ||
		!ValidKeyedIdentity(input.Budget.StateVersion) ||
		!ValidKeyedIdentity(input.Budget.Identity) ||
		!validOpaqueIdentity(input.Budget.ReservationIdentity) ||
		len(input.Budget.Candidates) != len(expected) {
		return &RequestError{Code: ReasonStateCorrupt, Message: "budget snapshot identity or candidate count is invalid"}
	}
	candidates := append([]BudgetCandidate(nil), input.Budget.Candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].BudgetID < candidates[j].BudgetID })
	anyUnavailable := false
	anyApplied := false
	snapshotKeyID, validSnapshotKey := KeyedIdentityKeyID(input.Budget.Identity)
	stateKeyID, validStateKey := KeyedIdentityKeyID(input.Budget.StateVersion)
	if !validSnapshotKey || !validStateKey || snapshotKeyID != stateKeyID {
		return &RequestError{Code: ReasonStateCorrupt, Message: "budget snapshot key generation is invalid"}
	}
	argumentBytes, argumentErr := canonicalArgumentBytesForBudgets(expected, input.Request)
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.BudgetID != expected[index].ID {
			return &RequestError{Code: ReasonStateCorrupt, Message: "budget snapshot does not match compiled declarations"}
		}
		if tool == nil {
			return &RequestError{Code: ReasonStateCorrupt, Message: "budget snapshot targets an undeclared tool"}
		}
		if err := validateBudgetCandidate(*candidate, expected[index], input, *tool, argumentBytes, argumentErr); err != nil {
			return &RequestError{Code: ReasonStateCorrupt, Message: err.Error()}
		}
		if candidate.Generation.KeyID != snapshotKeyID {
			return &RequestError{Code: ReasonStateCorrupt, Message: "budget candidate uses a different key generation"}
		}
		anyUnavailable = anyUnavailable || !candidate.Available
		anyApplied = anyApplied || candidate.ReservationApplied
	}
	if anyUnavailable {
		if anyApplied || input.Budget.ReservationIdentity != "absent" {
			return &RequestError{Code: ReasonStateCorrupt, Message: "an exhausted budget snapshot cannot contain a partial reservation"}
		}
	} else if !allBudgetReservationsApplied(candidates) ||
		!ValidKeyedIdentity(input.Budget.ReservationIdentity) {
		return &RequestError{Code: ReasonStateCorrupt, Message: "available budgets require one atomic keyed reservation"}
	}
	if input.Budget.ReservationIdentity != "absent" {
		reservationKeyID, valid := KeyedIdentityKeyID(input.Budget.ReservationIdentity)
		if !valid || reservationKeyID != snapshotKeyID {
			return &RequestError{Code: ReasonStateCorrupt, Message: "budget reservation uses a different key generation"}
		}
	}
	input.Budget.Candidates = candidates
	return nil
}

func validateBudgetCandidate(
	candidate BudgetCandidate,
	declaration Budget,
	input *EvaluationInput,
	tool Tool,
	argumentBytes *uint64,
	argumentErr error,
) error {
	if candidate.Reset != declaration.Reset || candidate.WindowSeconds != declaration.WindowSeconds ||
		candidate.Limits != declaration.Limits || !ValidKeyedIdentity(candidate.ScopeIdentity) ||
		!ValidKeyedIdentity(candidate.LineageIdentity) {
		return fmt.Errorf("budget %q contract identity is invalid", declaration.ID)
	}
	if err := validateBudgetScope(candidate.Scope, declaration, input, tool.ID); err != nil {
		return err
	}
	if candidate.Generation.PolicyDigest != input.Request.PolicyDigest ||
		candidate.Generation.ExecutableDigest != input.ExecutableDigest ||
		candidate.Generation.ToolContractDigest != input.Request.ToolContractDigest ||
		!lowerHexLength(candidate.Generation.KeyID, 32) ||
		!budgetCandidateUsesKey(candidate, candidate.Generation.KeyID) {
		return fmt.Errorf("budget %q governing generation is invalid", declaration.ID)
	}
	required, err := expectedBudgetUsageWithArgumentBytes(
		declaration.Limits, tool, input.Request, argumentBytes, argumentErr,
	)
	if err != nil || candidate.Required != required {
		return fmt.Errorf("budget %q reservation charge is invalid", declaration.ID)
	}
	available := budgetCapacityAvailable(
		candidate.Limits, candidate.Consumed, candidate.Reserved,
		candidate.Required, candidate.ReservationApplied,
	)
	if candidate.Available != available {
		return fmt.Errorf("budget %q availability does not match its counters", declaration.ID)
	}
	if available && candidate.Reason != "" || !available && candidate.Reason != ReasonBudgetExhausted {
		return fmt.Errorf("budget %q availability reason is invalid", declaration.ID)
	}
	return nil
}

func budgetCandidateUsesKey(candidate BudgetCandidate, keyID string) bool {
	identities := []string{
		candidate.ScopeIdentity, candidate.LineageIdentity,
		candidate.Scope.RepositoryIdentity, candidate.Scope.ServerIdentity,
	}
	for _, identity := range []string{
		candidate.Scope.RunIdentity, candidate.Scope.SessionIdentity, candidate.Scope.WindowIdentity,
	} {
		if identity != "absent" {
			identities = append(identities, identity)
		}
	}
	for _, identity := range identities {
		actual, valid := KeyedIdentityKeyID(identity)
		if !valid || actual != keyID {
			return false
		}
	}
	return true
}

func lowerHexLength(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validateBudgetScope(scope BudgetScope, declaration Budget, input *EvaluationInput, toolID string) error {
	credentials, err := safeStringList(scope.CredentialLabels, MaxCredentialLabels)
	if err != nil || !equalStrings(credentials, input.CredentialLabels) ||
		scope.RepositoryIdentity != input.Request.RepositoryIdentity ||
		scope.Principal != input.Principal || scope.ServerLabel != input.Request.ServerLabel ||
		scope.ServerIdentity != input.Request.ServerFingerprint || scope.ToolID != toolID {
		return fmt.Errorf("budget %q scope does not match trusted action context", declaration.ID)
	}
	if !ValidKeyedIdentity(scope.RepositoryIdentity) || !SafeLabel(scope.Principal) ||
		!SafeLabel(scope.ServerLabel) || !ValidKeyedIdentity(scope.ServerIdentity) {
		return fmt.Errorf("budget %q scope identity is unavailable", declaration.ID)
	}
	wantRun, wantSession, wantWindow := "absent", "absent", "absent"
	switch declaration.Reset {
	case BudgetResetOperatorRun:
		if !ValidKeyedIdentity(scope.RunIdentity) {
			return fmt.Errorf("budget %q requires an operator-bound run identity", declaration.ID)
		}
		wantRun = scope.RunIdentity
	case BudgetResetOperatorSession:
		if !ValidKeyedIdentity(scope.SessionIdentity) {
			return fmt.Errorf("budget %q requires an operator-bound session identity", declaration.ID)
		}
		wantSession = scope.SessionIdentity
	case BudgetResetFixedWindow:
		if !ValidKeyedIdentity(scope.WindowIdentity) || scope.WindowStartUnix < 0 ||
			scope.WindowStartUnix%int64(declaration.WindowSeconds) != 0 {
			return fmt.Errorf("budget %q fixed window identity is invalid", declaration.ID)
		}
		wantWindow = scope.WindowIdentity
	}
	if scope.RunIdentity != wantRun || scope.SessionIdentity != wantSession ||
		scope.WindowIdentity != wantWindow ||
		declaration.Reset != BudgetResetFixedWindow && scope.WindowStartUnix != 0 {
		return fmt.Errorf("budget %q contains an extraneous reset identity", declaration.ID)
	}
	return nil
}

func expectedBudgetUsage(limits BudgetLimits, tool Tool, request Request) (BudgetUsage, error) {
	return expectedBudgetUsageWithArgumentBytes(limits, tool, request, nil, nil)
}

func expectedBudgetUsageWithArgumentBytes(
	limits BudgetLimits,
	tool Tool,
	request Request,
	argumentBytes *uint64,
	argumentErr error,
) (BudgetUsage, error) {
	usage := BudgetUsage{}
	if limits.CallCount != 0 {
		usage.CallCount = 1
	}
	if limits.ArgumentBytes != 0 {
		if argumentErr != nil {
			return BudgetUsage{}, argumentErr
		}
		if argumentBytes != nil {
			usage.ArgumentBytes = *argumentBytes
		} else {
			if request.Arguments == nil {
				return BudgetUsage{}, fmt.Errorf("budgeted pre-call arguments are absent")
			}
			body, err := request.Arguments.MarshalJSON()
			if err != nil {
				return BudgetUsage{}, err
			}
			usage.ArgumentBytes = uint64(len(body))
		}
	}
	if limits.ResultBytes != 0 {
		usage.ResultBytes = tool.MaxResultBytes
	}
	if limits.CostUnits != 0 && tool.CostUnits != nil {
		usage.CostUnits = *tool.CostUnits
	}
	if limits.Concurrent != 0 {
		usage.Concurrent = 1
	}
	if limits.RateWindow != 0 {
		usage.RateWindow = 1
	}
	return usage, nil
}

func canonicalArgumentBytesForBudgets(
	budgets []Budget,
	request Request,
) (*uint64, error) {
	for _, budget := range budgets {
		if budget.Limits.ArgumentBytes == 0 {
			continue
		}
		if request.Arguments == nil {
			return nil, fmt.Errorf("budgeted pre-call arguments are absent")
		}
		body, err := request.Arguments.MarshalJSON()
		if err != nil {
			return nil, err
		}
		bytes := uint64(len(body))
		return &bytes, nil
	}
	return nil, nil
}

func budgetCapacityAvailable(
	limits BudgetLimits,
	consumed BudgetUsage,
	reserved BudgetUsage,
	required BudgetUsage,
	applied bool,
) bool {
	checks := []struct {
		limit    uint64
		consumed uint64
		reserved uint64
		required uint64
	}{
		{limits.CallCount, consumed.CallCount, reserved.CallCount, required.CallCount},
		{limits.DeniedCount, consumed.DeniedCount, reserved.DeniedCount, required.DeniedCount},
		{limits.ApprovalCount, consumed.ApprovalCount, reserved.ApprovalCount, required.ApprovalCount},
		{limits.ArgumentBytes, consumed.ArgumentBytes, reserved.ArgumentBytes, required.ArgumentBytes},
		{limits.ResultBytes, consumed.ResultBytes, reserved.ResultBytes, required.ResultBytes},
		{limits.CostUnits, consumed.CostUnits, reserved.CostUnits, required.CostUnits},
		{limits.Concurrent, consumed.Concurrent, reserved.Concurrent, required.Concurrent},
		{limits.RateWindow, consumed.RateWindow, reserved.RateWindow, required.RateWindow},
	}
	for _, check := range checks {
		if check.limit == 0 {
			if check.required != 0 {
				return false
			}
			continue
		}
		if applied && check.reserved < check.required {
			return false
		}
		total, overflow := saturatingBudgetAdd(check.consumed, check.reserved)
		if !applied {
			total, overflow = saturatingBudgetAdd(total, check.required)
		}
		if overflow || total > check.limit {
			return false
		}
	}
	return true
}

func saturatingBudgetAdd(left, right uint64) (uint64, bool) {
	if math.MaxUint64-left < right {
		return math.MaxUint64, true
	}
	return left + right, false
}

func allBudgetReservationsApplied(candidates []BudgetCandidate) bool {
	for _, candidate := range candidates {
		if !candidate.ReservationApplied || !candidate.Available {
			return false
		}
	}
	return true
}

func budgetSnapshotZero(snapshot BudgetSnapshot) bool {
	return snapshot.StateVersion == "" && snapshot.Identity == "" &&
		snapshot.ReservationIdentity == "" && !snapshot.Complete && snapshot.Candidates == nil
}

func canonicalAbsentBudget(snapshot BudgetSnapshot, stateVersion string) bool {
	return snapshot.StateVersion == stateVersion && snapshot.Identity == "absent" &&
		snapshot.ReservationIdentity == "absent" && snapshot.Complete &&
		len(snapshot.Candidates) == 0
}

func cloneBudgetSnapshot(snapshot BudgetSnapshot) BudgetSnapshot {
	out := snapshot
	out.Candidates = append([]BudgetCandidate(nil), snapshot.Candidates...)
	for index := range out.Candidates {
		out.Candidates[index].Scope.CredentialLabels = append(
			[]string(nil), snapshot.Candidates[index].Scope.CredentialLabels...,
		)
	}
	if out.Candidates == nil {
		out.Candidates = []BudgetCandidate{}
	}
	return out
}
