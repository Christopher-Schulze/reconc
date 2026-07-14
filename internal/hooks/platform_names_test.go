package hooks

import "testing"

func TestKiloUsesCurrentProductName(t *testing.T) {
	platform, ok := PlatformForKind(KindKilo)
	if !ok {
		t.Fatal("Kilo Code platform is not registered")
	}
	if platform.DisplayName != "Kilo Code" {
		t.Fatalf("Kilo display name = %q, want Kilo Code", platform.DisplayName)
	}
}
