package actionstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"reconc.dev/reconc/internal/action"
)

func (s *Store) CurrentStateVersion(ctx context.Context) (version string, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		if err := s.resampleRepositoryIdentity(); err != nil {
			return err
		}
		state, _, err := s.loadState()
		if err != nil {
			return err
		}
		version = state.Digest
		return nil
	})
	return version, resultErr
}

func (s *Store) Reserve(ctx context.Context, request ReserveRequest) (result ReserveResult, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		var err error
		result, err = s.reserveLocked(request)
		return err
	})
	return result, resultErr
}

func (s *Store) reserveLocked(input ReserveRequest) (ReserveResult, error) {
	if err := s.validateReserveRequest(input); err != nil {
		return ReserveResult{}, err
	}
	state, persisted, err := s.loadState()
	if err != nil {
		return ReserveResult{}, err
	}
	if input.Request.StateVersion != state.Digest {
		return ReserveResult{}, stateError(
			action.ReasonStateUnavailable,
			"action state changed before reservation",
			ErrStateVersionChanged,
		)
	}
	if err := rejectTerminalOrUnsafeRetry(state, input.Request.CallID, s.ownerID); err != nil {
		return ReserveResult{}, err
	}
	tool, declarations, err := input.Plan.BudgetContract(input.Request)
	if err != nil {
		return ReserveResult{}, stateError(action.ReasonStateCorrupt, "resolve compiled budget contract", err)
	}
	if len(declarations) == 0 {
		return ReserveResult{Snapshot: absentBudgetSnapshot(state.Digest)}, nil
	}
	clock, err := s.trustedNow(state)
	if err != nil {
		return ReserveResult{}, err
	}
	if existing := reservationForCall(state.Reservations, input.Request.CallID); existing != nil {
		return s.retryReservationSnapshot(state, persisted, *existing, declarations, tool, input, clock)
	}
	next := cloneState(state)
	generation := action.BudgetGeneration{
		PolicyDigest: input.Request.PolicyDigest, ExecutableDigest: input.Server.ExecutableDigest,
		ToolContractDigest: input.Request.ToolContractDigest, KeyID: s.key.ID(),
	}
	candidates, contractChanged, err := s.prepareCandidates(&next, declarations, tool, input, generation, clock.Time)
	if err != nil {
		return ReserveResult{}, err
	}
	if candidatesExhausted(candidates) {
		if contractChanged {
			s.applyClock(&next, clock)
			if err := s.writeState(state, persisted, &next); err != nil {
				return ReserveResult{}, err
			}
			return ReserveResult{Snapshot: s.budgetSnapshot(next.Digest, "absent", candidates)}, nil
		}
		return ReserveResult{Snapshot: s.budgetSnapshot(state.Digest, "absent", candidates)}, nil
	}
	if len(next.Reservations) >= MaxReservations {
		return ReserveResult{}, stateError(action.ReasonStateUnavailable, "action reservation capacity is exhausted", nil)
	}
	reservation, err := s.newReservation(input, candidates, clock.Time)
	if err != nil {
		return ReserveResult{}, err
	}
	next.Reservations = append(next.Reservations, reservation)
	sort.Slice(next.Reservations, func(i, j int) bool { return next.Reservations[i].Identity < next.Reservations[j].Identity })
	s.applyClock(&next, clock)
	if err := s.writeState(state, persisted, &next); err != nil {
		return ReserveResult{}, err
	}
	candidates, err = s.candidatesFromState(next, declarations, tool, input, generation, clock.Time)
	if err != nil {
		return ReserveResult{}, err
	}
	copy := cloneReservation(reservation)
	return ReserveResult{
		Snapshot:    s.budgetSnapshot(next.Digest, reservation.Identity, candidates),
		Reservation: &copy,
	}, nil
}

func (s *Store) validateReserveRequest(input ReserveRequest) error {
	if input.Plan == nil {
		return stateError(action.ReasonPolicyMissing, "compiled action plan is unavailable", nil)
	}
	if _, err := action.CanonicalRequest(input.Request); err != nil {
		return stateError(action.ReasonInvalidRequest, "budget reservation request is invalid", err)
	}
	if err := s.resampleRepositoryIdentity(); err != nil {
		return err
	}
	if input.Authority.Mode != input.Request.AuthorityMode {
		return stateError(action.ReasonAuthorityUnavailable, "policy authority does not match the canonical request", nil)
	}
	if err := input.Authority.VerifyLockDigest(input.Request.LockDigest); err != nil {
		return stateError(action.ReasonLockMismatch, "verify fresh policy authority", err)
	}
	if input.Request.RepositoryIdentity != s.repositoryIdentity ||
		!identityUsesKey(input.Request.RepositoryIdentity, s.key.ID()) ||
		input.Request.ServerFingerprint != input.Server.ServerIdentity {
		return stateError(action.ReasonIdentityUnavailable, "budget reservation identities are unavailable", nil)
	}
	if err := input.Server.Validate(s.key); err != nil {
		return stateError(action.ReasonIdentityUnavailable, "fresh downstream server observation is invalid", err)
	}
	if err := s.validateBoundContext(input.Context, input.Request.ServerLabel); err != nil {
		return err
	}
	return nil
}

func (s *Store) validateBoundContext(context BoundContext, serverLabel string) error {
	if context.Provenance != action.ProvenanceOperatorBound || !action.SafeLabel(context.Principal) ||
		context.Role != "" && !action.SafeLabel(context.Role) ||
		context.Environment != "" && !action.SafeLabel(context.Environment) ||
		context.ServerLabel != serverLabel || !action.SafeLabel(context.ServerLabel) ||
		!identityUsesKey(context.ContextIdentity, s.key.ID()) || context.Credentials == nil {
		return stateError(action.ReasonIdentityUnavailable, "operator-bound action context is invalid", nil)
	}
	for index, credential := range context.Credentials {
		if !action.SafeLabel(credential.Label) ||
			credential.Identity != "" && !identityUsesKey(credential.Identity, s.key.ID()) ||
			index > 0 && context.Credentials[index-1].Label >= credential.Label {
			return stateError(action.ReasonIdentityUnavailable, "operator credential bindings are invalid", nil)
		}
	}
	for _, identity := range []string{context.RunIdentity, context.SessionIdentity} {
		if identity != "" && !identityUsesKey(identity, s.key.ID()) {
			return stateError(action.ReasonIdentityUnavailable, "operator run or session identity is invalid", nil)
		}
	}
	wantIdentity := contextIdentity(
		s.key, context.Principal, context.Role, context.Environment, context.ServerLabel,
		context.RunIdentity, context.SessionIdentity, context.Credentials,
	)
	if !constantIdentityEqual(context.ContextIdentity, wantIdentity) {
		return stateError(action.ReasonIdentityUnavailable, "operator context identity does not match its exact values", nil)
	}
	return nil
}

func (s *Store) prepareCandidates(
	state *State,
	declarations []action.Budget,
	tool action.Tool,
	input ReserveRequest,
	generation action.BudgetGeneration,
	now time.Time,
) ([]action.BudgetCandidate, bool, error) {
	candidates := make([]action.BudgetCandidate, 0, len(declarations))
	contractChanged, err := pruneExpiredFixedWindowRecords(state, now)
	if err != nil {
		return nil, false, err
	}
	reservedByLineage, err := reservedUsageByLineage(*state)
	if err != nil {
		return nil, false, err
	}
	for _, declaration := range declarations {
		scope, lineage, scopeIdentity, err := s.budgetScope(declaration, tool.ID, input, now)
		if err != nil {
			return nil, false, err
		}
		record, changed, err := upsertBudgetRecord(state, declaration, scope, lineage, scopeIdentity, generation)
		if err != nil {
			return nil, false, err
		}
		contractChanged = contractChanged || changed
		required, err := action.RequiredBudgetUsage(declaration.Limits, tool, input.Request)
		if err != nil {
			return nil, false, stateError(action.ReasonStateCorrupt, "derive exact budget reservation charge", err)
		}
		reserved := reservedByLineage[lineage]
		available := action.BudgetCapacityAvailable(declaration.Limits, record.Consumed, reserved, required, false)
		reason := action.ReasonCode("")
		if !available {
			reason = action.ReasonBudgetExhausted
		}
		candidates = append(candidates, candidateFromRecord(*record, reserved, required, false, available, reason))
	}
	return candidates, contractChanged, nil
}

func pruneExpiredFixedWindowRecords(state *State, now time.Time) (bool, error) {
	if state == nil || len(state.Budgets) < 2 {
		return false, nil
	}
	active := make(map[string]bool, len(state.Reservations))
	for _, reservation := range state.Reservations {
		for _, charge := range reservation.Charges {
			active[charge.LineageIdentity] = true
		}
	}
	type group struct {
		keep    int
		indices []int
	}
	groups := make(map[string]*group)
	for index, record := range state.Budgets {
		if record.Reset != action.BudgetResetFixedWindow || active[record.LineageIdentity] ||
			!fixedWindowExpired(record, now) {
			continue
		}
		key := fixedWindowPruneKey(record)
		entry := groups[key]
		if entry == nil {
			groups[key] = &group{keep: index, indices: []int{index}}
			continue
		}
		entry.indices = append(entry.indices, index)
		if state.Budgets[entry.keep].Scope.WindowStartUnix < record.Scope.WindowStartUnix {
			entry.keep = index
		}
	}
	remove := make(map[int]bool)
	for _, entry := range groups {
		if len(entry.indices) < 2 {
			continue
		}
		history, err := mergedGenerationHistory(state.Budgets, entry.indices, state.Budgets[entry.keep].Generation)
		if err != nil {
			return false, err
		}
		state.Budgets[entry.keep].GenerationHistory = history
		for _, index := range entry.indices {
			if index != entry.keep {
				remove[index] = true
			}
		}
	}
	if len(remove) == 0 {
		return false, nil
	}
	kept := make([]BudgetRecord, 0, len(state.Budgets)-len(remove))
	for index, record := range state.Budgets {
		if !remove[index] {
			kept = append(kept, record)
		}
	}
	state.Budgets = kept
	return true, nil
}

func fixedWindowExpired(record BudgetRecord, now time.Time) bool {
	seconds := int64(record.WindowSeconds)
	return seconds > 0 && now.Unix()/seconds > record.Scope.WindowStartUnix/seconds
}

func fixedWindowPruneKey(record BudgetRecord) string {
	key := record.BudgetID + "\x00" + record.Scope.RepositoryIdentity + "\x00" +
		record.Scope.Principal + "\x00" + record.Scope.ServerLabel + "\x00" + record.Scope.ToolID + "\x00" +
		fmt.Sprintf("%d", record.WindowSeconds)
	for _, credential := range record.Scope.CredentialLabels {
		key += "\x00" + credential
	}
	return key
}

func mergedGenerationHistory(
	records []BudgetRecord,
	indices []int,
	active action.BudgetGeneration,
) ([]action.BudgetGeneration, error) {
	seen := make(map[action.BudgetGeneration]bool)
	history := make([]action.BudgetGeneration, 0, len(indices)+1)
	for _, index := range indices {
		for _, generation := range records[index].GenerationHistory {
			if generation == active || seen[generation] {
				continue
			}
			seen[generation] = true
			history = append(history, generation)
			if len(history) >= MaxGenerationHistory {
				return nil, stateError(action.ReasonStateUnavailable, "fixed-window generation history is exhausted", nil)
			}
		}
	}
	return append(history, active), nil
}

func (s *Store) budgetScope(
	declaration action.Budget,
	toolID string,
	input ReserveRequest,
	now time.Time,
) (action.BudgetScope, string, string, error) {
	runIdentity, sessionIdentity, windowIdentity := "absent", "absent", "absent"
	windowStart := int64(0)
	switch declaration.Reset {
	case action.BudgetResetOperatorRun:
		if input.Context.RunIdentity == "" {
			return action.BudgetScope{}, "", "", stateError(action.ReasonIdentityUnavailable, "run-reset budget requires an operator run identity", nil)
		}
		runIdentity = input.Context.RunIdentity
	case action.BudgetResetOperatorSession:
		if input.Context.SessionIdentity == "" {
			return action.BudgetScope{}, "", "", stateError(action.ReasonIdentityUnavailable, "session-reset budget requires an operator session identity", nil)
		}
		sessionIdentity = input.Context.SessionIdentity
	case action.BudgetResetFixedWindow:
		windowStart = now.Unix() / int64(declaration.WindowSeconds) * int64(declaration.WindowSeconds)
		windowIdentity = s.key.Identity(
			DomainBudget, []byte("window"), []byte(declaration.ID), []byte(fmt.Sprintf("%d", windowStart)),
		)
	}
	credentials := make([]string, len(input.Context.Credentials))
	for index, credential := range input.Context.Credentials {
		credentials[index] = credential.Label
	}
	scope := action.BudgetScope{
		RepositoryIdentity: input.Request.RepositoryIdentity,
		Principal:          input.Context.Principal, CredentialLabels: credentials,
		ServerLabel: input.Context.ServerLabel, ServerIdentity: input.Request.ServerFingerprint,
		ToolID: toolID, RunIdentity: runIdentity, SessionIdentity: sessionIdentity,
		WindowIdentity: windowIdentity, WindowStartUnix: windowStart,
	}
	lineageParts := [][]byte{
		[]byte("lineage"), []byte(s.projectKey), []byte(declaration.ID),
		[]byte(scope.Principal), []byte(scope.ServerLabel), []byte(scope.ToolID),
		[]byte(scope.RunIdentity), []byte(scope.SessionIdentity), []byte(scope.WindowIdentity),
	}
	for _, credential := range credentials {
		lineageParts = append(lineageParts, []byte(credential))
	}
	lineage := s.key.Identity(DomainBudget, lineageParts...)
	scopeBody, err := json.Marshal(scope)
	if err != nil {
		return action.BudgetScope{}, "", "", stateError(action.ReasonStateCorrupt, "encode budget scope", err)
	}
	scopeIdentity := s.key.Identity(DomainBudget, []byte("scope"), []byte(lineage), scopeBody)
	return scope, lineage, scopeIdentity, nil
}

func upsertBudgetRecord(
	state *State,
	declaration action.Budget,
	scope action.BudgetScope,
	lineage, scopeIdentity string,
	generation action.BudgetGeneration,
) (*BudgetRecord, bool, error) {
	index := sort.Search(len(state.Budgets), func(index int) bool {
		return state.Budgets[index].LineageIdentity >= lineage
	})
	if index == len(state.Budgets) || state.Budgets[index].LineageIdentity != lineage {
		if len(state.Budgets) >= MaxBudgetRecords {
			return nil, false, stateError(action.ReasonStateUnavailable, "budget record capacity is exhausted", nil)
		}
		if activeReservationBlocksResetChange(*state, declaration, scope) {
			return nil, false, stateError(
				action.ReasonReservationIndeterminate,
				"budget reset contract cannot change while a prior compatible reservation is active",
				nil,
			)
		}
		consumed, history, err := carryForwardChangedReset(state.Budgets, declaration, scope, generation)
		if err != nil {
			return nil, false, err
		}
		record := BudgetRecord{
			BudgetID: declaration.ID, ScopeIdentity: scopeIdentity, LineageIdentity: lineage,
			Scope: scope, Reset: declaration.Reset, WindowSeconds: declaration.WindowSeconds,
			Limits: declaration.Limits, Consumed: consumed, Generation: generation,
			GenerationHistory: history,
		}
		state.Budgets = append(state.Budgets, BudgetRecord{})
		copy(state.Budgets[index+1:], state.Budgets[index:])
		state.Budgets[index] = record
		return &state.Budgets[index], true, nil
	}
	record := &state.Budgets[index]
	if record.BudgetID != declaration.ID {
		return nil, false, stateError(action.ReasonStateCorrupt, "budget lineage collides with a different contract", nil)
	}
	resetChanged := record.Reset != declaration.Reset || record.WindowSeconds != declaration.WindowSeconds
	limitsChanged := record.Limits != declaration.Limits
	if (resetChanged || limitsChanged) && record.Generation == generation {
		return nil, false, stateError(action.ReasonStateCorrupt, "budget contract changed without a governing generation change", nil)
	}
	if limitsChanged && activeReservationBlocksLimitChange(*state, *record, declaration.Limits) {
		return nil, false, stateError(
			action.ReasonReservationIndeterminate,
			"budget limits cannot invalidate a prior active reservation",
			nil,
		)
	}
	changed := record.ScopeIdentity != scopeIdentity || record.Limits != declaration.Limits ||
		record.Generation != generation || resetChanged || !equalBudgetScopes(record.Scope, scope)
	if record.Generation != generation {
		if len(record.GenerationHistory) >= MaxGenerationHistory {
			return nil, false, stateError(action.ReasonStateUnavailable, "budget generation history is exhausted", nil)
		}
		record.GenerationHistory = append(record.GenerationHistory, generation)
	}
	record.ScopeIdentity, record.Scope = scopeIdentity, scope
	record.Reset, record.WindowSeconds = declaration.Reset, declaration.WindowSeconds
	record.Limits, record.Generation = declaration.Limits, generation
	return record, changed, nil
}

func activeReservationBlocksLimitChange(state State, record BudgetRecord, limits action.BudgetLimits) bool {
	proposed := record
	proposed.Limits = limits
	for _, reservation := range state.Reservations {
		for _, charge := range reservation.Charges {
			if charge.LineageIdentity == record.LineageIdentity &&
				validateReservationCharge(reservation.Status, charge, proposed) != nil {
				return true
			}
		}
	}
	return false
}

func activeReservationBlocksResetChange(state State, declaration action.Budget, scope action.BudgetScope) bool {
	for _, reservation := range state.Reservations {
		for _, charge := range reservation.Charges {
			record := budgetRecordForLineage(state.Budgets, charge.LineageIdentity)
			if record != nil && budgetCarryCompatible(*record, declaration.ID, scope) &&
				(record.Reset != declaration.Reset || record.WindowSeconds != declaration.WindowSeconds) {
				return true
			}
		}
	}
	return false
}

func carryForwardChangedReset(
	records []BudgetRecord,
	declaration action.Budget,
	scope action.BudgetScope,
	generation action.BudgetGeneration,
) (action.BudgetUsage, []action.BudgetGeneration, error) {
	for _, record := range records {
		if budgetCarryCompatible(record, declaration.ID, scope) && record.Reset == declaration.Reset &&
			record.WindowSeconds == declaration.WindowSeconds && record.Generation == generation {
			return action.BudgetUsage{}, []action.BudgetGeneration{generation}, nil
		}
	}
	consumed := action.BudgetUsage{}
	history := make([]action.BudgetGeneration, 0, 4)
	seen := make(map[action.BudgetGeneration]bool)
	for _, record := range records {
		if !budgetCarryCompatible(record, declaration.ID, scope) ||
			record.Reset == declaration.Reset && record.WindowSeconds == declaration.WindowSeconds {
			continue
		}
		consumed = saturatingUsageAdd(consumed, record.Consumed)
		for _, previous := range record.GenerationHistory {
			if seen[previous] || previous == generation {
				continue
			}
			seen[previous] = true
			history = append(history, previous)
		}
	}
	if len(history) >= MaxGenerationHistory {
		return action.BudgetUsage{}, nil, stateError(action.ReasonStateUnavailable, "budget reset migration history is exhausted", nil)
	}
	history = append(history, generation)
	return consumed, history, nil
}

func budgetCarryCompatible(record BudgetRecord, budgetID string, scope action.BudgetScope) bool {
	return record.BudgetID == budgetID && record.Scope.RepositoryIdentity == scope.RepositoryIdentity &&
		record.Scope.Principal == scope.Principal && record.Scope.ServerLabel == scope.ServerLabel &&
		record.Scope.ToolID == scope.ToolID && equalStrings(record.Scope.CredentialLabels, scope.CredentialLabels)
}

func equalStrings(left, right []string) bool {
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

func (s *Store) newReservation(input ReserveRequest, candidates []action.BudgetCandidate, now time.Time) (Reservation, error) {
	nonce := make([]byte, 32)
	read, err := io.ReadFull(secureRandomReader, nonce)
	if err != nil || read != len(nonce) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return Reservation{}, stateError(action.ReasonStateUnavailable, "generate budget reservation identity", err)
	}
	requestIdentity, err := s.reservationRequestIdentity(input)
	if err != nil {
		return Reservation{}, err
	}
	identity := s.key.Identity(DomainBudget, []byte("reservation"), []byte(input.Request.CallID), nonce)
	for index := range nonce {
		nonce[index] = 0
	}
	charges := make([]ReservationCharge, len(candidates))
	for index, candidate := range candidates {
		charges[index] = ReservationCharge{
			BudgetID: candidate.BudgetID, ScopeIdentity: candidate.ScopeIdentity,
			LineageIdentity: candidate.LineageIdentity, Generation: candidate.Generation,
			Reserved: candidate.Required,
		}
	}
	sort.Slice(charges, func(i, j int) bool { return charges[i].LineageIdentity < charges[j].LineageIdentity })
	return Reservation{
		Identity: identity, CallID: input.Request.CallID, OwnerID: s.ownerID,
		RequestIdentity: requestIdentity, ContextIdentity: input.Context.ContextIdentity,
		ExecutableDigest: input.Server.ExecutableDigest,
		Status:           ReservationReserved, CreatedAtUnix: now.Unix(), UpdatedAtUnix: now.Unix(),
		Charges: charges,
	}, nil
}

func (s *Store) reservationRequestIdentity(input ReserveRequest) (string, error) {
	request := input.Request
	request.StateVersion = "mutable-state-version"
	body, err := action.CanonicalRequest(request)
	if err != nil {
		return "", stateError(action.ReasonInvalidRequest, "encode stable reservation request identity", err)
	}
	return s.key.Identity(
		DomainUpstream, body, []byte(input.Context.ContextIdentity), []byte(input.Server.ExecutableDigest),
	), nil
}

func (s *Store) candidatesFromState(
	state State,
	declarations []action.Budget,
	tool action.Tool,
	input ReserveRequest,
	generation action.BudgetGeneration,
	now time.Time,
) ([]action.BudgetCandidate, error) {
	candidates := make([]action.BudgetCandidate, 0, len(declarations))
	reservedByLineage, err := reservedUsageByLineage(state)
	if err != nil {
		return nil, err
	}
	for _, declaration := range declarations {
		scope, lineage, scopeIdentity, err := s.budgetScope(declaration, tool.ID, input, now)
		if err != nil {
			return nil, err
		}
		record := budgetRecordForLineage(state.Budgets, lineage)
		if record == nil || record.ScopeIdentity != scopeIdentity || record.Generation != generation {
			return nil, stateError(action.ReasonStateCorrupt, "reconstruct budget candidate from current state", nil)
		}
		required, err := action.RequiredBudgetUsage(declaration.Limits, tool, input.Request)
		if err != nil {
			return nil, stateError(action.ReasonStateCorrupt, "derive exact budget reservation charge", err)
		}
		reserved := reservedByLineage[lineage]
		available := action.BudgetCapacityAvailable(
			record.Limits, record.Consumed, reserved, required, true,
		)
		reason := action.ReasonCode("")
		if !available {
			reason = action.ReasonBudgetExhausted
		}
		candidate := candidateFromRecord(*record, reserved, required, true, available, reason)
		candidate.Scope = scope
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s *Store) budgetSnapshot(stateVersion, reservation string, candidates []action.BudgetCandidate) action.BudgetSnapshot {
	snapshot := action.BudgetSnapshot{
		StateVersion: stateVersion, ReservationIdentity: reservation,
		Complete: true, Candidates: candidates,
	}
	body, err := json.Marshal(struct {
		StateVersion string                   `json:"state_version"`
		Reservation  string                   `json:"reservation"`
		Candidates   []action.BudgetCandidate `json:"candidates"`
	}{stateVersion, reservation, candidates})
	if err != nil {
		snapshot.Complete = false
		snapshot.Identity = "unavailable"
		return snapshot
	}
	snapshot.Identity = s.key.Identity(DomainBudget, []byte("snapshot"), body)
	return snapshot
}

func absentBudgetSnapshot(stateVersion string) action.BudgetSnapshot {
	return action.BudgetSnapshot{
		StateVersion: stateVersion, Identity: "absent", ReservationIdentity: "absent",
		Complete: true, Candidates: []action.BudgetCandidate{},
	}
}

func candidateFromRecord(
	record BudgetRecord,
	reserved, required action.BudgetUsage,
	applied, available bool,
	reason action.ReasonCode,
) action.BudgetCandidate {
	return action.BudgetCandidate{
		BudgetID: record.BudgetID, ScopeIdentity: record.ScopeIdentity,
		LineageIdentity: record.LineageIdentity, Scope: cloneBudgetScope(record.Scope),
		Reset: record.Reset, WindowSeconds: record.WindowSeconds, Limits: record.Limits,
		Consumed: record.Consumed, Reserved: reserved, Required: required,
		ReservationApplied: applied, Available: available, Reason: reason,
		Generation: record.Generation,
	}
}

func candidatesExhausted(candidates []action.BudgetCandidate) bool {
	for _, candidate := range candidates {
		if !candidate.Available {
			return true
		}
	}
	return false
}

func reservedUsageByLineage(state State) (map[string]action.BudgetUsage, error) {
	usage := make(map[string]action.BudgetUsage, len(state.Budgets))
	for _, reservation := range state.Reservations {
		for _, charge := range reservation.Charges {
			total, overflow := checkedUsageAdd(usage[charge.LineageIdentity], charge.Reserved)
			if overflow {
				return nil, stateError(action.ReasonStateCorrupt, "aggregate budget reservation overflowed", nil)
			}
			usage[charge.LineageIdentity] = total
		}
	}
	return usage, nil
}

func equalBudgetScopes(left, right action.BudgetScope) bool {
	if left.RepositoryIdentity != right.RepositoryIdentity || left.Principal != right.Principal ||
		left.ServerLabel != right.ServerLabel || left.ServerIdentity != right.ServerIdentity ||
		left.ToolID != right.ToolID || left.RunIdentity != right.RunIdentity ||
		left.SessionIdentity != right.SessionIdentity || left.WindowIdentity != right.WindowIdentity ||
		left.WindowStartUnix != right.WindowStartUnix || len(left.CredentialLabels) != len(right.CredentialLabels) {
		return false
	}
	for index := range left.CredentialLabels {
		if left.CredentialLabels[index] != right.CredentialLabels[index] {
			return false
		}
	}
	return true
}

func budgetRecordForLineage(records []BudgetRecord, lineage string) *BudgetRecord {
	index := sort.Search(len(records), func(index int) bool { return records[index].LineageIdentity >= lineage })
	if index == len(records) || records[index].LineageIdentity != lineage {
		return nil
	}
	return &records[index]
}

func reservationForCall(reservations []Reservation, callID string) *Reservation {
	for index := range reservations {
		if reservations[index].CallID == callID {
			return &reservations[index]
		}
	}
	return nil
}

func rejectTerminalOrUnsafeRetry(state State, callID, ownerID string) error {
	terminal := sort.Search(len(state.TerminalCalls), func(index int) bool {
		return state.TerminalCalls[index].CallID >= callID
	})
	if terminal < len(state.TerminalCalls) && state.TerminalCalls[terminal].CallID == callID {
		return stateError(action.ReasonReservationIndeterminate, "action call identity is already terminal", nil)
	}
	for _, reservation := range state.Reservations {
		if reservation.CallID == callID &&
			(reservation.OwnerID != ownerID || reservation.Status != ReservationReserved) {
			return stateError(action.ReasonReservationIndeterminate, "action call already has a non-reusable reservation", nil)
		}
	}
	return nil
}

func (s *Store) retryReservationSnapshot(
	state State,
	persisted bool,
	reservation Reservation,
	declarations []action.Budget,
	tool action.Tool,
	input ReserveRequest,
	clock ClockSnapshot,
) (ReserveResult, error) {
	if err := requireCurrentReservationContract(state, reservation, clock.Time); err != nil {
		return ReserveResult{}, s.persistClockObservationOnFailure(state, persisted, clock, err)
	}
	requestIdentity, err := s.reservationRequestIdentity(input)
	if err != nil {
		return ReserveResult{}, err
	}
	if reservation.RequestIdentity != requestIdentity ||
		reservation.ContextIdentity != input.Context.ContextIdentity ||
		reservation.ExecutableDigest != input.Server.ExecutableDigest {
		return ReserveResult{}, stateError(action.ReasonReservationIndeterminate, "call identity was reused with changed trusted input", nil)
	}
	generation := action.BudgetGeneration{
		PolicyDigest: input.Request.PolicyDigest, ExecutableDigest: input.Server.ExecutableDigest,
		ToolContractDigest: input.Request.ToolContractDigest, KeyID: s.key.ID(),
	}
	candidates, err := s.candidatesFromState(state, declarations, tool, input, generation, clock.Time)
	if err != nil {
		return ReserveResult{}, err
	}
	if len(candidates) != len(declarations) || candidatesExhausted(candidates) {
		return ReserveResult{}, stateError(action.ReasonStateCorrupt, "existing reservation no longer matches its budget records", nil)
	}
	if !reservationMatchesCandidates(reservation, candidates) {
		return ReserveResult{}, stateError(action.ReasonReservationIndeterminate, "existing reservation no longer matches its exact charges", nil)
	}
	copy := cloneReservation(reservation)
	return ReserveResult{
		Snapshot:    s.budgetSnapshot(state.Digest, reservation.Identity, candidates),
		Reservation: &copy,
	}, nil
}

func reservationMatchesCandidates(reservation Reservation, candidates []action.BudgetCandidate) bool {
	if len(reservation.Charges) != len(candidates) {
		return false
	}
	byLineage := make(map[string]action.BudgetCandidate, len(candidates))
	for _, candidate := range candidates {
		byLineage[candidate.LineageIdentity] = candidate
	}
	for _, charge := range reservation.Charges {
		candidate, exists := byLineage[charge.LineageIdentity]
		reserved := charge.Reserved
		reserved.ApprovalCount = 0
		if !exists || charge.BudgetID != candidate.BudgetID ||
			charge.ScopeIdentity != candidate.ScopeIdentity || charge.Generation != candidate.Generation ||
			reserved != candidate.Required {
			return false
		}
	}
	return true
}
