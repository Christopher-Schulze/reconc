package impactlab

import (
	"fmt"
	"slices"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
)

// LedgerAssertionForPhase returns the exact privacy-bounded recording contract
// for one compiled action phase.
func LedgerAssertionForPhase(
	phase action.Phase,
	policy *action.LedgerPolicy,
) (*ActionLedgerAssertion, error) {
	if policy == nil {
		return nil, fmt.Errorf("compiled action plan has no ledger policy")
	}
	assertion := &ActionLedgerAssertion{
		Mode: policy.Mode, Required: policy.Mode == action.LedgerRequired,
		ToolIdentity:   policy.ToolIdentity,
		SelectedFields: append([]action.LedgerField{}, policy.SelectedFields...),
	}
	if policy.Mode != action.LedgerOff {
		assertion.Event = ledgerEventForPhase(phase)
	}
	if err := validateActionLedgerAssertion(phase, assertion); err != nil {
		return nil, err
	}
	return assertion, nil
}

func ledgerEventForPhase(phase action.Phase) actionledger.EventType {
	switch phase {
	case action.PhasePreCall, action.PhaseProgress, action.PhaseObservation:
		return actionledger.EventPreDecision
	case action.PhasePostResult:
		return actionledger.EventResultInspection
	default:
		return ""
	}
}

func validateActionLedgerAssertion(phase action.Phase, assertion *ActionLedgerAssertion) error {
	if assertion == nil {
		return nil
	}
	if !assertion.Mode.Valid() || !assertion.ToolIdentity.Valid() || assertion.SelectedFields == nil ||
		len(assertion.SelectedFields) > action.MaxLedgerFields ||
		assertion.Required != (assertion.Mode == action.LedgerRequired) {
		return fmt.Errorf("ledger assertion policy is invalid")
	}
	if assertion.Mode == action.LedgerOff && assertion.Event != "" {
		return fmt.Errorf("disabled ledger assertion must not require an event")
	}
	if assertion.Mode != action.LedgerOff && assertion.Event == "" {
		return fmt.Errorf("enabled ledger assertion requires a lifecycle event")
	}
	wantEvent := ledgerEventForPhase(phase)
	if phase == "" {
		if assertion.Event != "" && assertion.Event != actionledger.EventPreDecision &&
			assertion.Event != actionledger.EventResultInspection {
			return fmt.Errorf("ledger assertion event is invalid")
		}
	} else if assertion.Mode != action.LedgerOff && assertion.Event != wantEvent {
		return fmt.Errorf("ledger assertion event does not match action phase")
	}
	for index, field := range assertion.SelectedFields {
		if field.Source != action.SourceArguments && field.Source != action.SourceResult {
			return fmt.Errorf("ledger assertion selected field source is invalid")
		}
		if _, err := action.CompilePointer(field.Pointer); err != nil {
			return fmt.Errorf("ledger assertion selected field pointer: %w", err)
		}
		if index > 0 && ledgerFieldLess(field, assertion.SelectedFields[index-1]) {
			return fmt.Errorf("ledger assertion selected fields are not canonical")
		}
		if index > 0 && field == assertion.SelectedFields[index-1] {
			return fmt.Errorf("ledger assertion selected fields contain duplicates")
		}
	}
	return nil
}

func ledgerFieldLess(left, right action.LedgerField) bool {
	if left.Source != right.Source {
		return left.Source < right.Source
	}
	return left.Pointer < right.Pointer
}

func equalActionLedgerAssertion(left, right *ActionLedgerAssertion) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Mode == right.Mode && left.Event == right.Event && left.Required == right.Required &&
		left.ToolIdentity == right.ToolIdentity && slices.Equal(left.SelectedFields, right.SelectedFields)
}

func cloneActionLedgerAssertion(input *ActionLedgerAssertion) *ActionLedgerAssertion {
	if input == nil {
		return nil
	}
	out := *input
	out.SelectedFields = append([]action.LedgerField{}, input.SelectedFields...)
	return &out
}
