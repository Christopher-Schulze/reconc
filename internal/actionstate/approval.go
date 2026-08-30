package actionstate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

type ApprovalBinding struct {
	Plan          *action.CompiledPlan
	Evaluation    action.EvaluationInput
	BudgetRequest action.Request
	Context       BoundContext
	Authority     PolicyAuthority
	Server        ObservedServer
}

type ApprovalIssueRequest struct {
	Binding           ApprovalBinding
	AuthorityPolicyID string
	TTL               time.Duration
}

type ApprovalIssueResult struct {
	Request      actionapproval.Request `json:"request"`
	StateVersion string                 `json:"state_version"`
	RequestState string                 `json:"request_state"`
	Evidence     ApprovalEvidence       `json:"evidence"`
}

type ApprovalConsumeRequest struct {
	Binding              ApprovalBinding
	Registry             LoadedApprovalRegistry
	Receipt              []byte
	RequestState         string
	ExpectedStateVersion string
}

type ApprovalConsumeResult struct {
	StateVersion     string                  `json:"state_version"`
	Status           actionapproval.Status   `json:"status"`
	Approval         action.ApprovalSnapshot `json:"approval"`
	AuthorityKeyID   string                  `json:"authority_key_id,omitempty"`
	ReceiptID        string                  `json:"receipt_id,omitempty"`
	ReceiptIdentity  string                  `json:"receipt_identity,omitempty"`
	ReceiptSignedAt  string                  `json:"receipt_signed_at,omitempty"`
	RegistryIdentity string                  `json:"registry_identity,omitempty"`
	Evidence         ApprovalEvidence        `json:"evidence"`
}

const ApprovalEvidenceSchema = "reconc.action-approval-evidence/v1"

// ApprovalEvidence is the payload-free transition contract consumed by the
// action ledger and explanation layers. Every value is a safe label, timestamp,
// counter, or bound cryptographic identity.
type ApprovalEvidence struct {
	Schema                    string                  `json:"schema"`
	RequestID                 string                  `json:"request_id"`
	CallID                    string                  `json:"call_id"`
	RequestIdentity           string                  `json:"request_identity"`
	RequiredApprovalIdentity  string                  `json:"required_approval_identity"`
	Status                    actionapproval.Status   `json:"status"`
	Decision                  actionapproval.Decision `json:"decision,omitempty"`
	AuthorityPolicyID         string                  `json:"authority_policy_id"`
	AuthorityKeyID            string                  `json:"authority_key_id,omitempty"`
	ReceiptID                 string                  `json:"receipt_id,omitempty"`
	ReceiptIdentity           string                  `json:"receipt_identity,omitempty"`
	ReceiptSignedAt           string                  `json:"receipt_signed_at,omitempty"`
	RegistryIdentity          string                  `json:"registry_identity,omitempty"`
	PlanIdentity              string                  `json:"plan_identity"`
	SourceIdentity            string                  `json:"source_identity"`
	RepositoryIdentity        string                  `json:"repository_identity"`
	PolicyDigest              string                  `json:"policy_digest"`
	LockDigest                string                  `json:"lock_digest"`
	ExecutableDigest          string                  `json:"executable_digest"`
	ServerLabel               string                  `json:"server_label"`
	ServerFingerprint         string                  `json:"server_fingerprint"`
	ToolID                    string                  `json:"tool_id"`
	ToolContractDigest        string                  `json:"tool_contract_digest"`
	Phase                     action.Phase            `json:"phase"`
	Principal                 string                  `json:"principal"`
	ContextIdentity           string                  `json:"context_identity"`
	CredentialLabels          []string                `json:"credential_labels"`
	TaintIdentity             string                  `json:"taint_identity"`
	RepositoryEffectIdentity  string                  `json:"repository_effect_identity"`
	BudgetReservationIdentity string                  `json:"budget_reservation_identity"`
	RuleIDs                   []string                `json:"rule_ids"`
	IssuedAt                  string                  `json:"issued_at"`
	ExpiresAt                 string                  `json:"expires_at"`
	UpdatedAtUnix             int64                   `json:"updated_at_unix"`
}

type ApprovalFinalizeRequest struct {
	RequestState         string
	ExpectedStateVersion string
	Status               actionapproval.Status
}

type ApprovalReconcileResult struct {
	StateVersion string                  `json:"state_version"`
	Expired      []ApprovalConsumeResult `json:"expired"`
}

func (s *Store) IssueApproval(
	ctx context.Context,
	input ApprovalIssueRequest,
) (result ApprovalIssueResult, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		var err error
		result, err = s.issueApprovalLocked(input)
		return err
	})
	return result, resultErr
}

func (s *Store) issueApprovalLocked(input ApprovalIssueRequest) (ApprovalIssueResult, error) {
	state, persisted, err := s.loadState()
	if err != nil {
		return ApprovalIssueResult{}, err
	}
	clock, err := s.trustedNow(state)
	if err != nil {
		return ApprovalIssueResult{}, err
	}
	evaluation, decision, reservationIdentity, err := s.prepareApprovalEvaluation(state, persisted, clock, input.Binding)
	if err != nil {
		return ApprovalIssueResult{}, err
	}
	request, err := s.newApprovalRequest(
		input, evaluation, decision, reservationIdentity, state, clock.Time,
	)
	if err != nil {
		return ApprovalIssueResult{}, err
	}
	return s.persistPendingApproval(state, persisted, clock, request)
}

func (s *Store) prepareApprovalEvaluation(
	state State,
	persisted bool,
	clock ClockSnapshot,
	binding ApprovalBinding,
) (action.EvaluationInput, action.EvaluationResult, string, error) {
	binding.Evaluation.Request.StateVersion = state.Digest
	reserve := binding.reserveRequest()
	if err := s.validateReserveRequest(reserve); err != nil {
		return action.EvaluationInput{}, action.EvaluationResult{}, "", err
	}
	if err := validateEvaluationBinding(binding); err != nil {
		return action.EvaluationInput{}, action.EvaluationResult{}, "", err
	}
	budget, reservationIdentity, err := s.approvalBudgetSnapshot(
		state, persisted, clock, binding,
	)
	if err != nil {
		return action.EvaluationInput{}, action.EvaluationResult{}, "", err
	}
	evaluator, err := action.NewEvaluator(binding.Plan)
	if err != nil {
		return action.EvaluationInput{}, action.EvaluationResult{}, "", stateError(action.ReasonPolicyMissing, "compile approval evaluator", err)
	}
	evaluation := binding.Evaluation
	evaluation.Budget = budget
	evaluation.ResampledIdentities = evaluator.IdentitySnapshot(evaluation)
	result := evaluator.Evaluate(evaluation)
	if result.Failure != nil || result.Decision != action.DecisionRequireApproval ||
		!action.ValidSHA256Identity(result.RequiredApprovalIdentity) || result.Cache.Eligible ||
		result.Cache.Reason != action.CacheApprovalPending {
		return action.EvaluationInput{}, action.EvaluationResult{}, "", stateError(
			action.ReasonApprovalInvalid, "current action decision does not require a fresh approval", nil,
		)
	}
	return evaluation, result, reservationIdentity, nil
}

func validateEvaluationBinding(binding ApprovalBinding) error {
	evaluation := binding.Evaluation
	credentials := credentialLabels(binding.Context.Credentials)
	if evaluation.ContextIdentity != binding.Context.ContextIdentity ||
		evaluation.ExecutableDigest != binding.Server.ExecutableDigest ||
		evaluation.Principal != binding.Context.Principal ||
		!equalStringSlices(evaluation.CredentialLabels, credentials) ||
		evaluation.Approval.Status != action.ApprovalNone || evaluation.Approval.Identity != "approval-none" ||
		evaluation.Lifecycle != action.LifecycleActive {
		return stateError(action.ReasonIdentityUnavailable, "approval evaluation does not match fresh trusted context", nil)
	}
	if evaluation.Request.Phase == action.PhasePostResult {
		budget := binding.BudgetRequest
		request := evaluation.Request
		if budget.Phase != action.PhasePreCall || budget.CallID != request.CallID ||
			budget.RepositoryIdentity != request.RepositoryIdentity ||
			budget.PolicyDigest != request.PolicyDigest || budget.LockDigest != request.LockDigest ||
			budget.AuthorityMode != request.AuthorityMode || budget.Transport != request.Transport ||
			budget.Platform != request.Platform || budget.ServerLabel != request.ServerLabel ||
			budget.ServerFingerprint != request.ServerFingerprint || budget.Tool != request.Tool ||
			budget.ToolContractDigest != request.ToolContractDigest {
			return stateError(action.ReasonIdentityUnavailable, "post-result approval budget request binding changed", nil)
		}
	}
	return nil
}

func (s *Store) approvalBudgetSnapshot(
	state State,
	persisted bool,
	clock ClockSnapshot,
	binding ApprovalBinding,
) (action.BudgetSnapshot, string, error) {
	input := binding.budgetReserveRequest()
	input.Request.StateVersion = state.Digest
	if err := s.validateReserveRequest(input); err != nil {
		return action.BudgetSnapshot{}, "", err
	}
	tool, declarations, err := input.Plan.BudgetContract(input.Request)
	if err != nil {
		return action.BudgetSnapshot{}, "", stateError(action.ReasonStateCorrupt, "resolve approval budget contract", err)
	}
	if len(declarations) == 0 {
		return absentBudgetSnapshot(state.Digest), "absent", nil
	}
	reservation := reservationForCall(state.Reservations, input.Request.CallID)
	wantStatus := ReservationReserved
	if binding.Evaluation.Request.Phase == action.PhasePostResult {
		wantStatus = ReservationDispatched
	}
	if reservation == nil || reservation.OwnerID != s.ownerID || reservation.Status != wantStatus {
		return action.BudgetSnapshot{}, "", stateError(action.ReasonReservationIndeterminate, "approval requires its live owned budget reservation", nil)
	}
	if err := requireCurrentReservationContract(state, *reservation, clock.Time); err != nil {
		return action.BudgetSnapshot{}, "", s.persistClockObservationOnFailure(state, persisted, clock, err)
	}
	requestIdentity, err := s.reservationRequestIdentity(input)
	if err != nil {
		return action.BudgetSnapshot{}, "", err
	}
	if reservation.RequestIdentity != requestIdentity ||
		reservation.ContextIdentity != input.Context.ContextIdentity ||
		reservation.ExecutableDigest != input.Server.ExecutableDigest ||
		!reservationMatchesDeclarations(*reservation, declarations) {
		return action.BudgetSnapshot{}, "", stateError(action.ReasonReservationIndeterminate, "approval budget reservation binding changed", nil)
	}
	if binding.Evaluation.Request.Phase == action.PhasePostResult {
		return absentBudgetSnapshot(state.Digest), reservation.Identity, nil
	}
	result, err := s.retryReservationSnapshot(state, persisted, *reservation, declarations, tool, input, clock)
	if err != nil {
		return action.BudgetSnapshot{}, "", err
	}
	return result.Snapshot, reservation.Identity, nil
}

func reservationMatchesDeclarations(reservation Reservation, declarations []action.Budget) bool {
	if len(reservation.Charges) != len(declarations) {
		return false
	}
	budgetIDs := make(map[string]bool, len(declarations))
	for _, declaration := range declarations {
		budgetIDs[declaration.ID] = true
	}
	for _, charge := range reservation.Charges {
		if !budgetIDs[charge.BudgetID] {
			return false
		}
	}
	return true
}

func (s *Store) newApprovalRequest(
	input ApprovalIssueRequest,
	evaluation action.EvaluationInput,
	decision action.EvaluationResult,
	budgetReservationIdentity string,
	state State,
	now time.Time,
) (actionapproval.Request, error) {
	requestIdentity, err := s.reservationRequestIdentity(input.Binding.reserveRequest())
	if err != nil {
		return actionapproval.Request{}, err
	}
	selected, err := s.selectedApprovalArguments(input.Binding.Plan, evaluation.Request, requestIdentity)
	if err != nil {
		return actionapproval.Request{}, err
	}
	return actionapproval.NewRequest(actionapproval.RequestInput{
		CallID: evaluation.Request.CallID, RequestIdentity: requestIdentity,
		RequiredApprovalIdentity: decision.RequiredApprovalIdentity,
		PlanIdentity:             decision.PlanIdentity, SourceIdentity: decision.SourceIdentity,
		RepositoryIdentity: evaluation.Request.RepositoryIdentity, StateVersion: state.Digest,
		PolicyDigest: evaluation.Request.PolicyDigest, LockDigest: evaluation.Request.LockDigest,
		ExecutableDigest: input.Binding.Server.ExecutableDigest,
		ServerLabel:      evaluation.Request.ServerLabel, ServerFingerprint: evaluation.Request.ServerFingerprint,
		ToolID: decision.ToolID, Tool: evaluation.Request.Tool,
		ToolContractDigest: evaluation.Request.ToolContractDigest, Phase: evaluation.Request.Phase,
		Principal: input.Binding.Context.Principal, ContextIdentity: input.Binding.Context.ContextIdentity,
		CredentialLabels: credentialLabels(input.Binding.Context.Credentials),
		TaintIdentity:    evaluation.Taint.Identity, RepositoryEffectIdentity: repositoryEffectIdentity(evaluation),
		SelectedArguments: selected, BudgetReservationID: budgetReservationIdentity,
		ReasonCode: decision.Reason, RuleIDs: append([]string(nil), decision.MatchedRuleIDs...),
		AuthorityPolicyID: input.AuthorityPolicyID, IssuedAt: now, TTL: input.TTL,
	}, secureRandomReader)
}

func (s *Store) selectedApprovalArguments(
	plan *action.CompiledPlan,
	request action.Request,
	requestIdentity string,
) ([]actionapproval.SelectedArgument, error) {
	_, pointers, err := plan.ApprovalDisclosures(request)
	if err != nil {
		return nil, stateError(action.ReasonPolicyMissing, "resolve approval disclosures", err)
	}
	root := request.Arguments
	if request.Phase == action.PhasePostResult {
		root = request.Result
	}
	if len(pointers) > 0 && root == nil {
		return nil, stateError(action.ReasonInspectionIncomplete, "selected approval arguments are unavailable", nil)
	}
	selected := make([]actionapproval.SelectedArgument, 0, len(pointers))
	for _, pointer := range pointers {
		summary, summaryErr := s.selectedApprovalArgument(*root, pointer, requestIdentity)
		if summaryErr != nil {
			return nil, summaryErr
		}
		selected = append(selected, summary)
	}
	return selected, nil
}

func (s *Store) selectedApprovalArgument(
	root action.Value,
	pointer string,
	requestIdentity string,
) (actionapproval.SelectedArgument, error) {
	resolved, err := action.ResolvePointer(root, pointer)
	if err != nil {
		return actionapproval.SelectedArgument{}, stateError(action.ReasonPolicyMissing, "resolve approval argument pointer", err)
	}
	summary := actionapproval.SelectedArgument{Pointer: pointer, State: resolved.State}
	valueBody := []byte{}
	if resolved.State == action.PointerPresent || resolved.State == action.PointerNull {
		valueBody, err = resolved.Value.MarshalJSON()
		if err != nil {
			return actionapproval.SelectedArgument{}, stateError(action.ReasonApprovalInvalid, "encode selected approval argument", err)
		}
		summary.Kind = resolved.Value.Kind()
		summary.ByteLength = uint64(len(valueBody))
	}
	summary.Identity = s.key.Identity(
		DomainArgument, []byte("approval-selected"), []byte(requestIdentity), []byte(pointer),
		[]byte(summary.State), []byte(summary.Kind), valueBody,
	)
	return summary, nil
}

func (s *Store) persistPendingApproval(
	state State,
	persisted bool,
	clock ClockSnapshot,
	request actionapproval.Request,
) (ApprovalIssueResult, error) {
	if len(state.Approvals) >= MaxApprovalRecords || pendingApprovalCount(state.Approvals) >= MaxPendingApprovals {
		return ApprovalIssueResult{}, stateError(action.ReasonStateUnavailable, "approval state capacity is exhausted", nil)
	}
	if approvalRecordForCallPhase(state.Approvals, request.CallID, request.Phase) != nil {
		return ApprovalIssueResult{}, stateError(action.ReasonApprovalReplayed, "action call phase already has an approval record", nil)
	}
	next := cloneState(state)
	if request.BudgetReservationID != "absent" {
		index := reservationIndex(next.Reservations, request.BudgetReservationID)
		if index < 0 || next.Reservations[index].CallID != request.CallID {
			return ApprovalIssueResult{}, stateError(action.ReasonReservationIndeterminate, "approval budget reservation changed", nil)
		}
		if _, err := reserveApprovalCharges(&next, index); err != nil {
			return ApprovalIssueResult{}, err
		}
		next.Reservations[index].UpdatedAtUnix = clock.Time.Unix()
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(request.Nonce)
	if err != nil {
		return ApprovalIssueResult{}, approvalContractError("decode validated approval nonce", err)
	}
	record := ApprovalRecord{
		Request: request, Status: actionapproval.StatusPending,
		ReservationIdentity: request.BudgetReservationID,
		NonceIdentity:       s.key.Identity(DomainApproval, []byte("nonce"), nonce),
		UpdatedAtUnix:       clock.Time.Unix(),
	}
	next.Approvals = append(next.Approvals, record)
	sort.Slice(next.Approvals, func(i, j int) bool {
		return next.Approvals[i].Request.RequestID < next.Approvals[j].Request.RequestID
	})
	s.applyClock(&next, clock)
	version := ""
	if err := s.writeStateMustAdvance(state, next, persisted, &version); err != nil {
		return ApprovalIssueResult{}, err
	}
	requestState, err := s.sealApprovalRequestState(request, version)
	if err != nil {
		return ApprovalIssueResult{}, err
	}
	return ApprovalIssueResult{
		Request: cloneApprovalRequest(request), StateVersion: version,
		RequestState: requestState,
		Evidence:     approvalEvidence(record),
	}, nil
}

func (s *Store) ConsumeApproval(
	ctx context.Context,
	input ApprovalConsumeRequest,
) (result ApprovalConsumeResult, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		var err error
		result, err = s.consumeApprovalLocked(input)
		return err
	})
	return result, resultErr
}

func (s *Store) consumeApprovalLocked(input ApprovalConsumeRequest) (ApprovalConsumeResult, error) {
	if err := s.resampleRepositoryIdentity(); err != nil {
		return ApprovalConsumeResult{}, err
	}
	state, persisted, err := s.loadState()
	if err != nil {
		return ApprovalConsumeResult{}, err
	}
	clock, err := s.trustedNow(state)
	if err != nil {
		return ApprovalConsumeResult{}, err
	}
	token, err := s.openApprovalRequestState(input.RequestState)
	if err != nil {
		return ApprovalConsumeResult{}, err
	}
	index := approvalRecordIndex(state.Approvals, token.RequestID)
	if index < 0 {
		return ApprovalConsumeResult{}, stateError(action.ReasonApprovalInvalid, "approval request state is unknown", nil)
	}
	record := state.Approvals[index]
	if !token.matches(record) {
		return ApprovalConsumeResult{}, stateError(action.ReasonApprovalInvalid, "approval request state binding changed", nil)
	}
	if record.Status != actionapproval.StatusPending {
		return approvalResult(state.Digest, record), stateError(action.ReasonApprovalReplayed, "approval receipt is no longer consumable", nil)
	}
	if input.ExpectedStateVersion != token.IssuanceStateVersion {
		return ApprovalConsumeResult{}, stateError(action.ReasonStateUnavailable, "approval issuance snapshot does not match its sealed request state", nil)
	}
	if err := s.validateCurrentApprovalBinding(state, clock, input.Binding, record); err != nil {
		return ApprovalConsumeResult{}, err
	}
	registry := input.Registry.compiled()
	verified, verifyErr := actionapproval.VerifySignedReceipt(registry, record.Request, input.Receipt, clock.Time)
	if verifyErr != nil {
		status := approvalFailureStatus(verifyErr)
		result, finishErr := s.finishPendingApproval(state, persisted, clock, index, status, nil, nil)
		if finishErr != nil {
			return result, errors.Join(
				stateError(approvalReason(verifyErr), "approval receipt verification failed", verifyErr),
				finishErr,
			)
		}
		return result, stateError(approvalReason(verifyErr), "approval receipt verification failed", verifyErr)
	}
	if verified.Receipt.Decision == actionapproval.DecisionReject {
		result, finishErr := s.finishPendingApproval(state, persisted, clock, index, actionapproval.StatusRejected, &verified, registry)
		if finishErr != nil {
			return result, errors.Join(
				stateError(action.ReasonApprovalRejected, "approval authority rejected the request", nil),
				finishErr,
			)
		}
		return result, stateError(action.ReasonApprovalRejected, "approval authority rejected the request", nil)
	}
	return s.commitPendingApproval(state, persisted, clock, index, verified, registry)
}

func (s *Store) validateCurrentApprovalBinding(
	state State,
	clock ClockSnapshot,
	binding ApprovalBinding,
	record ApprovalRecord,
) error {
	if err := validateEvaluationBinding(binding); err != nil {
		return err
	}
	reserve := binding.reserveRequest()
	reserve.Request.StateVersion = state.Digest
	if err := s.validateReserveRequest(reserve); err != nil {
		return err
	}
	requestIdentity, err := s.reservationRequestIdentity(reserve)
	if err != nil {
		return err
	}
	evaluator, err := action.NewEvaluator(binding.Plan)
	if err != nil {
		return stateError(action.ReasonPolicyMissing, "compile current approval policy", err)
	}
	currentEffect := repositoryEffectIdentity(binding.Evaluation)
	credentials := credentialLabels(binding.Context.Credentials)
	if requestIdentity != record.Request.RequestIdentity || evaluator.PlanIdentity() != record.Request.PlanIdentity ||
		binding.Evaluation.SourceIdentity != record.Request.SourceIdentity ||
		binding.Context.ContextIdentity != record.Request.ContextIdentity ||
		binding.Context.Principal != record.Request.Principal || !equalStringSlices(credentials, record.Request.CredentialLabels) ||
		binding.Server.ExecutableDigest != record.Request.ExecutableDigest ||
		binding.Evaluation.Taint.Identity != record.Request.TaintIdentity ||
		currentEffect != record.Request.RepositoryEffectIdentity {
		return stateError(action.ReasonIdentityUnavailable, "current trusted approval binding changed", nil)
	}
	return s.validateApprovalReservation(state, clock.Time, binding, record)
}

func (s *Store) validateApprovalReservation(
	state State,
	now time.Time,
	binding ApprovalBinding,
	record ApprovalRecord,
) error {
	budget := binding.budgetReserveRequest()
	budget.Request.StateVersion = state.Digest
	if err := s.validateReserveRequest(budget); err != nil {
		return err
	}
	tool, declarations, err := budget.Plan.BudgetContract(budget.Request)
	if err != nil || tool.ID != record.Request.ToolID {
		return stateError(action.ReasonPolicyMissing, "current approval tool contract changed", err)
	}
	if record.ReservationIdentity == "absent" {
		if len(declarations) != 0 {
			return stateError(action.ReasonReservationIndeterminate, "approval lost its required budget reservation", nil)
		}
		return nil
	}
	index := reservationIndex(state.Reservations, record.ReservationIdentity)
	if index < 0 {
		return stateError(action.ReasonReservationIndeterminate, "approval budget reservation is absent", nil)
	}
	reservation := state.Reservations[index]
	wantStatus := ReservationReserved
	if record.Request.Phase == action.PhasePostResult {
		wantStatus = ReservationDispatched
	}
	requestIdentity, identityErr := s.reservationRequestIdentity(budget)
	if identityErr != nil {
		return identityErr
	}
	if reservation.Status != wantStatus || reservation.OwnerID != s.ownerID ||
		reservation.CallID != record.Request.CallID || reservation.RequestIdentity != requestIdentity ||
		reservation.ContextIdentity != record.Request.ContextIdentity ||
		reservation.ExecutableDigest != record.Request.ExecutableDigest ||
		!reservationMatchesDeclarations(reservation, declarations) {
		return stateError(action.ReasonReservationIndeterminate, "approval budget reservation binding changed", nil)
	}
	return requireCurrentReservationContract(state, reservation, now)
}

func (s *Store) commitPendingApproval(
	state State,
	persisted bool,
	clock ClockSnapshot,
	index int,
	verified actionapproval.Verification,
	registry *actionapproval.CompiledRegistry,
) (ApprovalConsumeResult, error) {
	next := cloneState(state)
	record := &next.Approvals[index]
	if approvalReceiptExists(next.Approvals, verified.Receipt.ReceiptID, index) {
		replayErr := stateError(action.ReasonApprovalReplayed, "approval receipt identity was already consumed", nil)
		result, finishErr := s.finishPendingApproval(
			state, persisted, clock, index, actionapproval.StatusReplayed, nil, nil,
		)
		if finishErr != nil {
			return result, errors.Join(replayErr, finishErr)
		}
		return result, replayErr
	}
	if record.ReservationIdentity != "absent" {
		reservationPosition := reservationIndex(next.Reservations, record.ReservationIdentity)
		if reservationPosition < 0 {
			return ApprovalConsumeResult{}, stateError(action.ReasonReservationIndeterminate, "approval budget reservation is absent", nil)
		}
		if _, err := commitApprovalCharges(&next, reservationPosition); err != nil {
			return ApprovalConsumeResult{}, err
		}
		next.Reservations[reservationPosition].UpdatedAtUnix = clock.Time.Unix()
	}
	setVerifiedApprovalMetadata(record, actionapproval.StatusApproved, verified, registry)
	record.UpdatedAtUnix = clock.Time.Unix()
	s.applyClock(&next, clock)
	version := ""
	if err := s.writeStateMustAdvance(state, next, persisted, &version); err != nil {
		return ApprovalConsumeResult{}, err
	}
	return approvalResult(version, *record), nil
}

func (s *Store) FinalizeApproval(
	ctx context.Context,
	input ApprovalFinalizeRequest,
) (result ApprovalConsumeResult, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		if !finalApprovalFailureStatus(input.Status) {
			return stateError(action.ReasonApprovalInvalid, "approval final status is invalid", nil)
		}
		if err := s.resampleRepositoryIdentity(); err != nil {
			return err
		}
		state, persisted, clock, index, err := s.pendingApprovalTransition(input.RequestState, input.ExpectedStateVersion)
		if err != nil {
			return err
		}
		if input.Status == actionapproval.StatusExpired {
			expires, parseErr := time.Parse(time.RFC3339Nano, state.Approvals[index].Request.ExpiresAt)
			if parseErr != nil || clock.Time.Before(expires) {
				return stateError(action.ReasonApprovalInvalid, "approval has not reached its trusted expiry", parseErr)
			}
		}
		result, err = s.finishPendingApproval(state, persisted, clock, index, input.Status, nil, nil)
		return err
	})
	return result, resultErr
}

// ReconcileExpiredApprovals atomically terminalizes every pending approval
// whose trusted expiry has elapsed. Gateways call it during startup and before
// accepting new work so a crashed authority wait cannot strand reservations.
func (s *Store) ReconcileExpiredApprovals(
	ctx context.Context,
) (result ApprovalReconcileResult, resultErr error) {
	resultErr = s.withLock(ctx, func() error {
		if err := s.resampleRepositoryIdentity(); err != nil {
			return err
		}
		state, persisted, err := s.loadState()
		if err != nil {
			return err
		}
		clock, err := s.trustedNow(state)
		if err != nil {
			return err
		}
		result, err = s.reconcileExpiredApprovalsLocked(state, persisted, clock)
		return err
	})
	return result, resultErr
}

func (s *Store) reconcileExpiredApprovalsLocked(
	state State,
	persisted bool,
	clock ClockSnapshot,
) (ApprovalReconcileResult, error) {
	next := cloneState(state)
	expiredIndexes := make([]int, 0, len(next.Approvals))
	denialCapacityExhausted := false
	for index := range next.Approvals {
		record := &next.Approvals[index]
		if record.Status != actionapproval.StatusPending {
			continue
		}
		expires, err := time.Parse(time.RFC3339Nano, record.Request.ExpiresAt)
		if err != nil {
			return ApprovalReconcileResult{}, stateError(action.ReasonStateCorrupt, "parse pending approval expiry", err)
		}
		if clock.Time.Before(expires) {
			continue
		}
		record.Status = actionapproval.StatusExpired
		record.UpdatedAtUnix = clock.Time.Unix()
		exhausted, err := terminalizeApprovalReservation(&next, *record, clock.Time)
		if err != nil {
			return ApprovalReconcileResult{}, err
		}
		denialCapacityExhausted = denialCapacityExhausted || exhausted
		expiredIndexes = append(expiredIndexes, index)
	}
	if len(expiredIndexes) == 0 {
		return ApprovalReconcileResult{StateVersion: state.Digest, Expired: []ApprovalConsumeResult{}}, nil
	}
	s.applyClock(&next, clock)
	version := ""
	if err := s.writeStateMustAdvance(state, next, persisted, &version); err != nil {
		return ApprovalReconcileResult{}, err
	}
	expired := make([]ApprovalConsumeResult, len(expiredIndexes))
	for position, index := range expiredIndexes {
		expired[position] = approvalResult(version, next.Approvals[index])
	}
	result := ApprovalReconcileResult{StateVersion: version, Expired: expired}
	if denialCapacityExhausted {
		return result, stateError(action.ReasonBudgetExhausted, "denial-count capacity is exhausted", nil)
	}
	return result, nil
}

func (s *Store) pendingApprovalTransition(
	requestState string,
	expectedVersion string,
) (State, bool, ClockSnapshot, int, error) {
	state, persisted, err := s.loadState()
	if err != nil {
		return State{}, false, ClockSnapshot{}, -1, err
	}
	clock, err := s.trustedNow(state)
	if err != nil {
		return State{}, false, ClockSnapshot{}, -1, err
	}
	token, err := s.openApprovalRequestState(requestState)
	if err != nil {
		return State{}, false, ClockSnapshot{}, -1, err
	}
	index := approvalRecordIndex(state.Approvals, token.RequestID)
	if expectedVersion != token.IssuanceStateVersion || index < 0 ||
		state.Approvals[index].Status != actionapproval.StatusPending || !token.matches(state.Approvals[index]) {
		return State{}, false, ClockSnapshot{}, -1, stateError(action.ReasonStateUnavailable, "pending approval transition is stale", nil)
	}
	return state, persisted, clock, index, nil
}

func (s *Store) finishPendingApproval(
	state State,
	persisted bool,
	clock ClockSnapshot,
	index int,
	status actionapproval.Status,
	verified *actionapproval.Verification,
	registry *actionapproval.CompiledRegistry,
) (ApprovalConsumeResult, error) {
	next := cloneState(state)
	record := &next.Approvals[index]
	record.Status = status
	record.UpdatedAtUnix = clock.Time.Unix()
	if verified != nil {
		setVerifiedApprovalMetadata(record, status, *verified, registry)
	}
	denialCapacityExhausted, err := terminalizeApprovalReservation(&next, *record, clock.Time)
	if err != nil {
		return ApprovalConsumeResult{}, err
	}
	s.applyClock(&next, clock)
	version := ""
	if err := s.writeStateMustAdvance(state, next, persisted, &version); err != nil {
		return ApprovalConsumeResult{}, err
	}
	result := approvalResult(version, *record)
	if denialCapacityExhausted {
		return result, stateError(action.ReasonBudgetExhausted, "denial-count capacity is exhausted", nil)
	}
	return result, nil
}

func terminalizeApprovalReservation(state *State, record ApprovalRecord, now time.Time) (bool, error) {
	if record.ReservationIdentity == "absent" {
		return false, nil
	}
	index := reservationIndex(state.Reservations, record.ReservationIdentity)
	if index < 0 {
		return false, stateError(action.ReasonStateCorrupt, "pending approval budget reservation is absent", nil)
	}
	reservation := &state.Reservations[index]
	if record.Request.Phase == action.PhasePostResult {
		if reservation.Status != ReservationDispatched {
			return false, stateError(action.ReasonStateCorrupt, "post-result approval reservation is not dispatched", nil)
		}
		if _, err := releaseApprovalCharges(state, index); err != nil {
			return false, err
		}
		reservation.UpdatedAtUnix = now.Unix()
		return false, nil
	}
	if reservation.Status != ReservationReserved {
		return false, stateError(action.ReasonStateCorrupt, "pending approval budget reservation passed dispatch", nil)
	}
	exhausted, err := recordDenialCharges(state, index)
	if err != nil {
		return false, err
	}
	err = removeReservationAndRecord(state, index, TerminalCall{
		CallID: reservation.CallID, ReservationIdentity: reservation.Identity,
		Outcome: OutcomeBlocked, CompletedAtUnix: now.Unix(),
	})
	return exhausted, err
}

func terminalizeAbandonedPendingApprovals(state *State, ownerID string, now time.Time) error {
	for index := range state.Approvals {
		record := &state.Approvals[index]
		if record.Status != actionapproval.StatusPending || record.ReservationIdentity == "absent" {
			continue
		}
		position := reservationIndex(state.Reservations, record.ReservationIdentity)
		if position < 0 {
			return stateError(action.ReasonStateCorrupt, "pending approval budget reservation is absent", nil)
		}
		reservation := state.Reservations[position]
		if reservation.OwnerID != ownerID || reservation.Status == ReservationIndeterminate {
			continue
		}
		if _, err := releaseApprovalCharges(state, position); err != nil {
			return err
		}
		record.Status = actionapproval.StatusUnavailable
		record.UpdatedAtUnix = now.Unix()
	}
	return nil
}

func setVerifiedApprovalMetadata(
	record *ApprovalRecord,
	status actionapproval.Status,
	verified actionapproval.Verification,
	registry *actionapproval.CompiledRegistry,
) {
	record.Status = status
	record.RegistryIdentity = registry.Identity()
	record.AuthorityKeyID = verified.Receipt.AuthorityKeyID
	record.ReceiptID = verified.Receipt.ReceiptID
	record.ReceiptIdentity = verified.Identity
	record.ReceiptSignedAt = verified.Receipt.SignedAt
	record.ReceiptDecision = verified.Receipt.Decision
	record.ReceiptSignature = verified.Receipt.Signature
}

func approvalResult(version string, record ApprovalRecord) ApprovalConsumeResult {
	approvalStatus := action.ApprovalConsumed
	identity := record.ReceiptIdentity
	if record.Status != actionapproval.StatusApproved {
		approvalStatus = action.ApprovalNone
		identity = "approval-" + string(record.Status)
	}
	return ApprovalConsumeResult{
		StateVersion: version, Status: record.Status,
		Approval:       action.ApprovalSnapshot{Status: approvalStatus, Identity: identity},
		AuthorityKeyID: record.AuthorityKeyID, ReceiptID: record.ReceiptID,
		ReceiptIdentity: record.ReceiptIdentity, ReceiptSignedAt: record.ReceiptSignedAt,
		RegistryIdentity: record.RegistryIdentity,
		Evidence:         approvalEvidence(record),
	}
}

func approvalEvidence(record ApprovalRecord) ApprovalEvidence {
	decision := actionapproval.Decision("")
	if record.Status == actionapproval.StatusApproved {
		decision = actionapproval.DecisionApprove
	} else if record.Status == actionapproval.StatusRejected {
		decision = actionapproval.DecisionReject
	}
	request := record.Request
	return ApprovalEvidence{
		Schema: ApprovalEvidenceSchema, RequestID: request.RequestID, CallID: request.CallID,
		RequestIdentity: request.RequestIdentity, RequiredApprovalIdentity: request.RequiredApprovalIdentity,
		Status: record.Status, Decision: decision, AuthorityPolicyID: request.AuthorityPolicyID,
		AuthorityKeyID: record.AuthorityKeyID, ReceiptID: record.ReceiptID,
		ReceiptIdentity: record.ReceiptIdentity, ReceiptSignedAt: record.ReceiptSignedAt,
		RegistryIdentity: record.RegistryIdentity,
		PlanIdentity:     request.PlanIdentity, SourceIdentity: request.SourceIdentity,
		RepositoryIdentity: request.RepositoryIdentity, PolicyDigest: request.PolicyDigest,
		LockDigest: request.LockDigest, ExecutableDigest: request.ExecutableDigest,
		ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
		ToolID: request.ToolID, ToolContractDigest: request.ToolContractDigest,
		Phase: request.Phase, Principal: request.Principal, ContextIdentity: request.ContextIdentity,
		CredentialLabels: append([]string{}, request.CredentialLabels...),
		TaintIdentity:    request.TaintIdentity, RepositoryEffectIdentity: request.RepositoryEffectIdentity,
		BudgetReservationIdentity: request.BudgetReservationID,
		RuleIDs:                   append([]string{}, request.RuleIDs...), IssuedAt: request.IssuedAt,
		ExpiresAt: request.ExpiresAt, UpdatedAtUnix: record.UpdatedAtUnix,
	}
}

func approvalFailureStatus(err error) actionapproval.Status {
	switch approvalReason(err) {
	case action.ReasonApprovalExpired:
		return actionapproval.StatusExpired
	case action.ReasonAuthorityUnavailable:
		return actionapproval.StatusUnavailable
	case action.ReasonCancelled:
		return actionapproval.StatusCancelled
	default:
		return actionapproval.StatusMalformed
	}
}

func approvalReason(err error) action.ReasonCode {
	var approvalErr *actionapproval.ApprovalError
	if errors.As(err, &approvalErr) && approvalErr.Code.Valid() {
		return approvalErr.Code
	}
	return action.ReasonApprovalInvalid
}

func finalApprovalFailureStatus(status actionapproval.Status) bool {
	switch status {
	case actionapproval.StatusExpired, actionapproval.StatusCancelled,
		actionapproval.StatusUnavailable, actionapproval.StatusMalformed,
		actionapproval.StatusReplayed:
		return true
	default:
		return false
	}
}

func (b ApprovalBinding) reserveRequest() ReserveRequest {
	return ReserveRequest{
		Plan: b.Plan, Request: b.Evaluation.Request, Context: b.Context,
		Authority: b.Authority, Server: b.Server,
	}
}

func (b ApprovalBinding) budgetReserveRequest() ReserveRequest {
	request := b.Evaluation.Request
	if request.Phase == action.PhasePostResult {
		request = b.BudgetRequest
	}
	return ReserveRequest{
		Plan: b.Plan, Request: request, Context: b.Context,
		Authority: b.Authority, Server: b.Server,
	}
}

func credentialLabels(credentials []CredentialBinding) []string {
	labels := make([]string, len(credentials))
	for index, credential := range credentials {
		labels[index] = credential.Label
	}
	return labels
}

func repositoryEffectIdentity(input action.EvaluationInput) string {
	if input.RepositoryEffect == nil {
		return "absent"
	}
	return input.RepositoryEffect.Identity
}

func equalStringSlices(left, right []string) bool {
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

func approvalRecordForCallPhase(
	records []ApprovalRecord,
	callID string,
	phase action.Phase,
) *ApprovalRecord {
	for index := range records {
		if records[index].Request.CallID == callID && records[index].Request.Phase == phase {
			return &records[index]
		}
	}
	return nil
}

func approvalRecordIndex(records []ApprovalRecord, requestID string) int {
	index := sort.Search(len(records), func(index int) bool {
		return records[index].Request.RequestID >= requestID
	})
	if index == len(records) || records[index].Request.RequestID != requestID {
		return -1
	}
	return index
}

func pendingApprovalCount(records []ApprovalRecord) int {
	count := 0
	for _, record := range records {
		if record.Status == actionapproval.StatusPending {
			count++
		}
	}
	return count
}

func approvalReceiptExists(records []ApprovalRecord, receiptID string, except int) bool {
	for index, record := range records {
		if index != except && record.ReceiptID == receiptID {
			return true
		}
	}
	return false
}

func approvalContractError(message string, err error) error {
	return stateError(action.ReasonApprovalInvalid, fmt.Sprintf("approval contract: %s", message), err)
}
