package cievidence

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
)

func validateStatement(statement Statement) error {
	if statement.Schema != Schema || statement.FormatVersion != FormatVersion || !validText(statement.Repository) {
		return fmt.Errorf("CI evidence metadata is invalid")
	}
	if err := validateCandidate(statement.Candidate); err != nil {
		return err
	}
	if statement.IssuedAt <= 0 || statement.ExpiresAt <= statement.IssuedAt {
		return fmt.Errorf("CI evidence validity interval is invalid")
	}
	if len(statement.Checks) == 0 || len(statement.Checks) > MaxChecks {
		return fmt.Errorf("CI evidence must contain 1 to %d checks", MaxChecks)
	}
	for index, check := range statement.Checks {
		if !validText(check.ID) || !validText(check.RunID) || index > 0 && statement.Checks[index-1].ID >= check.ID {
			return fmt.Errorf("CI checks must have valid run identities and unique sorted IDs")
		}
		switch check.Outcome {
		case OutcomeSuccess, OutcomeFailure, OutcomePending, OutcomeCancelled:
		default:
			return fmt.Errorf("CI check outcome is invalid")
		}
	}
	return nil
}

func validateCandidate(candidate Candidate) error {
	if candidate.Kind != CandidateCommit && candidate.Kind != CandidateMergeQueue {
		return fmt.Errorf("CI candidate kind must be commit or merge-queue")
	}
	if len(candidate.ObjectID) != 40 && len(candidate.ObjectID) != 64 {
		return fmt.Errorf("CI candidate must contain an exact Git object ID")
	}
	nonzero := false
	for _, character := range candidate.ObjectID {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return fmt.Errorf("CI candidate object ID must be lowercase hexadecimal")
			}
		}
		nonzero = nonzero || character != '0'
	}
	if !nonzero {
		return fmt.Errorf("CI candidate cannot be Git's null object ID")
	}
	return nil
}

func validateRequirement(requirement Requirement) error {
	if !validText(requirement.Repository) || !action.SafeLabel(requirement.AuthorityKeyID) ||
		len(requirement.PublicKey) != ed25519.PublicKeySize || requirement.MaxAge <= 0 {
		return fmt.Errorf("trusted CI requirement is incomplete")
	}
	if len(requirement.RequiredChecks) == 0 || len(requirement.RequiredChecks) > MaxChecks {
		return fmt.Errorf("trusted CI requirement must select 1 to %d checks", MaxChecks)
	}
	for index, id := range requirement.RequiredChecks {
		if !validText(id) || index > 0 && requirement.RequiredChecks[index-1] >= id {
			return fmt.Errorf("required CI checks must have unique sorted IDs")
		}
	}
	return nil
}

func validText(value string) bool {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return false
		}
	}
	return true
}
