package hooks

import "strings"

// Event is Reconc's platform-neutral hook lifecycle.
type Event string

const (
	EventPreCommit          Event = "pre-commit"
	EventSessionStart       Event = "session-start"
	EventUserPromptSubmit   Event = "user-prompt-submit"
	EventPreToolUse         Event = "pre-tool-use"
	EventPermissionRequest  Event = "permission-request"
	EventPermissionDenied   Event = "permission-denied"
	EventPostToolUse        Event = "post-tool-use"
	EventPostToolUseFailure Event = "post-tool-use-failure"
	EventStop               Event = "stop"
	EventStopFailure        Event = "stop-failure"
	EventSessionEnd         Event = "session-end"
	EventNotification       Event = "notification"
	EventSubagentStart      Event = "subagent-start"
	EventSubagentStop       Event = "subagent-stop"
	EventPreCompaction      Event = "pre-compaction"
	EventPostCompaction     Event = "post-compaction"
)

// SupportMode describes how a platform exposes a neutral lifecycle event.
type SupportMode string

const (
	SupportNative       SupportMode = "native"
	SupportAdapted      SupportMode = "adapted"
	SupportInferred     SupportMode = "inferred"
	SupportExperimental SupportMode = "experimental"
	SupportUnsupported  SupportMode = "unsupported"
)

// FailurePolicy describes what happens when a hook errors or exceeds its
// platform timeout. Host means the platform contract owns the decision.
type FailurePolicy string

const (
	FailureBlock FailurePolicy = "block"
	FailureAllow FailurePolicy = "allow"
	FailureHost  FailurePolicy = "host"
)

// InstallMode defines the non-destructive installation strategy.
type InstallMode string

const (
	InstallExecutable  InstallMode = "executable"
	InstallNestedJSON  InstallMode = "nested-json-merge"
	InstallFlatJSON    InstallMode = "flat-json-merge"
	InstallOwnedJSON   InstallMode = "owned-json-key"
	InstallPlugin      InstallMode = "managed-plugin"
	InstallManagedJSON InstallMode = "managed-json-file"
)

// ActivationMode defines how a correct artifact becomes discoverable.
type ActivationMode string

const (
	ActivationAutomatic ActivationMode = "automatic"
	ActivationFlag      ActivationMode = "explicit-flag"
	ActivationGitPath   ActivationMode = "git-hooks-path"
)

const (
	// CodexActivationBlockStart and CodexActivationBlockEnd delimit the only
	// config.toml bytes that hook uninstall may remove.
	CodexActivationBlockStart = "# >>> reconc bootstrap hooks"
	CodexActivationBlockEnd   = "# <<< reconc bootstrap hooks"
)

// Capability is one platform mapping onto Reconc's neutral lifecycle.
type Capability struct {
	Event               Event         `json:"event"`
	NativeEvent         string        `json:"native_event,omitempty"`
	RuntimeEvents       []string      `json:"runtime_events,omitempty"`
	CompatibilityEvents []string      `json:"compatibility_events,omitempty"`
	Support             SupportMode   `json:"support"`
	Fallback            Event         `json:"fallback,omitempty"`
	ErrorPolicy         FailurePolicy `json:"error_policy"`
	TimeoutPolicy       FailurePolicy `json:"timeout_policy"`
	TimeoutSeconds      int           `json:"timeout_seconds"`
	MaxOutputBytes      int           `json:"max_output_bytes"`
}

// ActivationProbe is the declarative input used by status inspection and
// bootstrap auto-detection.
type ActivationProbe struct {
	Mode               ActivationMode `json:"mode"`
	ConfigDirs         []string       `json:"config_dirs"`
	EnablePath         string         `json:"enable_path,omitempty"`
	EnableSection      string         `json:"enable_section,omitempty"`
	EnableKey          string         `json:"enable_key,omitempty"`
	EnabledByDefault   bool           `json:"enabled_by_default,omitempty"`
	DisabledByEnv      string         `json:"disabled_by_env,omitempty"`
	LegacyArtifactPath string         `json:"legacy_artifact_path,omitempty"`
	RequiresWrapper    bool           `json:"requires_wrapper"`
}

// Platform is the stable, serializable platform contract. Generator and
// installer functions stay private but live in the same registry entries.
type Platform struct {
	Kind         string          `json:"kind"`
	DisplayName  string          `json:"display_name"`
	TargetPath   string          `json:"target_path"`
	ScaffoldPath string          `json:"scaffold_path"`
	Executable   bool            `json:"executable"`
	InstallMode  InstallMode     `json:"install_mode"`
	Activation   ActivationProbe `json:"activation"`
	Capabilities []Capability    `json:"capabilities"`
}

type platformDefinition struct {
	Platform
	generator generatorID
}

type generatorID uint8

const (
	generatorGitPreCommit generatorID = iota + 1
	generatorClaudeCode
	generatorCodex
	generatorGitHubCopilot
	generatorCursor
	generatorOpenCode
	generatorDevinCLI
	generatorAntigravity
	generatorKilo
	generatorGrok
)

const defaultHookOutputBytes = 8 * 1024

var platformRegistry = []platformDefinition{
	{
		Platform: Platform{Kind: KindGitPreCommit, DisplayName: "Git pre-commit", TargetPath: GitPreCommitPath, ScaffoldPath: GitPreCommitScaffoldPath, Executable: true, InstallMode: InstallExecutable, Activation: ActivationProbe{Mode: ActivationGitPath, ConfigDirs: []string{".git"}}, Capabilities: []Capability{
			capability(EventPreCommit, "pre-commit", SupportNative, FailureBlock, FailureBlock, 30),
		}},
		generator: generatorGitPreCommit,
	},
	{
		Platform: Platform{Kind: KindClaudeCode, DisplayName: "Claude Code", TargetPath: ClaudeCodeSettingsPath, ScaffoldPath: ClaudeCodeSettingsPath, InstallMode: InstallNestedJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".claude"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "claude-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "claude-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureBlock, 10, "claude-pre-tool-use"),
			capability(EventPermissionRequest, "PermissionRequest", SupportNative, FailureBlock, FailureBlock, 10, "claude-permission-request"),
			capability(EventPermissionDenied, "PermissionDenied", SupportNative, FailureAllow, FailureAllow, 5, "claude-permission-denied"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "claude-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUseFailure", SupportNative, FailureAllow, FailureAllow, 5, "claude-post-tool-use-failure"),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureBlock, 30, "claude-stop"),
			capability(EventStopFailure, "StopFailure", SupportNative, FailureAllow, FailureAllow, 5, "claude-stop-failure"),
			capability(EventSessionEnd, "SessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "claude-session-end"),
			capability(EventSubagentStart, "SubagentStart", SupportNative, FailureAllow, FailureAllow, 5, "claude-subagent-start"),
			capability(EventSubagentStop, "SubagentStop", SupportNative, FailureAllow, FailureAllow, 5, "claude-subagent-stop"),
			capability(EventPreCompaction, "PreCompact", SupportNative, FailureAllow, FailureAllow, 5, "claude-pre-compaction"),
			claudePostCompactionCapability(),
		}},
		generator: generatorClaudeCode,
	},
	{
		Platform: Platform{Kind: KindCodex, DisplayName: "Codex", TargetPath: CodexHooksPath, ScaffoldPath: CodexHooksPath, InstallMode: InstallNestedJSON, Activation: ActivationProbe{Mode: ActivationFlag, ConfigDirs: []string{".codex"}, EnablePath: ".codex/config.toml", EnableSection: "features", EnableKey: "hooks", EnabledByDefault: true, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "codex-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "codex-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureBlock, 10, "codex-pre-tool-use"),
			capability(EventPermissionRequest, "PermissionRequest", SupportNative, FailureBlock, FailureBlock, 10, "codex-permission-request"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "codex-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUse", SupportAdapted, FailureAllow, FailureAllow, 5),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureBlock, 30, "codex-stop"),
			unsupportedNative(EventSessionEnd, "SessionEnd"),
			capability(EventSubagentStart, "SubagentStart", SupportNative, FailureAllow, FailureAllow, 5, "codex-subagent-start"),
			capability(EventSubagentStop, "SubagentStop", SupportNative, FailureAllow, FailureAllow, 5, "codex-subagent-stop"),
			capability(EventPreCompaction, "PreCompact", SupportNative, FailureAllow, FailureAllow, 5, "codex-pre-compaction"),
			capability(EventPostCompaction, "PostCompact", SupportNative, FailureAllow, FailureAllow, 5, "codex-post-compaction"),
		}},
		generator: generatorCodex,
	},
	{
		Platform: Platform{Kind: KindGitHubCopilot, DisplayName: "GitHub Copilot", TargetPath: GitHubCopilotHooksPath, ScaffoldPath: GitHubCopilotHooksPath, InstallMode: InstallManagedJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".github/hooks"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "copilot-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "copilot-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureAllow, 10, "copilot-pre-tool-use"),
			capability(EventPermissionRequest, "PermissionRequest", SupportNative, FailureBlock, FailureAllow, 10, "copilot-permission-request"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "copilot-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUseFailure", SupportNative, FailureAllow, FailureAllow, 5, "copilot-post-tool-use-failure"),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 30, "copilot-stop"),
			capability(EventSessionEnd, "SessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "copilot-session-end"),
			capability(EventNotification, "Notification", SupportNative, FailureAllow, FailureAllow, 5, "copilot-notification"),
			capability(EventSubagentStart, "subagentStart", SupportNative, FailureAllow, FailureAllow, 5, "copilot-subagent-start"),
			capability(EventSubagentStop, "SubagentStop", SupportNative, FailureBlock, FailureAllow, 30, "copilot-subagent-stop"),
			capability(EventPreCompaction, "PreCompact", SupportNative, FailureAllow, FailureAllow, 5, "copilot-pre-compaction"),
			unsupportedNative(EventPostCompaction, "PostCompact"),
		}},
		generator: generatorGitHubCopilot,
	},
	{
		Platform: Platform{Kind: KindCursor, DisplayName: "Cursor", TargetPath: CursorHooksPath, ScaffoldPath: CursorHooksPath, InstallMode: InstallNestedJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".cursor"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "sessionStart", SupportNative, FailureAllow, FailureAllow, 5, "cursor-session-start"),
			capability(EventUserPromptSubmit, "beforeSubmitPrompt", SupportNative, FailureBlock, FailureBlock, 5, "cursor-user-prompt-submit"),
			cursorPreToolCapability(),
			fallback(EventPermissionRequest, EventPreToolUse),
			capabilityMany(EventPostToolUse, "postToolUse", SupportNative, FailureAllow, FailureAllow, 5, "cursor-post-tool-use", "cursor-after-shell-execution", "cursor-after-file-edit", "cursor-after-tab-file-edit"),
			capability(EventPostToolUseFailure, "afterShellExecution", SupportAdapted, FailureAllow, FailureAllow, 5),
			capability(EventStop, "stop", SupportNative, FailureBlock, FailureBlock, 30, "cursor-stop"),
			capability(EventSessionEnd, "sessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "cursor-session-end"),
			unsupported(EventPostCompaction),
		}},
		generator: generatorCursor,
	},
	{
		Platform: Platform{Kind: KindOpenCode, DisplayName: "OpenCode", TargetPath: OpenCodePluginPath, ScaffoldPath: OpenCodePluginPath, InstallMode: InstallPlugin, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".opencode"}}, Capabilities: []Capability{
			capability(EventSessionStart, "session.created", SupportNative, FailureAllow, FailureAllow, 5, "opencode-session-start"),
			capability(EventUserPromptSubmit, "chat.message", SupportNative, FailureAllow, FailureAllow, 5, "opencode-user-prompt-submit"),
			capability(EventPreToolUse, "tool.execute.before", SupportNative, FailureBlock, FailureBlock, 10, "opencode-pre-tool-use"),
			capability(EventPermissionRequest, "permission.ask", SupportNative, FailureBlock, FailureBlock, 10, "opencode-permission-request"),
			capability(EventPostToolUse, "tool.execute.after", SupportNative, FailureAllow, FailureAllow, 5, "opencode-post-tool-use"),
			capability(EventPostToolUseFailure, "message.part.updated(error)", SupportAdapted, FailureAllow, FailureAllow, 5, "opencode-post-tool-use-failure"),
			capability(EventStop, "session.idle", SupportInferred, FailureBlock, FailureAllow, 30, "opencode-stop"),
			capability(EventSessionEnd, "session.deleted", SupportNative, FailureAllow, FailureAllow, 5, "opencode-session-end"),
			capability(EventPreCompaction, "experimental.session.compacting", SupportExperimental, FailureAllow, FailureAllow, 5, "opencode-pre-compaction"),
			capability(EventPostCompaction, "session.compacted", SupportNative, FailureAllow, FailureAllow, 5, "opencode-post-compaction"),
		}},
		generator: generatorOpenCode,
	},
	{
		Platform: Platform{Kind: KindDevinCLI, DisplayName: "Devin CLI", TargetPath: DevinHooksPath, ScaffoldPath: DevinHooksPath, InstallMode: InstallFlatJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".devin"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "devin-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "devin-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureAllow, 10, "devin-pre-tool-use"),
			capability(EventPermissionRequest, "PermissionRequest", SupportNative, FailureBlock, FailureAllow, 10, "devin-permission-request"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "devin-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUse", SupportAdapted, FailureAllow, FailureAllow, 5),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 30, "devin-stop"),
			capability(EventSessionEnd, "SessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "devin-session-end"),
			capability(EventPostCompaction, "PostCompaction", SupportNative, FailureAllow, FailureAllow, 5, "devin-post-compaction"),
		}},
		generator: generatorDevinCLI,
	},
	{
		Platform: Platform{Kind: KindAntigravity, DisplayName: "Antigravity CLI", TargetPath: AntigravityHooksPath, ScaffoldPath: AntigravityHooksPath, InstallMode: InstallOwnedJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".agents"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "PreInvocation", SupportAdapted, FailureAllow, FailureAllow, 5, "antigravity-pre-invocation"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureAllow, 10, "antigravity-pre-tool-use"),
			fallback(EventPermissionRequest, EventPreToolUse),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "antigravity-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUse", SupportAdapted, FailureAllow, FailureAllow, 5),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 30, "antigravity-stop"),
			capability(EventSessionEnd, "PostInvocation", SupportAdapted, FailureAllow, FailureAllow, 5, "antigravity-post-invocation"),
			unsupported(EventPostCompaction),
		}},
		generator: generatorAntigravity,
	},
	{
		Platform: Platform{Kind: KindKilo, DisplayName: "Kilo Code", TargetPath: KiloPluginPath, ScaffoldPath: KiloPluginPath, InstallMode: InstallPlugin, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".kilo", ".kilocode"}, DisabledByEnv: "KILO_PURE", LegacyArtifactPath: ".kilocode/plugin/reconc.js"}, Capabilities: []Capability{
			capability(EventSessionStart, "session.created", SupportNative, FailureAllow, FailureAllow, 5, "kilo-session-start"),
			capability(EventUserPromptSubmit, "chat.message", SupportNative, FailureAllow, FailureAllow, 5, "kilo-user-prompt-submit"),
			capability(EventPreToolUse, "tool.execute.before", SupportNative, FailureBlock, FailureBlock, 10, "kilo-pre-tool-use"),
			capability(EventPermissionRequest, "permission.ask", SupportNative, FailureBlock, FailureBlock, 10, "kilo-permission-request"),
			capability(EventPostToolUse, "tool.execute.after", SupportNative, FailureAllow, FailureAllow, 5, "kilo-post-tool-use"),
			capability(EventPostToolUseFailure, "message.part.updated(error)", SupportAdapted, FailureAllow, FailureAllow, 5, "kilo-post-tool-use-failure"),
			capability(EventStop, "session.idle", SupportInferred, FailureBlock, FailureAllow, 30, "kilo-stop"),
			capability(EventSessionEnd, "session.deleted", SupportNative, FailureAllow, FailureAllow, 5, "kilo-session-end"),
			capability(EventPreCompaction, "experimental.session.compacting", SupportExperimental, FailureAllow, FailureAllow, 5, "kilo-pre-compaction"),
			capability(EventPostCompaction, "session.compacted", SupportNative, FailureAllow, FailureAllow, 5, "kilo-post-compaction"),
		}},
		generator: generatorKilo,
	},
	{
		Platform: Platform{Kind: KindGrok, DisplayName: "Grok Build", TargetPath: GrokHooksPath, ScaffoldPath: GrokHooksPath, InstallMode: InstallManagedJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".grok"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "grok-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "grok-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureAllow, 10, "grok-pre-tool-use"),
			fallback(EventPermissionRequest, EventPreToolUse),
			capability(EventPermissionDenied, "PermissionDenied", SupportNative, FailureAllow, FailureAllow, 5, "grok-permission-denied"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "grok-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUseFailure", SupportNative, FailureAllow, FailureAllow, 5, "grok-post-tool-use-failure"),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 600, "grok-stop"),
			capability(EventStopFailure, "StopFailure", SupportNative, FailureAllow, FailureAllow, 5, "grok-stop-failure"),
			capability(EventSessionEnd, "SessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "grok-session-end"),
			capability(EventNotification, "Notification", SupportNative, FailureAllow, FailureAllow, 5, "grok-notification"),
			capability(EventSubagentStart, "SubagentStart", SupportNative, FailureAllow, FailureAllow, 5, "grok-subagent-start"),
			capability(EventSubagentStop, "SubagentStop", SupportNative, FailureAllow, FailureAllow, 5, "grok-subagent-stop"),
			capability(EventPreCompaction, "PreCompact", SupportNative, FailureAllow, FailureAllow, 5, "grok-pre-compaction"),
			capability(EventPostCompaction, "PostCompact", SupportNative, FailureAllow, FailureAllow, 5, "grok-post-compaction"),
		}},
		generator: generatorGrok,
	},
}

var runtimeRouteIndex = buildRuntimeRouteIndex()

func capability(event Event, native string, support SupportMode, errors, timeouts FailurePolicy, timeoutSeconds int, runtimeEvents ...string) Capability {
	return Capability{Event: event, NativeEvent: native, RuntimeEvents: runtimeEvents, Support: support, ErrorPolicy: errors, TimeoutPolicy: timeouts, TimeoutSeconds: timeoutSeconds, MaxOutputBytes: defaultHookOutputBytes}
}

func capabilityMany(event Event, native string, support SupportMode, errors, timeouts FailurePolicy, timeoutSeconds int, runtimeEvents ...string) Capability {
	return capability(event, native, support, errors, timeouts, timeoutSeconds, runtimeEvents...)
}

func cursorPreToolCapability() Capability {
	capability := capabilityMany(EventPreToolUse, "preToolUse", SupportNative, FailureBlock, FailureBlock, 10, "cursor-pre-tool-use", "cursor-before-shell-execution")
	capability.CompatibilityEvents = []string{"cursor-before-read-file", "cursor-before-tab-file-read"}
	return capability
}

func claudePostCompactionCapability() Capability {
	capability := capability(EventPostCompaction, "PostCompact", SupportNative, FailureAllow, FailureAllow, 5, "claude-post-compaction")
	capability.CompatibilityEvents = []string{"claude-compaction-recovery"}
	return capability
}

func fallback(event, fallbackEvent Event) Capability {
	return Capability{Event: event, Support: SupportUnsupported, Fallback: fallbackEvent, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost, MaxOutputBytes: defaultHookOutputBytes}
}

func unsupported(event Event) Capability {
	return Capability{Event: event, Support: SupportUnsupported, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost, MaxOutputBytes: defaultHookOutputBytes}
}

func unsupportedNative(event Event, native string) Capability {
	capability := unsupported(event)
	capability.NativeEvent = native
	return capability
}

// Platforms returns deep copies so callers cannot mutate the registry.
func Platforms() []Platform {
	platforms := make([]Platform, len(platformRegistry))
	for i := range platformRegistry {
		platforms[i] = clonePlatform(platformRegistry[i].Platform)
	}
	return platforms
}

// AgentPlatforms returns every non-git platform in stable bootstrap order.
func AgentPlatforms() []Platform {
	platforms := Platforms()
	out := make([]Platform, 0, len(platforms)-1)
	for _, platform := range platforms {
		if platform.Kind != KindGitPreCommit {
			out = append(out, platform)
		}
	}
	return out
}

func PlatformForKind(kind string) (Platform, bool) {
	definition, ok := lookupPlatformDefinition(kind)
	if !ok {
		return Platform{}, false
	}
	return clonePlatform(definition.Platform), true
}

func platformKinds(agentOnly bool) []string {
	kinds := make([]string, 0, len(platformRegistry))
	for _, definition := range platformRegistry {
		if agentOnly && definition.Kind == KindGitPreCommit {
			continue
		}
		kinds = append(kinds, definition.Kind)
	}
	return kinds
}

func lookupPlatformDefinition(kind string) (platformDefinition, bool) {
	for _, definition := range platformRegistry {
		if definition.Kind == kind {
			return definition, true
		}
	}
	return platformDefinition{}, false
}

func clonePlatform(platform Platform) Platform {
	platform.Activation.ConfigDirs = append([]string(nil), platform.Activation.ConfigDirs...)
	platform.Capabilities = append([]Capability(nil), platform.Capabilities...)
	for i := range platform.Capabilities {
		platform.Capabilities[i].RuntimeEvents = append([]string(nil), platform.Capabilities[i].RuntimeEvents...)
		platform.Capabilities[i].CompatibilityEvents = append([]string(nil), platform.Capabilities[i].CompatibilityEvents...)
	}
	return platform
}

// RuntimeRoute is the allocation-free dispatch view used on every hook event.
type RuntimeRoute struct {
	PlatformKind   string
	Event          Event
	ErrorPolicy    FailurePolicy
	TimeoutPolicy  FailurePolicy
	TimeoutSeconds int
	MaxOutputBytes int
}

// RuntimeEvent resolves a generated runtime route back to its platform-neutral
// event without cloning the full platform registry entry.
func RuntimeEvent(name string) (RuntimeRoute, bool) {
	route, ok := runtimeRouteIndex[name]
	return route, ok
}

func buildRuntimeRouteIndex() map[string]RuntimeRoute {
	count := 0
	for _, definition := range platformRegistry {
		for _, capability := range definition.Capabilities {
			count += len(capability.RuntimeEvents) + len(capability.CompatibilityEvents)
		}
	}
	index := make(map[string]RuntimeRoute, count)
	for _, definition := range platformRegistry {
		for _, capability := range definition.Capabilities {
			for _, runtimeEvent := range capability.RuntimeEvents {
				index[runtimeEvent] = runtimeRoute(definition.Kind, capability)
			}
			for _, runtimeEvent := range capability.CompatibilityEvents {
				index[runtimeEvent] = runtimeRoute(definition.Kind, capability)
			}
		}
	}
	return index
}

func runtimeRoute(kind string, capability Capability) RuntimeRoute {
	return RuntimeRoute{
		PlatformKind:   kind,
		Event:          capability.Event,
		ErrorPolicy:    capability.ErrorPolicy,
		TimeoutPolicy:  capability.TimeoutPolicy,
		TimeoutSeconds: capability.TimeoutSeconds,
		MaxOutputBytes: capability.MaxOutputBytes,
	}
}

// RuntimeEvents returns every agent hook-runtime route in deterministic
// platform and lifecycle order. Git pre-commit executes `reconc ci` directly
// and therefore has no hook-runtime route.
func RuntimeEvents() []string {
	events := []string{}
	for _, definition := range platformRegistry {
		for _, capability := range definition.Capabilities {
			events = append(events, capability.RuntimeEvents...)
			events = append(events, capability.CompatibilityEvents...)
		}
	}
	return events
}

// GrokRuntimeEvents returns every first-class Grok runtime route in registry
// order. Hook generation, preflight, doctor, and audits share this owner.
func GrokRuntimeEvents() []string {
	platform, ok := PlatformForKind(KindGrok)
	if !ok {
		return nil
	}
	events := make([]string, 0, len(platform.Capabilities))
	for _, capability := range platform.Capabilities {
		events = append(events, capability.RuntimeEvents...)
	}
	return events
}

// GrokTargetHasRuntimeEvent matches one exact event argument in an inspected
// Grok command. Prefix collisions such as grok-stop-failure cannot satisfy
// grok-stop coverage.
func GrokTargetHasRuntimeEvent(target, event string) bool {
	event = strings.TrimSpace(event)
	if event == "" {
		return false
	}
	for _, field := range strings.Fields(target) {
		if strings.Trim(field, `"'`) == event {
			return true
		}
	}
	return false
}
