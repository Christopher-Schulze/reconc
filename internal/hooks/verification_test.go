package hooks

import (
	"strings"
	"testing"
)

func TestVerificationSurfacesCoverEveryPlatformAndCursorSurface(t *testing.T) {
	surfaces := VerificationSurfaces()
	covered := map[string]int{}
	for _, surface := range surfaces {
		covered[surface.Kind]++
		if surface.Surface == "" || surface.Action == "" {
			t.Fatalf("incomplete verification surface: %+v", surface)
		}
		if surface.Kind != KindGitPreCommit && len(surface.ExpectedEvents) == 0 {
			t.Fatalf("agent surface has no expected routes: %+v", surface)
		}
	}
	for _, kind := range SupportedKinds() {
		if covered[kind] == 0 {
			t.Fatalf("platform %s has no verification surface", kind)
		}
	}
	if covered[KindCursor] != 6 || covered[KindKilo] != 2 {
		t.Fatalf("surface counts: cursor=%d kilo=%d", covered[KindCursor], covered[KindKilo])
	}
}

func TestRuntimeEventForUsesFirstClassPreToolRoute(t *testing.T) {
	for _, platform := range AgentPlatforms() {
		route, ok := RuntimeEventFor(platform.Kind, EventPreToolUse)
		if !ok || route == "" {
			t.Fatalf("%s has no first-class synthetic pre-tool route", platform.Kind)
		}
		resolved, ok := RuntimeEvent(route)
		if !ok || resolved.PlatformKind != platform.Kind || resolved.Event != EventPreToolUse {
			t.Fatalf("%s route %q resolved to %+v, %t", platform.Kind, route, resolved, ok)
		}
	}
}

func TestGeneratedArtifactsThatInvokeWrapperDeclareWrapperRequirement(t *testing.T) {
	for _, platform := range Platforms() {
		artifact, err := Generate(platform.Kind)
		if err != nil {
			t.Fatalf("generate %s: %v", platform.Kind, err)
		}
		invokesWrapper := strings.Contains(artifact.Content, WrapperPath)
		if invokesWrapper != platform.Activation.RequiresWrapper {
			t.Fatalf("%s invokes wrapper=%t but registry requires_wrapper=%t", platform.Kind, invokesWrapper, platform.Activation.RequiresWrapper)
		}
	}
}
