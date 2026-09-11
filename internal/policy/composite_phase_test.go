package policy

import "testing"

func TestValidateCompositeCommandPhase(t *testing.T) {
	if err := ValidateCompositeCommandPhase(KindNot, []Check{{Kind: KindForbidCommand, Commands: []string{"git"}}}); err != nil {
		t.Fatalf("pure not { forbid_command } rejected: %v", err)
	}
	if err := ValidateCompositeCommandPhase(KindAllOf, []Check{{Kind: KindForbidCommand}, {Kind: KindRequireClaim}}); err != nil {
		t.Fatalf("mixed all_of rejected: %v", err)
	}
	if err := ValidateCompositeCommandPhase(KindNot, nil); err == nil {
		t.Fatal("empty not admitted")
	}
	if err := ValidateCompositeCommandPhase(KindNot, []Check{{Kind: KindNot}}); err == nil {
		t.Fatal("nested not admitted")
	}
}
