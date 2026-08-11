package actionledger

import (
	"fmt"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

type SelectedFieldInput struct {
	DeclarationIndex   uint16
	PolicyDigest       string
	LockDigest         string
	ToolContractDigest string
	Source             action.ValueSource
	Pointer            string
	Selected           action.PointerResult
}

// SelectedField derives one policy-bound pointer and value identity under the
// active TASK 157 key lease. The canonical value is used only in-memory as an
// HMAC input and is never retained by the returned type.
func (s *Store) SelectedField(input SelectedFieldInput) (SelectedFieldEvidence, error) {
	evidence := SelectedFieldEvidence{
		DeclarationIndex: input.DeclarationIndex, Source: input.Source, State: input.Selected.State,
	}
	if s == nil {
		return evidence, fmt.Errorf("action ledger store is unavailable")
	}
	if !lowerDigestPattern.MatchString(input.PolicyDigest) || !lowerDigestPattern.MatchString(input.LockDigest) ||
		!action.ValidSHA256Identity(input.ToolContractDigest) ||
		input.DeclarationIndex >= MaxSelectedFields ||
		input.Source != action.SourceArguments && input.Source != action.SourceResult {
		return evidence, fmt.Errorf("selected field policy binding is invalid")
	}
	if _, err := action.CompilePointer(input.Pointer); err != nil {
		return evidence, fmt.Errorf("selected field pointer is invalid: %w", err)
	}
	if !validPointerState(input.Selected.State) {
		return evidence, fmt.Errorf("selected field state is invalid")
	}
	var canonical []byte
	if input.Selected.State == action.PointerPresent || input.Selected.State == action.PointerNull {
		var err error
		canonical, err = input.Selected.Value.MarshalJSON()
		if err != nil {
			return evidence, fmt.Errorf("encode canonical selected field: %w", err)
		}
		if len(canonical) > action.MaxArgumentBytes {
			return evidence, fmt.Errorf("canonical selected field exceeds %d bytes", action.MaxArgumentBytes)
		}
		items, err := countSelectedItems(input.Selected.Value, 0)
		if err != nil {
			return evidence, err
		}
		evidence.Kind = input.Selected.Value.Kind()
		evidence.ByteLength = uint64(len(canonical))
		evidence.ItemCount = items
	}
	err := s.storage.WithIdentity(func(key *actionstate.IdentityKey) error {
		repositoryIdentity := s.storage.RepositoryIdentity()
		evidence.PointerIdentity = key.Identity(
			actionstate.DomainLedger,
			[]byte("selected-pointer"), []byte(repositoryIdentity),
			[]byte(input.PolicyDigest), []byte(input.LockDigest),
			[]byte(input.ToolContractDigest), []byte(fmt.Sprintf("%d", input.DeclarationIndex)),
			[]byte(input.Source), []byte(input.Pointer),
		)
		if canonical != nil {
			evidence.ValueIdentity = key.Identity(
				actionstate.DomainLedger,
				[]byte("selected-value"), []byte(repositoryIdentity),
				[]byte(input.PolicyDigest), []byte(input.LockDigest),
				[]byte(input.ToolContractDigest), []byte(evidence.PointerIdentity),
				[]byte(input.Selected.State), canonical,
			)
		}
		if evidence.PointerIdentity == "" || canonical != nil && evidence.ValueIdentity == "" {
			return fmt.Errorf("selected field identity derivation failed")
		}
		return nil
	})
	if err != nil {
		evidence.PointerIdentity = ""
		evidence.ValueIdentity = ""
		evidence.Complete = false
		return evidence, fmt.Errorf("derive selected field identity: %w", err)
	}
	evidence.Complete = true
	return evidence, nil
}

func countSelectedItems(value action.Value, depth int) (uint32, error) {
	if depth > action.MaxJSONDepth {
		return 0, fmt.Errorf("selected field exceeds depth %d", action.MaxJSONDepth)
	}
	count := uint32(0)
	switch value.Kind() {
	case action.ValueArray:
		items, _ := value.Items()
		for _, item := range items {
			if count >= action.MaxJSONItems {
				return 0, fmt.Errorf("selected field exceeds %d items", action.MaxJSONItems)
			}
			count++
			child, err := countSelectedItems(item, depth+1)
			if err != nil {
				return 0, err
			}
			if child > action.MaxJSONItems-count {
				return 0, fmt.Errorf("selected field exceeds %d items", action.MaxJSONItems)
			}
			count += child
		}
	case action.ValueObject:
		members, _ := value.Members()
		for _, member := range members {
			if count >= action.MaxJSONItems {
				return 0, fmt.Errorf("selected field exceeds %d items", action.MaxJSONItems)
			}
			count++
			child, err := countSelectedItems(member.Value, depth+1)
			if err != nil {
				return 0, err
			}
			if child > action.MaxJSONItems-count {
				return 0, fmt.Errorf("selected field exceeds %d items", action.MaxJSONItems)
			}
			count += child
		}
	}
	return count, nil
}
