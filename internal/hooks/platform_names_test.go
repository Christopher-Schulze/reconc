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

func TestPiUsesOfficialProductName(t *testing.T) {
	platform, ok := PlatformForKind(KindPi)
	if !ok {
		t.Fatal("Pi Coding Agent platform is not registered")
	}
	if platform.DisplayName != "Pi Coding Agent" {
		t.Fatalf("Pi display name = %q, want Pi Coding Agent", platform.DisplayName)
	}
}
