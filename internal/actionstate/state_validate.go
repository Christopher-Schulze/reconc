package actionstate

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

const MaxGenerationHistory = 256

func (s *Store) stateDigest(state State) (string, error) {
	payload := statePayload{
		Schema: state.Schema, FormatVersion: state.FormatVersion,
		KeyID: state.KeyID, RepositoryIdentity: state.RepositoryIdentity,
		Revision: state.Revision, ClockSource: state.ClockSource,
		LastObservedUnixNano: state.LastObservedUnixNano,
		Budgets:              state.Budgets, Reservations: state.Reservations,
		TerminalCalls: state.TerminalCalls, Approvals: state.Approvals,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", stateError(action.ReasonStateCorrupt, "encode action state identity", err)
	}
	return s.key.Identity(DomainState, body), nil
}

func (s *Store) transactionDigest(transaction stateTransaction) (string, error) {
	payload := transactionPayload{
		Schema: transaction.Schema, FormatVersion: transaction.FormatVersion,
		BeforePersisted: transaction.BeforePersisted,
		BeforeRevision:  transaction.BeforeRevision,
		BeforeDigest:    transaction.BeforeDigest, After: transaction.After,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", stateError(action.ReasonStateCorrupt, "encode action transaction identity", err)
	}
	return s.key.Identity(DomainTransaction, body), nil
}

func (s *Store) validateState(state State, persisted bool) error {
	if state.Schema != StateSchema || state.FormatVersion != StateFormatVersion {
		return stateError(action.ReasonStateCorrupt, "action state schema metadata is invalid", nil)
	}
	if state.KeyID != s.key.ID() || !identityUsesKey(state.RepositoryIdentity, state.KeyID) {
		return stateError(action.ReasonStateCorrupt, "action state key binding is invalid", nil)
	}
	if state.RepositoryIdentity != s.repositoryIdentity {
		return stateError(action.ReasonStateCorrupt, "action state belongs to a different repository identity", nil)
	}
	if state.Budgets == nil || state.Reservations == nil || state.TerminalCalls == nil || state.Approvals == nil {
		return stateError(action.ReasonStateCorrupt, "action state collections must be explicit arrays", nil)
	}
	if len(state.Budgets) > MaxBudgetRecords || len(state.Reservations) > MaxReservations ||
		len(state.TerminalCalls) > MaxTerminalCallRecords || len(state.Approvals) > MaxApprovalRecords {
		return stateError(action.ReasonStateCorrupt, "action state collection capacity is exceeded", nil)
	}
	if persisted && (state.Revision == 0 || !action.SafeLabel(state.ClockSource) || state.LastObservedUnixNano <= 0) {
		return stateError(action.ReasonStateCorrupt, "persisted action state clock or revision is invalid", nil)
	}
	if !persisted && state.Revision != 0 {
		return stateError(action.ReasonStateCorrupt, "unpersisted action state has a revision", nil)
	}
	if err := s.validateBudgetRecords(state); err != nil {
		return err
	}
	if err := s.validateReservations(state); err != nil {
		return err
	}
	if err := s.validateTerminalCalls(state); err != nil {
		return err
	}
	if err := s.validateApprovalRecords(state); err != nil {
		return err
	}
	digest, err := s.stateDigest(state)
	if err != nil || !constantIdentityEqual(digest, state.Digest) {
		return stateError(action.ReasonStateCorrupt, "action state digest is invalid", err)
	}
	return nil
}

func (s *Store) validateBudgetRecords(state State) error {
	for index, record := range state.Budgets {
		if index > 0 && state.Budgets[index-1].LineageIdentity >= record.LineageIdentity {
			return stateError(action.ReasonStateCorrupt, "budget records are not unique and sorted", nil)
		}
		if !action.SafeLabel(record.BudgetID) ||
			!identityUsesKey(record.ScopeIdentity, state.KeyID) ||
			!identityUsesKey(record.LineageIdentity, state.KeyID) ||
			record.ScopeIdentity == record.LineageIdentity || !record.Reset.Valid() ||
			record.Limits.Empty() || !budgetLimitsBounded(record.Limits) ||
			len(record.GenerationHistory) == 0 || len(record.GenerationHistory) > MaxGenerationHistory ||
			record.GenerationHistory == nil || record.Consumed.Concurrent != 0 {
			return stateError(action.ReasonStateCorrupt, "budget record contract is invalid", nil)
		}
		if err := validateStoredScope(record, state); err != nil {
			return err
		}
		if err := validateGeneration(record.Generation, state.KeyID); err != nil {
			return err
		}
		for historyIndex, generation := range record.GenerationHistory {
			if err := validateGeneration(generation, state.KeyID); err != nil {
				return err
			}
			if historyIndex > 0 && generation == record.GenerationHistory[historyIndex-1] {
				return stateError(action.ReasonStateCorrupt, "budget generation history contains a duplicate transition", nil)
			}
		}
		if record.GenerationHistory[len(record.GenerationHistory)-1] != record.Generation {
			return stateError(action.ReasonStateCorrupt, "budget generation history does not end at the active generation", nil)
		}
	}
	return nil
}

func validateStoredScope(record BudgetRecord, state State) error {
	scope := record.Scope
	credentials := append([]string(nil), scope.CredentialLabels...)
	if credentials == nil || !sort.StringsAreSorted(credentials) || hasDuplicateStrings(credentials) ||
		scope.RepositoryIdentity != state.RepositoryIdentity || !action.SafeLabel(scope.Principal) ||
		!action.SafeLabel(scope.ServerLabel) || !action.SafeLabel(scope.ToolID) ||
		!identityUsesKey(scope.RepositoryIdentity, state.KeyID) ||
		!identityUsesKey(scope.ServerIdentity, state.KeyID) {
		return stateError(action.ReasonStateCorrupt, "budget scope identity is invalid", nil)
	}
	for _, credential := range credentials {
		if !action.SafeLabel(credential) {
			return stateError(action.ReasonStateCorrupt, "budget credential scope is invalid", nil)
		}
	}
	wantRun, wantSession, wantWindow := "absent", "absent", "absent"
	switch record.Reset {
	case action.BudgetResetOperatorRun:
		if !identityUsesKey(scope.RunIdentity, state.KeyID) {
			return stateError(action.ReasonStateCorrupt, "run-reset budget identity is invalid", nil)
		}
		wantRun = scope.RunIdentity
	case action.BudgetResetOperatorSession:
		if !identityUsesKey(scope.SessionIdentity, state.KeyID) {
			return stateError(action.ReasonStateCorrupt, "session-reset budget identity is invalid", nil)
		}
		wantSession = scope.SessionIdentity
	case action.BudgetResetFixedWindow:
		if record.WindowSeconds == 0 || record.WindowSeconds > 86400 ||
			record.Limits.RateWindow == 0 && record.WindowSeconds == 0 ||
			!identityUsesKey(scope.WindowIdentity, state.KeyID) || scope.WindowStartUnix < 0 ||
			scope.WindowStartUnix%int64(record.WindowSeconds) != 0 {
			return stateError(action.ReasonStateCorrupt, "fixed-window budget identity is invalid", nil)
		}
		wantWindow = scope.WindowIdentity
	default:
		if record.WindowSeconds != 0 || record.Limits.RateWindow != 0 {
			return stateError(action.ReasonStateCorrupt, "non-window budget contains window state", nil)
		}
	}
	if scope.RunIdentity != wantRun || scope.SessionIdentity != wantSession ||
		scope.WindowIdentity != wantWindow ||
		record.Reset != action.BudgetResetFixedWindow && scope.WindowStartUnix != 0 {
		return stateError(action.ReasonStateCorrupt, "budget scope reset components are invalid", nil)
	}
	return nil
}

func (s *Store) validateReservations(state State) error {
	records := make(map[string]BudgetRecord, len(state.Budgets))
	for _, record := range state.Budgets {
		records[record.LineageIdentity] = record
	}
	activeCalls := make(map[string]bool, len(state.Reservations))
	for index, reservation := range state.Reservations {
		if index > 0 && state.Reservations[index-1].Identity >= reservation.Identity ||
			!identityUsesKey(reservation.Identity, state.KeyID) || !validCallID(reservation.CallID) ||
			!validOpaqueStateIdentity(reservation.OwnerID) || !reservation.Status.Valid() ||
			!identityUsesKey(reservation.RequestIdentity, state.KeyID) ||
			!identityUsesKey(reservation.ContextIdentity, state.KeyID) ||
			!action.ValidSHA256Identity(reservation.ExecutableDigest) ||
			reservation.CreatedAtUnix <= 0 || reservation.UpdatedAtUnix < reservation.CreatedAtUnix ||
			len(reservation.Charges) == 0 || activeCalls[reservation.CallID] {
			return stateError(action.ReasonStateCorrupt, "action reservation is invalid", nil)
		}
		activeCalls[reservation.CallID] = true
		for chargeIndex, charge := range reservation.Charges {
			record, exists := records[charge.LineageIdentity]
			if !exists || record.BudgetID != charge.BudgetID ||
				!identityUsesKey(charge.ScopeIdentity, state.KeyID) ||
				chargeIndex > 0 && reservation.Charges[chargeIndex-1].LineageIdentity >= charge.LineageIdentity {
				return stateError(action.ReasonStateCorrupt, "reservation charge is invalid", nil)
			}
			if err := validateGeneration(charge.Generation, state.KeyID); err != nil {
				return err
			}
			if err := validateReservationCharge(reservation.Status, charge, record); err != nil {
				return err
			}
		}
		if state.LastObservedUnixNano > 0 &&
			reservation.UpdatedAtUnix > state.LastObservedUnixNano/int64(time.Second) {
			return stateError(action.ReasonStateCorrupt, "reservation timestamp exceeds the trusted clock", nil)
		}
	}
	if _, err := reservedUsageByLineage(state); err != nil {
		return err
	}
	return nil
}

func (s *Store) validateTerminalCalls(state State) error {
	active := make(map[string]bool, len(state.Reservations))
	for _, reservation := range state.Reservations {
		active[reservation.CallID] = true
	}
	reservations := make(map[string]bool, len(state.TerminalCalls))
	for index, call := range state.TerminalCalls {
		if index > 0 && state.TerminalCalls[index-1].CallID >= call.CallID ||
			!validCallID(call.CallID) || !identityUsesKey(call.ReservationIdentity, state.KeyID) ||
			!call.Outcome.Valid() || call.CompletedAtUnix <= 0 || active[call.CallID] ||
			reservations[call.ReservationIdentity] ||
			state.LastObservedUnixNano > 0 && call.CompletedAtUnix > state.LastObservedUnixNano/int64(time.Second) {
			return stateError(action.ReasonStateCorrupt, "terminal action call is invalid", nil)
		}
		reservations[call.ReservationIdentity] = true
	}
	return nil
}

func (s *Store) validateApprovalRecords(state State) error {
	calls := make(map[string]bool, len(state.Approvals))
	nonces := make(map[string]bool, len(state.Approvals))
	receipts := make(map[string]bool, len(state.Approvals))
	reservations := make(map[string]Reservation, len(state.Reservations))
	for _, reservation := range state.Reservations {
		reservations[reservation.Identity] = reservation
	}
	terminals := make(map[string]TerminalCall, len(state.TerminalCalls))
	for _, terminal := range state.TerminalCalls {
		terminals[terminal.ReservationIdentity] = terminal
	}
	budgets := make(map[string]BudgetRecord, len(state.Budgets))
	for _, budget := range state.Budgets {
		budgets[budget.LineageIdentity] = budget
	}
	pending := 0
	for index, record := range state.Approvals {
		request := record.Request
		if index > 0 && state.Approvals[index-1].Request.RequestID >= request.RequestID ||
			request.Validate() != nil || request.RepositoryIdentity != state.RepositoryIdentity ||
			!identityUsesKey(request.RequestIdentity, state.KeyID) ||
			!identityUsesKey(request.StateVersion, state.KeyID) ||
			record.ReservationIdentity != request.BudgetReservationID ||
			(record.ReservationIdentity != "absent" && !identityUsesKey(record.ReservationIdentity, state.KeyID)) ||
			!identityUsesKey(record.NonceIdentity, state.KeyID) ||
			!record.Status.Valid() || record.UpdatedAtUnix <= 0 || calls[request.CallID] || nonces[record.NonceIdentity] ||
			state.LastObservedUnixNano > 0 && record.UpdatedAtUnix > state.LastObservedUnixNano/int64(time.Second) {
			return stateError(action.ReasonStateCorrupt, "approval replay record is invalid", nil)
		}
		for _, selected := range request.SelectedArguments {
			if !identityUsesKey(selected.Identity, state.KeyID) {
				return stateError(action.ReasonStateCorrupt, "selected approval identity uses a different key", nil)
			}
		}
		nonce, err := base64.RawURLEncoding.Strict().DecodeString(request.Nonce)
		if err != nil || !constantIdentityEqual(
			record.NonceIdentity,
			s.key.Identity(DomainApproval, []byte("nonce"), nonce),
		) {
			return stateError(action.ReasonStateCorrupt, "approval nonce identity is invalid", err)
		}
		calls[request.CallID] = true
		nonces[record.NonceIdentity] = true
		if record.Status == actionapproval.StatusPending {
			pending++
		}
		metadataEmpty := record.RegistryIdentity == "" && record.AuthorityKeyID == "" &&
			record.ReceiptID == "" && record.ReceiptIdentity == "" && record.ReceiptSignedAt == ""
		metadataValid := action.ValidSHA256Identity(record.RegistryIdentity) &&
			action.SafeLabel(record.AuthorityKeyID) && validApprovalReceiptID(record.ReceiptID) &&
			action.ValidSHA256Identity(record.ReceiptIdentity) && !receipts[record.ReceiptID] &&
			validStoredReceiptTime(record)
		if record.Status == actionapproval.StatusApproved || record.Status == actionapproval.StatusRejected {
			if !metadataValid {
				return stateError(action.ReasonStateCorrupt, "verified approval receipt metadata is invalid", nil)
			}
			receipts[record.ReceiptID] = true
		} else if !metadataEmpty {
			return stateError(action.ReasonStateCorrupt, "non-approved replay record contains receipt metadata", nil)
		}
		if err := validateApprovalRecordTransition(record, reservations, terminals, budgets); err != nil {
			return err
		}
	}
	if pending > MaxPendingApprovals {
		return stateError(action.ReasonStateCorrupt, "pending approval capacity is exceeded", nil)
	}
	return nil
}

func validStoredReceiptTime(record ApprovalRecord) bool {
	signedAt, err := time.Parse(time.RFC3339Nano, record.ReceiptSignedAt)
	if err != nil || signedAt.IsZero() || signedAt.UTC().Format(time.RFC3339Nano) != record.ReceiptSignedAt {
		return false
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, record.Request.IssuedAt)
	if err != nil {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, record.Request.ExpiresAt)
	return err == nil && !signedAt.Before(issuedAt) && signedAt.Before(expiresAt)
}

func validateApprovalRecordTransition(
	record ApprovalRecord,
	reservations map[string]Reservation,
	terminals map[string]TerminalCall,
	budgets map[string]BudgetRecord,
) error {
	if record.ReservationIdentity == "absent" {
		return nil
	}
	reservation, active := reservations[record.ReservationIdentity]
	terminal, completed := terminals[record.ReservationIdentity]
	switch record.Status {
	case actionapproval.StatusPending:
		if !active || completed || reservation.Status != ReservationReserved {
			return stateError(action.ReasonStateCorrupt, "pending approval reservation state is invalid", nil)
		}
		if err := validateApprovalReservationBinding(record, reservation); err != nil {
			return err
		}
		return validateApprovalChargeState(reservation, budgets, false)
	case actionapproval.StatusApproved:
		if active == completed {
			return stateError(action.ReasonStateCorrupt, "approved call must have exactly one active or terminal reservation", nil)
		}
		if completed {
			if terminal.CallID != record.Request.CallID {
				return stateError(action.ReasonStateCorrupt, "approved terminal call binding is invalid", nil)
			}
			return nil
		}
		if err := validateApprovalReservationBinding(record, reservation); err != nil {
			return err
		}
		return validateApprovalChargeState(reservation, budgets, true)
	default:
		if active || !completed || terminal.CallID != record.Request.CallID || terminal.Outcome != OutcomeBlocked {
			return stateError(action.ReasonStateCorrupt, "failed approval terminal state is invalid", nil)
		}
		return nil
	}
}

func validateApprovalReservationBinding(record ApprovalRecord, reservation Reservation) error {
	if reservation.CallID != record.Request.CallID ||
		reservation.RequestIdentity != record.Request.RequestIdentity ||
		reservation.ContextIdentity != record.Request.ContextIdentity ||
		reservation.ExecutableDigest != record.Request.ExecutableDigest {
		return stateError(action.ReasonStateCorrupt, "approval reservation binding is invalid", nil)
	}
	return nil
}

func validateApprovalChargeState(
	reservation Reservation,
	budgets map[string]BudgetRecord,
	committed bool,
) error {
	for _, charge := range reservation.Charges {
		record, exists := budgets[charge.LineageIdentity]
		if !exists {
			return stateError(action.ReasonStateCorrupt, "approval charge budget is absent", nil)
		}
		if record.Limits.ApprovalCount == 0 {
			continue
		}
		if charge.ApprovalCommitted != committed || committed && charge.Reserved.ApprovalCount != 0 ||
			!committed && charge.Reserved.ApprovalCount != 1 {
			return stateError(action.ReasonStateCorrupt, "approval charge transition is invalid", nil)
		}
	}
	return nil
}

func validApprovalReceiptID(value string) bool {
	if len(value) != 30 || !strings.HasPrefix(value, "arc_") {
		return false
	}
	for _, character := range value[4:] {
		if character < 'a' || character > 'z' {
			if character < '2' || character > '7' {
				return false
			}
		}
	}
	return true
}

func validateReservationCharge(status ReservationStatus, charge ReservationCharge, record BudgetRecord) error {
	usage := charge.Reserved
	if usage.DeniedCount != 0 || usage.CallCount > 1 || usage.ApprovalCount > 1 ||
		usage.Concurrent > 1 || usage.RateWindow > 1 ||
		!reservedDimensionValid(usage.CallCount, record.Limits.CallCount) ||
		!reservedDimensionValid(usage.ApprovalCount, record.Limits.ApprovalCount) ||
		!reservedDimensionValid(usage.ArgumentBytes, record.Limits.ArgumentBytes) ||
		!reservedDimensionValid(usage.ResultBytes, record.Limits.ResultBytes) ||
		!reservedDimensionValid(usage.CostUnits, record.Limits.CostUnits) ||
		!reservedDimensionValid(usage.Concurrent, record.Limits.Concurrent) ||
		!reservedDimensionValid(usage.RateWindow, record.Limits.RateWindow) {
		return stateError(action.ReasonStateCorrupt, "reservation charge exceeds its budget contract", nil)
	}
	if charge.ApprovalCommitted && (record.Limits.ApprovalCount == 0 || usage.ApprovalCount != 0) {
		return stateError(action.ReasonStateCorrupt, "committed approval charge is invalid", nil)
	}
	if status == ReservationReserved && charge.DispatchCommitted {
		return stateError(action.ReasonStateCorrupt, "pre-dispatch reservation contains a dispatch commit", nil)
	}
	if status != ReservationReserved && !charge.DispatchCommitted {
		return stateError(action.ReasonStateCorrupt, "post-dispatch reservation lacks a dispatch commit", nil)
	}
	if charge.DispatchCommitted &&
		(usage.CallCount != 0 || usage.ApprovalCount != 0 || usage.ArgumentBytes != 0 ||
			usage.CostUnits != 0 || usage.RateWindow != 0) {
		return stateError(action.ReasonStateCorrupt, "post-dispatch reservation retains committed capacity", nil)
	}
	return nil
}

func reservedDimensionValid(value, limit uint64) bool {
	if limit == 0 {
		return value == 0
	}
	return value <= limit
}

func validateGeneration(generation action.BudgetGeneration, keyID string) error {
	if !validLowerHex(generation.PolicyDigest, 64) ||
		!action.ValidSHA256Identity(generation.ExecutableDigest) ||
		!action.ValidSHA256Identity(generation.ToolContractDigest) || generation.KeyID != keyID {
		return stateError(action.ReasonStateCorrupt, "budget governing generation is invalid", nil)
	}
	return nil
}

func budgetLimitsBounded(limits action.BudgetLimits) bool {
	values := []uint64{
		limits.CallCount, limits.DeniedCount, limits.ApprovalCount, limits.ArgumentBytes,
		limits.ResultBytes, limits.CostUnits, limits.Concurrent, limits.RateWindow,
	}
	for _, value := range values {
		if value > math.MaxInt64 {
			return false
		}
	}
	return limits.Concurrent <= action.MaxConcurrentCalls
}

func identityUsesKey(identity, keyID string) bool {
	prefix := "hmac-sha256:v1:" + keyID + ":"
	return action.ValidKeyedIdentity(identity) && strings.HasPrefix(identity, prefix)
}

func constantIdentityEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func validOpaqueStateIdentity(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validCallID(value string) bool {
	if len(value) != 30 || !strings.HasPrefix(value, "act_") {
		return false
	}
	for _, character := range value[4:] {
		if character < 'a' || character > 'z' {
			if character < '2' || character > '7' {
				return false
			}
		}
	}
	return true
}

func hasDuplicateStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}
