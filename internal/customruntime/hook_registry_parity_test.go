package customruntime

import (
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestReservedRuntimeNamesMatchBuiltInRegistry(t *testing.T) {
	want := hooks.SupportedKinds()
	if len(reservedRuntimeNames) != len(want) {
		t.Fatalf("reserved runtime names = %d, built-in registry = %d", len(reservedRuntimeNames), len(want))
	}
	for _, kind := range want {
		if _, reserved := reservedRuntimeNames[kind]; !reserved {
			t.Errorf("built-in runtime %q is not reserved from custom manifests", kind)
		}
	}
}
