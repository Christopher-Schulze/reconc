package bootstrap

import "reconc.dev/reconc/internal/commandproof"

const (
	RepositoryReceiptFormatVersion = "1"
	RepositoryReceiptRelativePath  = ".reconc/install.lock.json"
	SyncPlanFormatVersion          = "reconc.repository-sync-plan/v1"
	SyncReportFormatVersion        = "reconc.repository-sync-report/v1"
	SyncVerifyFormatVersion        = "reconc.repository-sync-verify/v1"
	SyncRecoveryFormatVersion      = "reconc.repository-sync-recovery/v1"
	SyncResolutionFormatVersion    = "reconc.repository-sync-resolution/v1"
)

type PolicyPackIdentity struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type HarnessPackIdentity struct {
	Name                    string `json:"name"`
	Version                 string `json:"version"`
	MinimumProductVersion   string `json:"minimum_product_version"`
	MaximumExclusiveVersion string `json:"maximum_exclusive_version"`
	Digest                  string `json:"digest"`
}

type ManagedFile struct {
	Path      string `json:"path"`
	Mode      uint32 `json:"mode"`
	SHA256    string `json:"sha256"`
	Component string `json:"component"`
	Ownership string `json:"ownership"`
}

type ManagedBlock struct {
	Path            string `json:"path"`
	BlockStart      string `json:"block_start"`
	BlockEnd        string `json:"block_end"`
	ManagedSHA256   string `json:"managed_sha256"`
	WholeFileSHA256 string `json:"whole_file_sha256"`
	Component       string `json:"component"`
}

type GeneratedArtifact struct {
	Path      string `json:"path"`
	Generator string `json:"generator"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
}

type RepositoryReceipt struct {
	Schema             string                `json:"$schema"`
	FormatVersion      string                `json:"format_version"`
	ProductVersion     string                `json:"product_version"`
	Profile            ProfileName           `json:"profile"`
	PolicyPacks        []PolicyPackIdentity  `json:"policy_packs"`
	HarnessPacks       []HarnessPackIdentity `json:"harness_packs"`
	Hooks              []string              `json:"hooks"`
	PolicySources      []string              `json:"policy_sources"`
	ManagedFiles       []ManagedFile         `json:"managed_files"`
	ManagedBlocks      []ManagedBlock        `json:"managed_blocks"`
	GeneratedArtifacts []GeneratedArtifact   `json:"generated_artifacts"`
	UserOwnedPaths     []string              `json:"user_owned_paths"`
	PlanDigest         string                `json:"plan_digest"`
	Generation         uint64                `json:"generation"`
	ReceiptDigest      string                `json:"receipt_digest"`
}

type SyncActionState string

const (
	SyncUnchanged          SyncActionState = "unchanged"
	SyncReplaceOwned       SyncActionState = "replace-owned"
	SyncUpdateManagedBlock SyncActionState = "update-managed-block"
	SyncCreateOwned        SyncActionState = "create-owned"
	SyncUserDrift          SyncActionState = "user-drift"
	SyncOrphanedLegacy     SyncActionState = "orphaned-legacy"
	SyncIncompatible       SyncActionState = "incompatible"
	SyncManualReview       SyncActionState = "manual-review"
)

type SyncAction struct {
	Component     string          `json:"component"`
	Path          string          `json:"path"`
	Mode          uint32          `json:"mode"`
	State         SyncActionState `json:"state"`
	CurrentSHA256 string          `json:"current_sha256,omitempty"`
	ReceiptSHA256 string          `json:"receipt_sha256,omitempty"`
	DesiredSHA256 string          `json:"desired_sha256,omitempty"`
	CurrentMode   uint32          `json:"current_mode,omitempty"`
	CandidatePath string          `json:"candidate_path,omitempty"`
	Reason        string          `json:"reason"`
}

type SyncMigration struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path"`
}

type SyncPlan struct {
	Schema                string                 `json:"$schema"`
	FormatVersion         string                 `json:"format_version"`
	RepoRoot              string                 `json:"repo_root"`
	CurrentProductVersion string                 `json:"current_product_version"`
	TargetProductVersion  string                 `json:"target_product_version"`
	CurrentReceiptDigest  string                 `json:"current_receipt_digest"`
	LegacyReceiptImport   bool                   `json:"legacy_receipt_import"`
	GitSnapshot           *commandproof.Snapshot `json:"git_snapshot"`
	CurrentPolicyPacks    []PolicyPackIdentity   `json:"current_policy_packs"`
	CurrentHarnessPacks   []HarnessPackIdentity  `json:"current_harness_packs"`
	TargetPolicyPacks     []PolicyPackIdentity   `json:"target_policy_packs"`
	TargetHarnessPacks    []HarnessPackIdentity  `json:"target_harness_packs"`
	Actions               []SyncAction           `json:"actions"`
	Migrations            []SyncMigration        `json:"migrations"`
	Candidates            []string               `json:"candidates"`
	BlockingIssues        []string               `json:"blocking_issues"`
	PlanDigest            string                 `json:"plan_digest"`
}

type SyncStatus string

const (
	SyncComplete   SyncStatus = "complete"
	SyncRefused    SyncStatus = "refused"
	SyncRolledBack SyncStatus = "rolled-back"
)

type SyncReport struct {
	Schema        string          `json:"$schema"`
	FormatVersion string          `json:"format_version"`
	RepoRoot      string          `json:"repo_root"`
	Status        SyncStatus      `json:"status"`
	PlanDigest    string          `json:"plan_digest"`
	ProductFrom   string          `json:"product_from"`
	ProductTo     string          `json:"product_to"`
	ReceiptFrom   string          `json:"receipt_from"`
	ReceiptTo     string          `json:"receipt_to,omitempty"`
	Changed       []string        `json:"changed"`
	Unchanged     []string        `json:"unchanged"`
	Candidates    []string        `json:"candidates"`
	Migrations    []SyncMigration `json:"migrations"`
	RolledBack    []string        `json:"rolled_back"`
	Verification  []Check         `json:"verification"`
	NextAction    string          `json:"next_action"`
}

type SyncVerification struct {
	Schema        string  `json:"$schema"`
	FormatVersion string  `json:"format_version"`
	RepoRoot      string  `json:"repo_root"`
	Valid         bool    `json:"valid"`
	ReceiptDigest string  `json:"receipt_digest,omitempty"`
	Checks        []Check `json:"checks"`
	NextAction    string  `json:"next_action"`
}

type SyncRecoveryStatus string

const (
	SyncRecoveryClean      SyncRecoveryStatus = "clean"
	SyncRecoveryFinalized  SyncRecoveryStatus = "finalized"
	SyncRecoveryRefused    SyncRecoveryStatus = "refused"
	SyncRecoveryRolledBack SyncRecoveryStatus = "rolled-back"
)

type SyncRecovery struct {
	Schema        string             `json:"$schema"`
	FormatVersion string             `json:"format_version"`
	RepoRoot      string             `json:"repo_root"`
	Status        SyncRecoveryStatus `json:"status"`
	PlanDigest    string             `json:"plan_digest,omitempty"`
	Restored      []string           `json:"restored"`
	Verification  []Check            `json:"verification"`
	NextAction    string             `json:"next_action"`
}

type SyncResolutionStrategy string

const (
	SyncKeepCurrent SyncResolutionStrategy = "keep-current"
	SyncUseTarget   SyncResolutionStrategy = "use-target"
	SyncUseBinary   SyncResolutionStrategy = "use-binary"
)

type SyncResolutionRequest struct {
	Plan        *SyncPlan
	ExactDigest string
	Path        string
	Strategy    SyncResolutionStrategy
	Binary      *BinarySelection
}

type SyncResolutionReport struct {
	Schema        string                 `json:"$schema"`
	FormatVersion string                 `json:"format_version"`
	RepoRoot      string                 `json:"repo_root"`
	Status        SyncStatus             `json:"status"`
	PlanDigest    string                 `json:"plan_digest"`
	Path          string                 `json:"path"`
	Strategy      SyncResolutionStrategy `json:"strategy"`
	Changed       []string               `json:"changed"`
	RolledBack    []string               `json:"rolled_back"`
	Verification  []Check                `json:"verification"`
	NextAction    string                 `json:"next_action"`
}
