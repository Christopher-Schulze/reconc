package hooks

// CursorEventDisposition is the complete policy-relevant classification of
// one current Cursor hook event. Installed routes are derived from the shared
// platform registry; excluded events stay explicit so generator omissions
// cannot be mistaken for accidental gaps.
type CursorEventDisposition struct {
	NativeEvent   string             `json:"native_event"`
	Install       bool               `json:"install"`
	Event         Event              `json:"event,omitempty"`
	Support       SupportMode        `json:"support"`
	ErrorPolicy   FailurePolicy      `json:"error_policy"`
	TimeoutPolicy FailurePolicy      `json:"timeout_policy"`
	ResponseMode  CursorResponseMode `json:"response_mode,omitempty"`
	Surfaces      []HostSurface      `json:"surfaces"`
	Evidence      string             `json:"evidence"`
	Limitation    string             `json:"limitation,omitempty"`
}

var cursorCurrentEvents = []string{
	"sessionStart",
	"sessionEnd",
	"preToolUse",
	"postToolUse",
	"postToolUseFailure",
	"subagentStart",
	"subagentStop",
	"beforeShellExecution",
	"afterShellExecution",
	"beforeMCPExecution",
	"afterMCPExecution",
	"beforeReadFile",
	"afterFileEdit",
	"beforeSubmitPrompt",
	"preCompact",
	"stop",
	"afterAgentResponse",
	"afterAgentThought",
	"beforeTabFileRead",
	"afterTabFileEdit",
	"workspaceOpen",
}

var cursorEvidenceSemantics = map[string]string{
	"sessionStart":         "session lifecycle and route liveness only",
	"sessionEnd":           "session cleanup and route liveness only",
	"preToolUse":           "blocking repository-write policy",
	"postToolUse":          "authoritative successful read, write, or shell evidence",
	"postToolUseFailure":   "authoritative failure; never positive evidence",
	"subagentStart":        "native child-session decision and bounded lifecycle",
	"subagentStop":         "child-session completion decision and bounded follow-up",
	"beforeShellExecution": "blocking command policy only",
	"afterShellExecution":  "passive diagnostics and liveness; no command outcome evidence",
	"beforeMCPExecution":   "classified MCP pre-action enforcement",
	"afterMCPExecution":    "classified successful MCP evidence or redacted observation",
	"afterFileEdit":        "authoritative successful write evidence",
	"beforeSubmitPrompt":   "native prompt-submission decision and session liveness",
	"preCompact":           "bounded passive compaction lifecycle only",
	"stop":                 "hard completion decision and bounded follow-up",
	"afterTabFileEdit":     "authoritative successful Tab write evidence",
	"workspaceOpen":        "sessionless desktop and CLI artifact liveness only",
}

var cursorExcludedEvents = map[string]CursorEventDisposition{
	"beforeReadFile": {
		NativeEvent: "beforeReadFile", Support: SupportUnsupported, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost,
		Evidence: "none", Limitation: "Reconc has no deny-read policy and a pre-read event cannot prove a successful read",
	},
	"afterAgentResponse": {
		NativeEvent: "afterAgentResponse", Support: SupportUnsupported, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost,
		Evidence: "none", Limitation: "assistant response capture is privacy-sensitive and non-evidentiary",
	},
	"afterAgentThought": {
		NativeEvent: "afterAgentThought", Support: SupportUnsupported, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost,
		Evidence: "none", Limitation: "thought capture is privacy-sensitive and non-evidentiary",
	},
	"beforeTabFileRead": {
		NativeEvent: "beforeTabFileRead", Support: SupportUnsupported, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost,
		Evidence: "none", Limitation: "Reconc has no deny-read policy and a pre-read event cannot prove a successful read",
	},
}

// CursorEventDispositions returns all current Cursor events exactly once in
// official event order.
func CursorEventDispositions() []CursorEventDisposition {
	installed := map[string]CursorEventDisposition{}
	platform, _ := PlatformForKind(KindCursor)
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.Compatibility || binding.RuntimeEvent == "" {
				continue
			}
			installed[binding.NativeEvent] = CursorEventDisposition{
				NativeEvent:   binding.NativeEvent,
				Install:       true,
				Event:         capability.Event,
				Support:       capability.Support,
				ErrorPolicy:   capability.ErrorPolicy,
				TimeoutPolicy: capability.TimeoutPolicy,
				ResponseMode:  binding.ResponseMode,
				Surfaces:      append([]HostSurface(nil), binding.Surfaces...),
				Evidence:      cursorEvidenceSemantics[binding.NativeEvent],
			}
		}
	}
	out := make([]CursorEventDisposition, 0, len(cursorCurrentEvents))
	for _, event := range cursorCurrentEvents {
		disposition, ok := installed[event]
		if !ok {
			disposition = cursorExcludedEvents[event]
		}
		disposition.Surfaces = append([]HostSurface(nil), disposition.Surfaces...)
		out = append(out, disposition)
	}
	return out
}

func cursorDocumentedSurfaces(nativeEvent string) []HostSurface {
	if nativeEvent == "afterTabFileEdit" {
		return []HostSurface{HostSurfaceCursorTab}
	}
	surfaces := []HostSurface{
		HostSurfaceCursorDesktopAgent,
		HostSurfaceCursorDesktopCmdK,
	}
	switch nativeEvent {
	case "sessionStart", "sessionEnd", "preToolUse", "postToolUse", "beforeSubmitPrompt", "stop":
		surfaces = append(surfaces,
			HostSurfaceCursorCLIInteractive,
			HostSurfaceCursorCLIPrint,
		)
	case "workspaceOpen":
		return append(surfaces,
			HostSurfaceCursorCLIInteractive,
			HostSurfaceCursorCLIPrint,
		)
	}
	switch nativeEvent {
	case "sessionStart", "sessionEnd", "beforeMCPExecution", "afterMCPExecution", "workspaceOpen":
		return surfaces
	default:
		return append(surfaces, HostSurfaceCursorCloud)
	}
}
