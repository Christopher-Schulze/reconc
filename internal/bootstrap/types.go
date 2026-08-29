// Package bootstrap plans, applies, and verifies non-destructive repository
// onboarding transactions.
package bootstrap

const (
	InspectFormatVersion = "reconc.bootstrap.inspect/v2"
	PlanFormatVersion    = "reconc.bootstrap.plan/v1"
	ReportFormatVersion  = "reconc.bootstrap.report/v1"
	RemovalFormatVersion = "reconc.bootstrap.remove/v1"
	VerifyFormatVersion  = "reconc.bootstrap.verify/v1"
	InitFormatVersion    = "reconc.init/v1"
)

type ProfileName string

const (
	ProfileMinimal  ProfileName = "minimal"
	ProfileGoverned ProfileName = "governed"
	ProfileExisting ProfileName = "existing"
	ProfileAdvanced ProfileName = "advanced"
)

type Profile struct {
	Name         ProfileName `json:"name"`
	Summary      string      `json:"summary"`
	Policy       bool        `json:"policy"`
	AgentDoc     bool        `json:"agent_doc"`
	Tasks        bool        `json:"tasks"`
	Docs         bool        `json:"docs"`
	Ignores      bool        `json:"ignores"`
	Wrapper      bool        `json:"wrapper"`
	DefaultPacks []string    `json:"default_packs"`
}

func Profiles() []Profile {
	return []Profile{
		{
			Name: ProfileGoverned, Summary: "Policy, AI orientation, TASK control plane, docs, runtime ignores, and repo-local wrapper.",
			Policy: true, AgentDoc: true, Tasks: true, Docs: true, Ignores: true, Wrapper: true,
			DefaultPacks: []string{"default", "agent"},
		},
		{
			Name: ProfileMinimal, Summary: "Policy, compact AI orientation, and runtime ignores.",
			Policy: true, AgentDoc: true, Ignores: true, DefaultPacks: []string{"default", "agent"},
		},
		{
			Name: ProfileExisting, Summary: "Hooks, repo-local wrapper, and optional binary for a repository with existing fresh policy.",
			Wrapper: true, DefaultPacks: []string{},
		},
		{
			Name: ProfileAdvanced, Summary: "Complete governed control plane plus the embedded public advanced harness.",
			Policy: true, AgentDoc: true, Tasks: true, Docs: true, Ignores: true, Wrapper: true,
			DefaultPacks: []string{"default", "agent"},
		},
	}
}

type Request struct {
	RepoRoot             string
	Profile              ProfileName
	Packs                []string
	Hooks                []string
	Binary               *BinarySelection
	TrustExistingWrapper bool
}

type Selection struct {
	Profile              ProfileName            `json:"profile"`
	Packs                []string               `json:"packs"`
	HarnessPacks         []HarnessPackSelection `json:"harness_packs,omitempty"`
	Hooks                []string               `json:"hooks"`
	Binary               *BinarySelection       `json:"binary,omitempty"`
	TrustExistingWrapper bool                   `json:"trust_existing_wrapper,omitempty"`
}

type HarnessPackSelection struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type BinarySelection struct {
	SourcePath string `json:"source_path"`
	SHA256     string `json:"sha256"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

type Inspection struct {
	FormatVersion     string               `json:"format_version"`
	RepoRoot          string               `json:"repo_root"`
	DetectionState    string               `json:"detection_state"`
	DetectedStacks    []string             `json:"detected_stacks"`
	StackEvidence     []DetectionEvidence  `json:"stack_evidence"`
	PackageManagers   []DetectionEvidence  `json:"package_managers"`
	RepositoryMarkers []string             `json:"repository_markers"`
	Ambiguities       []string             `json:"ambiguities"`
	PackSuggestions   []string             `json:"pack_suggestions"`
	DetectedPlatforms []string             `json:"detected_platforms"`
	PlatformStatuses  []PlatformInspection `json:"platform_statuses"`
	ExistingPaths     []string             `json:"existing_paths"`
	BinaryResolution  ArtifactResolution   `json:"binary_resolution"`
}

type DetectionEvidence struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

type PlatformInspection struct {
	Kind        string   `json:"kind"`
	DisplayName string   `json:"display_name"`
	TargetPath  string   `json:"target_path"`
	State       string   `json:"state"`
	Detail      string   `json:"detail"`
	Evidence    []string `json:"evidence"`
	Generated   bool     `json:"generated"`
	Installed   bool     `json:"installed"`
	Executable  bool     `json:"executable"`
	Configured  bool     `json:"configured"`
	Remediation string   `json:"remediation,omitempty"`
}

type ArtifactResolution struct {
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	StableName string   `json:"stable_name,omitempty"`
	Path       string   `json:"path,omitempty"`
	Source     string   `json:"source,omitempty"`
	Candidates []string `json:"candidates"`
	Diagnostic string   `json:"diagnostic,omitempty"`
}

type ActionState string

const (
	ActionCreate    ActionState = "create"
	ActionUnchanged ActionState = "unchanged"
	ActionConflict  ActionState = "conflict"
)

type Action struct {
	Component      string      `json:"component"`
	Path           string      `json:"path"`
	Mode           uint32      `json:"mode"`
	DesiredSHA256  string      `json:"desired_sha256"`
	State          ActionState `json:"state"`
	ExistingKind   string      `json:"existing_kind"`
	ExistingSHA256 string      `json:"existing_sha256,omitempty"`
	ExistingMode   uint32      `json:"existing_mode,omitempty"`
	CandidatePath  string      `json:"candidate_path,omitempty"`
}

type Plan struct {
	FormatVersion   string    `json:"format_version"`
	ProductVersion  string    `json:"product_version"`
	RepoRoot        string    `json:"repo_root"`
	Selection       Selection `json:"selection"`
	Actions         []Action  `json:"actions"`
	CompileRequired bool      `json:"compile_required"`
	BlockingIssues  []string  `json:"blocking_issues"`
	PlanDigest      string    `json:"plan_digest"`
}

type ApplyStatus string

const (
	ApplyComplete   ApplyStatus = "complete"
	ApplyDrift      ApplyStatus = "drift"
	ApplyRolledBack ApplyStatus = "rolled_back"
)

type Report struct {
	FormatVersion string       `json:"format_version"`
	RepoRoot      string       `json:"repo_root"`
	PlanDigest    string       `json:"plan_digest"`
	Status        ApplyStatus  `json:"status"`
	Created       []string     `json:"created"`
	Unchanged     []string     `json:"unchanged"`
	Candidates    []string     `json:"candidates"`
	RolledBack    []string     `json:"rolled_back"`
	ReceiptPath   string       `json:"receipt_path,omitempty"`
	Summary       ApplySummary `json:"summary"`
	NextAction    string       `json:"next_action"`
}

type ApplySummary struct {
	Created       int  `json:"created"`
	Preserved     int  `json:"preserved"`
	Drifted       int  `json:"drifted"`
	Skipped       int  `json:"skipped"`
	Installed     int  `json:"installed"`
	Configured    int  `json:"configured"`
	Live          int  `json:"live"`
	LivenessKnown bool `json:"liveness_known"`
	// InspectionErrors carries non-fatal hook-status, liveness, or bootstrap
	// receipt-retention failures. When present, the zeroed counters above mean
	// "unknown", not "none", and the failures are surfaced instead of silently
	// swallowed.
	InspectionErrors []string `json:"inspection_errors,omitempty"`
}

type RemovalStatus string

const (
	RemovalComplete   RemovalStatus = "complete"
	RemovalDrift      RemovalStatus = "drift"
	RemovalRolledBack RemovalStatus = "rolled_back"
)

type RemovalReport struct {
	FormatVersion string        `json:"format_version"`
	RepoRoot      string        `json:"repo_root"`
	PlanDigest    string        `json:"plan_digest"`
	Status        RemovalStatus `json:"status"`
	ReceiptPath   string        `json:"receipt_path"`
	Removed       []string      `json:"removed"`
	Updated       []string      `json:"updated"`
	Preserved     []string      `json:"preserved"`
	Candidates    []string      `json:"candidates"`
	RolledBack    []string      `json:"rolled_back"`
	NextAction    string        `json:"next_action"`
}

type Verification struct {
	FormatVersion string  `json:"format_version"`
	RepoRoot      string  `json:"repo_root"`
	PlanDigest    string  `json:"plan_digest"`
	Valid         bool    `json:"valid"`
	Checks        []Check `json:"checks"`
	NextAction    string  `json:"next_action"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type InitStatus string

const (
	InitComplete   InitStatus = "complete"
	InitDrift      InitStatus = "drift"
	InitRefused    InitStatus = "refused"
	InitRolledBack InitStatus = "rolled_back"
)

// InitRequest is the complete non-interactive authorization for canonical
// repository onboarding. Empty profile and hook selection are resolved only
// through deterministic inspection or an existing transaction receipt.
type InitRequest struct {
	RepoRoot             string
	Profile              ProfileName
	Packs                []string
	Hooks                []string
	HooksExplicit        bool
	NoHooks              bool
	AcceptManagedBlocks  bool
	SkipGitHook          bool
	SkipAgentHooks       bool
	CompatibilityWarning []string
}

// InitReport is the stable result shared by text and JSON rendering.
type InitReport struct {
	FormatVersion  string                 `json:"format_version"`
	Operation      string                 `json:"operation"`
	Status         InitStatus             `json:"status"`
	Changed        bool                   `json:"changed"`
	Owner          *string                `json:"owner"`
	CurrentVersion string                 `json:"current_version"`
	TargetVersion  *string                `json:"target_version"`
	Channel        *string                `json:"channel"`
	BinaryPath     *string                `json:"binary_path"`
	ReceiptPath    *string                `json:"receipt_path"`
	PlanDigest     *string                `json:"plan_digest"`
	PlanPath       *string                `json:"plan_path"`
	RepoRoot       string                 `json:"repo_root"`
	Profile        ProfileName            `json:"profile"`
	PolicyPacks    []string               `json:"policy_packs"`
	HarnessPacks   []HarnessPackSelection `json:"harness_packs"`
	Hooks          []string               `json:"hooks"`
	Checks         []Check                `json:"checks"`
	Actions        []Action               `json:"actions"`
	Candidates     []string               `json:"candidates"`
	Warnings       []string               `json:"warnings"`
	NextAction     string                 `json:"next_action"`
}

type desiredArtifact struct {
	component  string
	path       string
	mode       uint32
	content    []byte
	sourcePath string
}
