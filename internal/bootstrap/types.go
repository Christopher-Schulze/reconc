// Package bootstrap plans, applies, and verifies non-destructive repository
// onboarding transactions.
package bootstrap

const (
	InspectFormatVersion = "reconc.bootstrap.inspect/v1"
	PlanFormatVersion    = "reconc.bootstrap.plan/v1"
	ReportFormatVersion  = "reconc.bootstrap.report/v1"
	VerifyFormatVersion  = "reconc.bootstrap.verify/v1"
)

type ProfileName string

const (
	ProfileMinimal  ProfileName = "minimal"
	ProfileGoverned ProfileName = "governed"
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
			Name: ProfileMinimal, Summary: "Policy and a compact AI orientation file only.",
			Policy: true, AgentDoc: true, DefaultPacks: []string{"default", "agent"},
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
	Profile              ProfileName      `json:"profile"`
	Packs                []string         `json:"packs"`
	Hooks                []string         `json:"hooks"`
	Binary               *BinarySelection `json:"binary,omitempty"`
	TrustExistingWrapper bool             `json:"trust_existing_wrapper,omitempty"`
}

type BinarySelection struct {
	SourcePath string `json:"source_path"`
	SHA256     string `json:"sha256"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

type Inspection struct {
	FormatVersion     string             `json:"format_version"`
	RepoRoot          string             `json:"repo_root"`
	DetectedStacks    []string           `json:"detected_stacks"`
	PackSuggestions   []string           `json:"pack_suggestions"`
	DetectedPlatforms []string           `json:"detected_platforms"`
	ExistingPaths     []string           `json:"existing_paths"`
	BinaryResolution  ArtifactResolution `json:"binary_resolution"`
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
	FormatVersion string      `json:"format_version"`
	RepoRoot      string      `json:"repo_root"`
	PlanDigest    string      `json:"plan_digest"`
	Status        ApplyStatus `json:"status"`
	Created       []string    `json:"created"`
	Unchanged     []string    `json:"unchanged"`
	Candidates    []string    `json:"candidates"`
	RolledBack    []string    `json:"rolled_back"`
	NextAction    string      `json:"next_action"`
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

type desiredArtifact struct {
	component  string
	path       string
	mode       uint32
	content    []byte
	sourcePath string
}
