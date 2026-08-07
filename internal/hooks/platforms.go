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
	EventPermissionResult   Event = "permission-result"
	EventPermissionDenied   Event = "permission-denied"
	EventPostToolUse        Event = "post-tool-use"
	EventPostToolUseFailure Event = "post-tool-use-failure"
	EventToolObservation    Event = "tool-observation"
	EventMCPBefore          Event = "mcp-before"
	EventMCPAfter           Event = "mcp-after"
	EventStop               Event = "stop"
	EventStopFailure        Event = "stop-failure"
	EventInterrupt          Event = "interrupt"
	EventSessionEnd         Event = "session-end"
	EventNotification       Event = "notification"
	EventSubagentStart      Event = "subagent-start"
	EventSubagentStop       Event = "subagent-stop"
	EventPreCompaction      Event = "pre-compaction"
	EventPostCompaction     Event = "post-compaction"
	EventContinuation       Event = "continuation-observation"
	EventWorkspaceOpen      Event = "workspace-open"
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
	InstallExecutable       InstallMode = "executable"
	InstallNestedJSON       InstallMode = "nested-json-merge"
	InstallNestedEventsJSON InstallMode = "nested-events-json-merge"
	InstallFlatJSON         InstallMode = "flat-json-merge"
	InstallOwnedJSON        InstallMode = "owned-json-key"
	InstallPlugin           InstallMode = "managed-plugin"
	InstallManagedJSON      InstallMode = "managed-json-file"
	InstallGlobalTOML       InstallMode = "global-toml-block"
)

// ActivationMode defines how a correct artifact becomes discoverable.
type ActivationMode string

const (
	ActivationAutomatic ActivationMode = "automatic"
	ActivationFlag      ActivationMode = "explicit-flag"
	ActivationGitPath   ActivationMode = "git-hooks-path"
	ActivationGlobal    ActivationMode = "global-config"
)

const (
	// CodexActivationBlockStart and CodexActivationBlockEnd delimit the only
	// config.toml bytes that hook uninstall may remove.
	CodexActivationBlockStart = "# >>> reconc bootstrap hooks"
	CodexActivationBlockEnd   = "# <<< reconc bootstrap hooks"
)

// Capability is one platform mapping onto Reconc's neutral lifecycle.
type Capability struct {
	Event            Event           `json:"event"`
	Bindings         []NativeBinding `json:"bindings,omitempty"`
	Support          SupportMode     `json:"support"`
	Fallback         Event           `json:"fallback,omitempty"`
	ErrorPolicy      FailurePolicy   `json:"error_policy"`
	TimeoutPolicy    FailurePolicy   `json:"timeout_policy"`
	TimeoutSeconds   int             `json:"timeout_seconds"`
	MaxOutputBytes   int             `json:"max_output_bytes"`
	MaxContinuations int             `json:"max_continuations,omitempty"`
}

// CursorResponseMode is the host response contract for one Cursor binding.
type CursorResponseMode string

const (
	CursorResponseDecision      CursorResponseMode = "decision"
	CursorResponseObservation   CursorResponseMode = "observation"
	CursorResponseStopFollowup  CursorResponseMode = "stop-followup"
	CursorResponseFireAndForget CursorResponseMode = "fire-and-forget"
)

// HostSurface is one declared host execution surface. Configuration and live
// observation remain separate status facts.
type HostSurface string

const (
	HostSurfaceCursorDesktopAgent   HostSurface = "cursor-desktop-agent"
	HostSurfaceCursorDesktopCmdK    HostSurface = "cursor-desktop-cmd-k"
	HostSurfaceCursorTab            HostSurface = "cursor-tab"
	HostSurfaceCursorCLIInteractive HostSurface = "cursor-cli-interactive"
	HostSurfaceCursorCLIPrint       HostSurface = "cursor-cli-print"
	HostSurfaceCursorCloud          HostSurface = "cursor-cloud"
)

// NativeBinding owns one host event to runtime-route mapping.
type NativeBinding struct {
	NativeEvent   string             `json:"native_event"`
	RuntimeEvent  string             `json:"runtime_event,omitempty"`
	Compatibility bool               `json:"compatibility"`
	Matcher       string             `json:"matcher,omitempty"`
	ResponseMode  CursorResponseMode `json:"response_mode,omitempty"`
	LoopLimit     int                `json:"loop_limit,omitempty"`
	Surfaces      []HostSurface      `json:"surfaces,omitempty"`
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
	generatorOMP
	generatorPi
	generatorZCode
	generatorKimiCode
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
			capability(EventNotification, "Notification", SupportNative, FailureAllow, FailureAllow, 5, "claude-notification"),
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
			adaptedFallback(EventPostToolUseFailure, EventPostToolUse, FailureAllow, FailureAllow, 5),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureBlock, 30, "codex-stop"),
			capability(EventSessionEnd, "SessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "codex-session-end"),
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
			cursorCapability(EventSessionStart, "sessionStart", "cursor-session-start", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseFireAndForget, 0),
			cursorCapability(EventUserPromptSubmit, "beforeSubmitPrompt", "cursor-user-prompt-submit", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseDecision, 0),
			cursorPreToolCapability(),
			fallback(EventPermissionRequest, EventPreToolUse),
			cursorPostToolCapability(),
			cursorCapability(EventPostToolUseFailure, "postToolUseFailure", "cursor-post-tool-use-failure", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseObservation, 0),
			cursorCapability(EventToolObservation, "afterShellExecution", "cursor-after-shell-execution", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseObservation, 0),
			cursorCapability(EventMCPBefore, "beforeMCPExecution", "cursor-before-mcp-execution", SupportNative, FailureBlock, FailureBlock, 10, CursorResponseDecision, 0),
			cursorCapability(EventMCPAfter, "afterMCPExecution", "cursor-after-mcp-execution", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseObservation, 0),
			cursorCapability(EventSubagentStart, "subagentStart", "cursor-subagent-start", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseDecision, 0),
			cursorCapability(EventSubagentStop, "subagentStop", "cursor-subagent-stop", SupportNative, FailureAllow, FailureAllow, 30, CursorResponseStopFollowup, 10),
			cursorCapability(EventPreCompaction, "preCompact", "cursor-pre-compaction", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseFireAndForget, 0),
			cursorCapability(EventStop, "stop", "cursor-stop", SupportNative, FailureBlock, FailureBlock, 30, CursorResponseStopFollowup, 10),
			cursorCapability(EventSessionEnd, "sessionEnd", "cursor-session-end", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseFireAndForget, 0),
			cursorCapability(EventWorkspaceOpen, "workspaceOpen", "cursor-workspace-open", SupportNative, FailureAllow, FailureAllow, 5, CursorResponseFireAndForget, 0),
			unsupported(EventPostCompaction),
		}},
		generator: generatorCursor,
	},
	{
		Platform: Platform{Kind: KindOpenCode, DisplayName: "OpenCode", TargetPath: OpenCodePluginPath, ScaffoldPath: OpenCodePluginPath, InstallMode: InstallPlugin, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".opencode"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "session.created", SupportNative, FailureAllow, FailureAllow, 5, "opencode-session-start"),
			capability(EventUserPromptSubmit, "chat.message", SupportNative, FailureAllow, FailureAllow, 5, "opencode-user-prompt-submit"),
			capability(EventPreToolUse, "tool.execute.before", SupportNative, FailureBlock, FailureBlock, 10, "opencode-pre-tool-use"),
			capability(EventPermissionRequest, "permission.ask", SupportNative, FailureBlock, FailureBlock, 10, "opencode-permission-request"),
			capability(EventPostToolUse, "tool.execute.after", SupportNative, FailureAllow, FailureAllow, 5, "opencode-post-tool-use"),
			capability(EventPostToolUseFailure, "message.part.updated(error)", SupportAdapted, FailureAllow, FailureAllow, 5, "opencode-post-tool-use-failure"),
			adaptedFallback(EventMCPBefore, EventPreToolUse, FailureBlock, FailureBlock, 10),
			adaptedFallback(EventMCPAfter, EventPostToolUse, FailureAllow, FailureAllow, 5),
			bunStopCapability("opencode"),
			continuationCapability("opencode"),
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
			adaptedFallback(EventPostToolUseFailure, EventPostToolUse, FailureAllow, FailureAllow, 5),
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
			adaptedFallback(EventPostToolUseFailure, EventPostToolUse, FailureAllow, FailureAllow, 5),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 30, "antigravity-stop"),
			capability(EventSessionEnd, "PostInvocation", SupportAdapted, FailureAllow, FailureAllow, 5, "antigravity-post-invocation"),
			unsupported(EventPostCompaction),
		}},
		generator: generatorAntigravity,
	},
	{
		Platform: Platform{Kind: KindKilo, DisplayName: "Kilo Code", TargetPath: KiloPluginPath, ScaffoldPath: KiloPluginPath, InstallMode: InstallPlugin, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".kilo", ".kilocode"}, DisabledByEnv: "KILO_PURE", LegacyArtifactPath: ".kilocode/plugin/reconc.js", RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "session.created", SupportNative, FailureAllow, FailureAllow, 5, "kilo-session-start"),
			capability(EventUserPromptSubmit, "chat.message", SupportNative, FailureAllow, FailureAllow, 5, "kilo-user-prompt-submit"),
			capability(EventPreToolUse, "tool.execute.before", SupportNative, FailureBlock, FailureBlock, 10, "kilo-pre-tool-use"),
			capability(EventPermissionRequest, "permission.ask", SupportNative, FailureBlock, FailureBlock, 10, "kilo-permission-request"),
			capability(EventPostToolUse, "tool.execute.after", SupportNative, FailureAllow, FailureAllow, 5, "kilo-post-tool-use"),
			capability(EventPostToolUseFailure, "message.part.updated(error)", SupportAdapted, FailureAllow, FailureAllow, 5, "kilo-post-tool-use-failure"),
			adaptedFallback(EventMCPBefore, EventPreToolUse, FailureBlock, FailureBlock, 10),
			adaptedFallback(EventMCPAfter, EventPostToolUse, FailureAllow, FailureAllow, 5),
			bunStopCapability("kilo"),
			continuationCapability("kilo"),
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
	{
		Platform: Platform{Kind: KindOMP, DisplayName: "Oh My Pi", TargetPath: OMPExtensionPath, ScaffoldPath: OMPExtensionPath, InstallMode: InstallPlugin, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".omp"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "session_start", SupportNative, FailureAllow, FailureAllow, 5, "omp-session-start"),
			capability(EventUserPromptSubmit, "input", SupportNative, FailureAllow, FailureAllow, 5, "omp-user-prompt-submit"),
			capability(EventPreToolUse, "tool_call", SupportNative, FailureBlock, FailureBlock, 10, "omp-pre-tool-use"),
			capability(EventPermissionRequest, "tool_approval_requested", SupportNative, FailureAllow, FailureAllow, 5, "omp-permission-request"),
			capability(EventPermissionResult, "tool_approval_resolved", SupportNative, FailureAllow, FailureAllow, 5, "omp-permission-result"),
			capability(EventPostToolUse, "tool_result", SupportNative, FailureAllow, FailureAllow, 5, "omp-post-tool-use"),
			capability(EventPostToolUseFailure, "tool_result", SupportNative, FailureAllow, FailureAllow, 5, "omp-post-tool-use-failure"),
			adaptedFallback(EventMCPBefore, EventPreToolUse, FailureBlock, FailureBlock, 10),
			adaptedFallback(EventMCPAfter, EventPostToolUse, FailureAllow, FailureAllow, 5),
			ompStopCapability(),
			capability(EventSessionEnd, "session_shutdown", SupportNative, FailureAllow, FailureAllow, 1, "omp-session-end"),
			capability(EventPreCompaction, "auto_compaction_start", SupportNative, FailureAllow, FailureAllow, 5, "omp-pre-compaction"),
			capability(EventPostCompaction, "auto_compaction_end", SupportNative, FailureAllow, FailureAllow, 5, "omp-post-compaction"),
		}},
		generator: generatorOMP,
	},
	{
		Platform: Platform{Kind: KindPi, DisplayName: "Pi Coding Agent", TargetPath: PiExtensionPath, ScaffoldPath: PiExtensionPath, InstallMode: InstallPlugin, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".pi"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "session_start", SupportNative, FailureAllow, FailureAllow, 5, "pi-session-start"),
			capability(EventUserPromptSubmit, "input", SupportNative, FailureAllow, FailureAllow, 5, "pi-user-prompt-submit"),
			piPreToolCapability(),
			fallback(EventPermissionRequest, EventPreToolUse),
			capability(EventPostToolUse, "tool_result", SupportNative, FailureAllow, FailureAllow, 5, "pi-post-tool-use"),
			capability(EventPostToolUseFailure, "tool_result", SupportNative, FailureAllow, FailureAllow, 5, "pi-post-tool-use-failure"),
			adaptedFallback(EventMCPBefore, EventPreToolUse, FailureBlock, FailureBlock, 10),
			adaptedFallback(EventMCPAfter, EventPostToolUse, FailureAllow, FailureAllow, 5),
			piStopCapability(),
			piContinuationCapability(),
			capability(EventSessionEnd, "session_shutdown", SupportNative, FailureAllow, FailureAllow, 5, "pi-session-end"),
			capability(EventPreCompaction, "session_before_compact", SupportNative, FailureAllow, FailureAllow, 5, "pi-pre-compaction"),
			capability(EventPostCompaction, "session_compact", SupportNative, FailureAllow, FailureAllow, 5, "pi-post-compaction"),
		}},
		generator: generatorPi,
	},
	{
		Platform: Platform{Kind: KindZCode, DisplayName: "ZCode", TargetPath: ZCodeConfigPath, ScaffoldPath: ZCodeConfigPath, InstallMode: InstallNestedEventsJSON, Activation: ActivationProbe{Mode: ActivationAutomatic, ConfigDirs: []string{".zcode"}, RequiresWrapper: true}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "zcode-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "zcode-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureAllow, 10, "zcode-pre-tool-use"),
			capability(EventPermissionRequest, "PermissionRequest", SupportNative, FailureBlock, FailureAllow, 10, "zcode-permission-request"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "zcode-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUseFailure", SupportNative, FailureAllow, FailureAllow, 5, "zcode-post-tool-use-failure"),
			adaptedFallback(EventMCPBefore, EventPreToolUse, FailureBlock, FailureAllow, 10),
			adaptedFallback(EventMCPAfter, EventPostToolUse, FailureAllow, FailureAllow, 5),
			zcodeStopCapability(),
			unsupported(EventSessionEnd),
			unsupported(EventPostCompaction),
		}},
		generator: generatorZCode,
	},
	{
		Platform: Platform{Kind: KindKimiCode, DisplayName: "Kimi Code CLI", TargetPath: KimiCodeConfigDisplayPath, InstallMode: InstallGlobalTOML, Activation: ActivationProbe{Mode: ActivationGlobal, ConfigDirs: []string{"~/.kimi-code"}}, Capabilities: []Capability{
			capability(EventSessionStart, "SessionStart", SupportNative, FailureAllow, FailureAllow, 5, "kimi-session-start"),
			capability(EventUserPromptSubmit, "UserPromptSubmit", SupportNative, FailureAllow, FailureAllow, 5, "kimi-user-prompt-submit"),
			capability(EventPreToolUse, "PreToolUse", SupportNative, FailureBlock, FailureAllow, 10, "kimi-pre-tool-use"),
			capability(EventPermissionRequest, "PermissionRequest", SupportNative, FailureAllow, FailureAllow, 5, "kimi-permission-request"),
			capability(EventPermissionResult, "PermissionResult", SupportNative, FailureAllow, FailureAllow, 5, "kimi-permission-result"),
			capability(EventPostToolUse, "PostToolUse", SupportNative, FailureAllow, FailureAllow, 5, "kimi-post-tool-use"),
			capability(EventPostToolUseFailure, "PostToolUseFailure", SupportNative, FailureAllow, FailureAllow, 5, "kimi-post-tool-use-failure"),
			capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 30, "kimi-stop"),
			capability(EventStopFailure, "StopFailure", SupportNative, FailureAllow, FailureAllow, 5, "kimi-stop-failure"),
			capability(EventInterrupt, "Interrupt", SupportNative, FailureAllow, FailureAllow, 5, "kimi-interrupt"),
			capability(EventSessionEnd, "SessionEnd", SupportNative, FailureAllow, FailureAllow, 5, "kimi-session-end"),
			capability(EventSubagentStart, "SubagentStart", SupportNative, FailureAllow, FailureAllow, 5, "kimi-subagent-start"),
			capability(EventSubagentStop, "SubagentStop", SupportNative, FailureAllow, FailureAllow, 5, "kimi-subagent-stop"),
			capability(EventPreCompaction, "PreCompact", SupportNative, FailureAllow, FailureAllow, 5, "kimi-pre-compaction"),
			capability(EventPostCompaction, "PostCompact", SupportNative, FailureAllow, FailureAllow, 5, "kimi-post-compaction"),
			capability(EventNotification, "Notification", SupportNative, FailureAllow, FailureAllow, 5, "kimi-notification"),
		}},
		generator: generatorKimiCode,
	},
}

var runtimeRouteIndex = buildRuntimeRouteIndex()

func capability(event Event, native string, support SupportMode, errors, timeouts FailurePolicy, timeoutSeconds int, runtimeEvents ...string) Capability {
	bindings := make([]NativeBinding, 0, len(runtimeEvents))
	if len(runtimeEvents) == 0 && native != "" {
		bindings = append(bindings, NativeBinding{NativeEvent: native})
	}
	for _, runtimeEvent := range runtimeEvents {
		bindings = append(bindings, NativeBinding{NativeEvent: native, RuntimeEvent: runtimeEvent})
	}
	return Capability{Event: event, Bindings: bindings, Support: support, ErrorPolicy: errors, TimeoutPolicy: timeouts, TimeoutSeconds: timeoutSeconds, MaxOutputBytes: defaultHookOutputBytes}
}

func cursorPreToolCapability() Capability {
	return Capability{
		Event: EventPreToolUse,
		Bindings: []NativeBinding{
			cursorBinding("preToolUse", "cursor-pre-tool-use", "Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabWrite", CursorResponseDecision, 0),
			cursorBinding("beforeShellExecution", "cursor-before-shell-execution", "", CursorResponseDecision, 0),
		},
		Support: SupportNative, ErrorPolicy: FailureBlock, TimeoutPolicy: FailureBlock,
		TimeoutSeconds: 10, MaxOutputBytes: defaultHookOutputBytes,
	}
}

func cursorPostToolCapability() Capability {
	return Capability{
		Event: EventPostToolUse,
		Bindings: []NativeBinding{
			cursorBinding("postToolUse", "cursor-post-tool-use", "Read|Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabRead|TabWrite|Shell", CursorResponseObservation, 0),
			cursorBinding("afterFileEdit", "cursor-after-file-edit", "", CursorResponseObservation, 0),
			cursorBinding("afterTabFileEdit", "cursor-after-tab-file-edit", "", CursorResponseObservation, 0),
		},
		Support: SupportNative, ErrorPolicy: FailureAllow, TimeoutPolicy: FailureAllow,
		TimeoutSeconds: 5, MaxOutputBytes: defaultHookOutputBytes,
	}
}

func cursorBinding(nativeEvent, runtimeEvent, matcher string, responseMode CursorResponseMode, loopLimit int) NativeBinding {
	return NativeBinding{
		NativeEvent: nativeEvent, RuntimeEvent: runtimeEvent, Matcher: matcher,
		ResponseMode: responseMode, LoopLimit: loopLimit,
		Surfaces: cursorDocumentedSurfaces(nativeEvent),
	}
}

func cursorCapability(event Event, nativeEvent, runtimeEvent string, support SupportMode, errors, timeouts FailurePolicy, timeoutSeconds int, responseMode CursorResponseMode, loopLimit int) Capability {
	return Capability{
		Event: event, Bindings: []NativeBinding{cursorBinding(nativeEvent, runtimeEvent, "", responseMode, loopLimit)},
		Support: support, ErrorPolicy: errors, TimeoutPolicy: timeouts,
		TimeoutSeconds: timeoutSeconds, MaxOutputBytes: defaultHookOutputBytes,
	}
}

func continuationCapability(prefix string) Capability {
	return Capability{
		Event: EventContinuation,
		Bindings: []NativeBinding{
			{NativeEvent: "session.idle", RuntimeEvent: prefix + "-continuation-accepted", Compatibility: true},
			{NativeEvent: "session.idle", RuntimeEvent: prefix + "-continuation-failed", Compatibility: true},
			{NativeEvent: "session.idle", RuntimeEvent: prefix + "-continuation-unavailable", Compatibility: true},
			{NativeEvent: "session.idle", RuntimeEvent: prefix + "-continuation-suppressed", Compatibility: true},
		},
		Support: SupportInferred, ErrorPolicy: FailureAllow, TimeoutPolicy: FailureAllow,
		TimeoutSeconds: 5, MaxOutputBytes: defaultHookOutputBytes, MaxContinuations: 10,
	}
}

func bunStopCapability(prefix string) Capability {
	capability := capability(EventStop, "session.idle", SupportInferred, FailureBlock, FailureAllow, 30, prefix+"-stop")
	capability.MaxContinuations = 10
	return capability
}

func ompStopCapability() Capability {
	capability := capability(EventStop, "session_stop", SupportNative, FailureBlock, FailureBlock, 29, "omp-stop")
	capability.MaxContinuations = 8
	return capability
}

func zcodeStopCapability() Capability {
	capability := capability(EventStop, "Stop", SupportNative, FailureBlock, FailureAllow, 30, "zcode-stop")
	capability.MaxContinuations = 3
	return capability
}

func piPreToolCapability() Capability {
	return Capability{
		Event: EventPreToolUse,
		Bindings: []NativeBinding{
			{NativeEvent: "tool_call", RuntimeEvent: "pi-pre-tool-use"},
			{NativeEvent: "user_bash", RuntimeEvent: "pi-user-bash"},
		},
		Support: SupportNative, ErrorPolicy: FailureBlock, TimeoutPolicy: FailureBlock,
		TimeoutSeconds: 10, MaxOutputBytes: defaultHookOutputBytes,
	}
}

func piStopCapability() Capability {
	capability := capability(EventStop, "agent_settled", SupportInferred, FailureAllow, FailureAllow, 30, "pi-stop")
	capability.MaxContinuations = 10
	return capability
}

func piContinuationCapability() Capability {
	return Capability{
		Event: EventContinuation,
		Bindings: []NativeBinding{
			{NativeEvent: "agent_settled", RuntimeEvent: "pi-continuation-requested", Compatibility: true},
			{NativeEvent: "agent_settled", RuntimeEvent: "pi-continuation-failed", Compatibility: true},
			{NativeEvent: "agent_settled", RuntimeEvent: "pi-continuation-suppressed", Compatibility: true},
		},
		Support: SupportInferred, ErrorPolicy: FailureAllow, TimeoutPolicy: FailureAllow,
		TimeoutSeconds: 5, MaxOutputBytes: defaultHookOutputBytes, MaxContinuations: 10,
	}
}

func claudePostCompactionCapability() Capability {
	return Capability{
		Event: EventPostCompaction,
		Bindings: []NativeBinding{
			{NativeEvent: "PostCompact", RuntimeEvent: "claude-post-compaction"},
			{NativeEvent: "SessionStart", RuntimeEvent: "claude-compaction-recovery", Compatibility: true},
		},
		Support: SupportNative, ErrorPolicy: FailureAllow, TimeoutPolicy: FailureAllow,
		TimeoutSeconds: 5, MaxOutputBytes: defaultHookOutputBytes,
	}
}

func fallback(event, fallbackEvent Event) Capability {
	return Capability{Event: event, Support: SupportUnsupported, Fallback: fallbackEvent, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost, MaxOutputBytes: defaultHookOutputBytes}
}

func adaptedFallback(event, fallbackEvent Event, errors, timeouts FailurePolicy, timeoutSeconds int) Capability {
	return Capability{
		Event: event, Support: SupportAdapted, Fallback: fallbackEvent,
		ErrorPolicy: errors, TimeoutPolicy: timeouts, TimeoutSeconds: timeoutSeconds,
		MaxOutputBytes: defaultHookOutputBytes,
	}
}

func unsupported(event Event) Capability {
	return Capability{Event: event, Support: SupportUnsupported, ErrorPolicy: FailureHost, TimeoutPolicy: FailureHost, MaxOutputBytes: defaultHookOutputBytes}
}

func unsupportedNative(event Event, native string) Capability {
	capability := unsupported(event)
	capability.Bindings = []NativeBinding{{NativeEvent: native}}
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

// RepositoryAgentPlatforms returns agent integrations whose artifacts belong
// to one repository. Global host integrations such as Kimi Code are excluded
// from bootstrap auto-detection and repository transaction plans.
func RepositoryAgentPlatforms() []Platform {
	platforms := AgentPlatforms()
	out := make([]Platform, 0, len(platforms))
	for _, platform := range platforms {
		if platform.Activation.Mode != ActivationGlobal {
			out = append(out, platform)
		}
	}
	return out
}

// BootstrapKinds returns every repository-owned hook kind accepted by
// bootstrap and init. Global host configuration is always an explicit,
// separate hook install.
func BootstrapKinds() []string {
	kinds := make([]string, 0, len(platformRegistry))
	for _, definition := range platformRegistry {
		if definition.Activation.Mode != ActivationGlobal {
			kinds = append(kinds, definition.Kind)
		}
	}
	return kinds
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
		platform.Capabilities[i].Bindings = append([]NativeBinding(nil), platform.Capabilities[i].Bindings...)
		for bindingIndex := range platform.Capabilities[i].Bindings {
			platform.Capabilities[i].Bindings[bindingIndex].Surfaces = append([]HostSurface(nil), platform.Capabilities[i].Bindings[bindingIndex].Surfaces...)
		}
	}
	return platform
}

// PrimaryNativeEvent derives the advertised host event from the first
// non-compatibility binding.
func (capability Capability) PrimaryNativeEvent() string {
	for _, binding := range capability.Bindings {
		if !binding.Compatibility {
			return binding.NativeEvent
		}
	}
	return ""
}

func validatePlatform(platform Platform) error {
	nativeEvents := map[string]struct{}{}
	runtimeEvents := map[string]struct{}{}
	for _, capability := range platform.Capabilities {
		if capability.MaxOutputBytes <= 0 {
			return hookGeneratorError("%s %s has no output budget", platform.Kind, capability.Event)
		}
		if capability.Support != SupportUnsupported && capability.TimeoutSeconds <= 0 {
			return hookGeneratorError("%s %s has no timeout budget", platform.Kind, capability.Event)
		}
		if capability.MaxContinuations < 0 || capability.MaxContinuations > 100 {
			return hookGeneratorError("%s %s has invalid continuation limit %d", platform.Kind, capability.Event, capability.MaxContinuations)
		}
		if platform.Kind != KindGitPreCommit && capability.Support != SupportUnsupported &&
			len(capability.Bindings) == 0 && capability.Fallback == "" {
			return hookGeneratorError("%s %s has no native binding or fallback", platform.Kind, capability.Event)
		}
		for _, binding := range capability.Bindings {
			if binding.NativeEvent == "" {
				return hookGeneratorError("%s %s has an empty native event binding", platform.Kind, capability.Event)
			}
			if capability.Support == SupportUnsupported && binding.RuntimeEvent != "" {
				return hookGeneratorError("%s unsupported %s binding exposes runtime route %s", platform.Kind, capability.Event, binding.RuntimeEvent)
			}
			if platform.Kind != KindGitPreCommit && capability.Support != SupportUnsupported && binding.RuntimeEvent == "" {
				return hookGeneratorError("%s %s binding %s has no runtime route", platform.Kind, capability.Event, binding.NativeEvent)
			}
			if binding.RuntimeEvent != "" {
				if _, duplicate := runtimeEvents[binding.RuntimeEvent]; duplicate {
					return hookGeneratorError("%s duplicates runtime route %s", platform.Kind, binding.RuntimeEvent)
				}
				runtimeEvents[binding.RuntimeEvent] = struct{}{}
			}
			if platform.Kind != KindCursor {
				if binding.Matcher != "" || binding.ResponseMode != "" || binding.LoopLimit != 0 || len(binding.Surfaces) != 0 {
					return hookGeneratorError("%s binding %s uses Cursor-only fields", platform.Kind, binding.NativeEvent)
				}
				continue
			}
			if !binding.Compatibility {
				if _, duplicate := nativeEvents[binding.NativeEvent]; duplicate {
					return hookGeneratorError("%s duplicates native event %s", platform.Kind, binding.NativeEvent)
				}
				nativeEvents[binding.NativeEvent] = struct{}{}
			}
			if binding.ResponseMode == "" {
				return hookGeneratorError("Cursor binding %s has no response mode", binding.NativeEvent)
			}
			if binding.LoopLimit < 0 || binding.LoopLimit > 100 {
				return hookGeneratorError("Cursor binding %s has invalid loop limit %d", binding.NativeEvent, binding.LoopLimit)
			}
			if binding.LoopLimit > 0 && binding.ResponseMode != CursorResponseStopFollowup {
				return hookGeneratorError("Cursor binding %s has loop limit outside stop-followup mode", binding.NativeEvent)
			}
		}
		if platform.Kind == KindCursor && capability.Support != SupportUnsupported &&
			capability.ErrorPolicy != capability.TimeoutPolicy {
			return hookGeneratorError("Cursor %s cannot represent different error and timeout policies", capability.Event)
		}
	}
	return nil
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
			for _, binding := range capability.Bindings {
				if binding.RuntimeEvent != "" {
					count++
				}
			}
		}
	}
	index := make(map[string]RuntimeRoute, count)
	for _, definition := range platformRegistry {
		for _, capability := range definition.Capabilities {
			for _, binding := range capability.Bindings {
				if binding.RuntimeEvent != "" {
					index[binding.RuntimeEvent] = runtimeRoute(definition.Kind, capability)
				}
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
			for _, binding := range capability.Bindings {
				if binding.RuntimeEvent != "" {
					events = append(events, binding.RuntimeEvent)
				}
			}
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
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent != "" && !binding.Compatibility {
				events = append(events, binding.RuntimeEvent)
			}
		}
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
