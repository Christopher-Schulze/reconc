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

func TestOMPUsesOfficialProductName(t *testing.T) {
	platform, ok := PlatformForKind(KindOMP)
	if !ok {
		t.Fatal("Oh My Pi platform is not registered")
	}
	if platform.DisplayName != "Oh My Pi" {
		t.Fatalf("OMP display name = %q, want Oh My Pi", platform.DisplayName)
	}
}
