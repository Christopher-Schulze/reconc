package hooks

import "sort"

// VerificationSurface is one exact host execution surface in the shared
// offline/live verification matrix. It is registry-derived and contains no
// observed-host claim.
type VerificationSurface struct {
	Kind           string   `json:"kind"`
	Surface        string   `json:"surface"`
	ExpectedEvents []string `json:"expected_events"`
	Inferred       bool     `json:"inferred"`
	Action         string   `json:"action"`
}

// VerificationSurfaces returns the canonical host/surface matrix used by the
// product verifier and the developer probe wrapper.
func VerificationSurfaces() []VerificationSurface {
	surfaces := make([]VerificationSurface, 0, len(platformRegistry)+7)
	for _, platform := range Platforms() {
		events := platformRuntimeEvents(platform)
		inferred := platformHasInferredCapability(platform)
		if platform.Kind == KindCursor {
			for surface, surfaceEvents := range platformSurfaceEvents(platform) {
				surfaces = append(surfaces, verificationSurface(platform.Kind, string(surface), surfaceEvents, inferred))
			}
			continue
		}
		for _, surface := range verificationSurfaceNames(platform.Kind) {
			surfaces = append(surfaces, verificationSurface(platform.Kind, surface, events, inferred))
		}
	}
	sort.Slice(surfaces, func(i, j int) bool {
		if surfaces[i].Kind == surfaces[j].Kind {
			return surfaces[i].Surface < surfaces[j].Surface
		}
		return surfaces[i].Kind < surfaces[j].Kind
	})
	return surfaces
}

// VerificationSurfaceFor resolves one exact shared matrix entry.
func VerificationSurfaceFor(kind, surface string) (VerificationSurface, bool) {
	for _, candidate := range VerificationSurfaces() {
		if candidate.Kind == kind && candidate.Surface == surface {
			return candidate, true
		}
	}
	return VerificationSurface{}, false
}

// RuntimeEventFor returns the first non-compatibility route for one neutral
// lifecycle. It is the deterministic synthetic-probe route for that platform.
func RuntimeEventFor(kind string, event Event) (string, bool) {
	platform, ok := PlatformForKind(kind)
	if !ok {
		return "", false
	}
	for _, capability := range platform.Capabilities {
		if capability.Event != event || capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if !binding.Compatibility && binding.RuntimeEvent != "" {
				return binding.RuntimeEvent, true
			}
		}
	}
	return "", false
}

func verificationSurface(kind, surface string, events []string, inferred bool) VerificationSurface {
	return VerificationSurface{
		Kind: kind, Surface: surface, ExpectedEvents: append([]string(nil), events...),
		Inferred: inferred, Action: verificationSurfaceAction(kind, surface),
	}
}

func verificationSurfaceNames(kind string) []string {
	switch kind {
	case KindGitPreCommit:
		return []string{"pre-commit"}
	case KindKilo:
		return []string{"cli", "vscode"}
	case KindGrok:
		return []string{"tui"}
	default:
		return []string{"cli"}
	}
}

func platformHasInferredCapability(platform Platform) bool {
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportInferred {
			return true
		}
	}
	return false
}

func verificationSurfaceAction(kind, surface string) string {
	switch kind + ":" + surface {
	case KindCursor + ":" + string(HostSurfaceCursorDesktopAgent):
		return "Open the disposable repository in Cursor Agent and exercise the documented positive, negative, MCP, subagent, compaction, and Stop routes."
	case KindCursor + ":" + string(HostSurfaceCursorDesktopCmdK):
		return "Open the disposable repository in Cursor Cmd+K and exercise only the documented Cmd+K routes."
	case KindCursor + ":" + string(HostSurfaceCursorTab):
		return "Open the disposable repository in Cursor and accept one Tab edit."
	case KindCursor + ":" + string(HostSurfaceCursorCloud):
		return "Start an approved Cursor cloud-agent run for the disposable repository and exercise the documented cloud routes."
	case KindKilo + ":vscode":
		return "Open the disposable repository in Kilo Code's VS Code host and exercise the documented project-plugin routes."
	case KindGitPreCommit + ":pre-commit":
		return "Stage a disposable denied change and attempt a commit without bypassing hooks."
	default:
		return "Start " + kind + " in the disposable repository and exercise its documented positive, negative, compaction, and Stop routes."
	}
}
