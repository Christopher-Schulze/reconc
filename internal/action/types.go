// Package action owns Reconc's canonical, transport-neutral Action Plane
// contract. The package is pure: it performs no filesystem, process, network,
// clock, approval, state, or ledger IO.
package action

import (
	"net/netip"
	"regexp"
)

const (
	PlanFormatVersion       = "1"
	MaxArgumentBytes        = 8 << 20
	MaxToolNameBytes        = 256
	MaxGatewayToolNameBytes = 128
	MaxPointerBytes         = 1024
	MaxJSONDepth            = 32
	MaxJSONItems            = 65536
	MaxJSONStringBytes      = 4 << 20
	MaxNumberLexemeBytes    = 1024
	MaxNumberDigits         = 768
	MaxNumberExponent       = 100000
	MaxConditionDepth       = 16
	MaxConditionNodes       = 1024
	MaxCompiledNodes        = 262144
	MaxListValues           = 256
	MaxPatternBytes         = 4096
	MaxSafeLabelBytes       = 64
	MaxRuleMessageBytes     = 512
	MaxRules                = 4096
	MaxTools                = 512
	MaxBudgets              = 1024
	MaxApprovalDisclosures  = 1024
	MaxDetectors            = 1024
	MaxLedgerFields         = 256
	MaxDetectorFields       = 256
	MaxDetectorCategories   = 32
	MaxForbiddenTerms       = 256
	MaxConcurrentCalls      = 4
	MaxCompiledPlanBytes    = 24 << 20
)

type Transport string

const (
	TransportHostMCP  Transport = "host_mcp"
	TransportMCPStdio Transport = "mcp_stdio"
)

func (t Transport) Valid() bool {
	return t == TransportHostMCP || t == TransportMCPStdio
}

type Platform string

const (
	PlatformClaudeCode Platform = "claude-code"
	PlatformCodex      Platform = "codex"
	PlatformCursor     Platform = "cursor"
	PlatformOpenCode   Platform = "opencode"
	PlatformKilo       Platform = "kilo"
	PlatformOMP        Platform = "omp"
	PlatformPi         Platform = "pi"
	PlatformZCode      Platform = "zcode"
)

func BuiltinPlatforms() []Platform {
	return []Platform{
		PlatformClaudeCode,
		PlatformCodex,
		PlatformCursor,
		PlatformOpenCode,
		PlatformKilo,
		PlatformOMP,
		PlatformPi,
		PlatformZCode,
	}
}

type EffectKind string

const (
	EffectRepositoryRead  EffectKind = "repository_read"
	EffectRepositoryWrite EffectKind = "repository_write"
	EffectCommand         EffectKind = "command"
	EffectExternal        EffectKind = "external"
)

func (k EffectKind) Valid() bool {
	switch k {
	case EffectRepositoryRead, EffectRepositoryWrite, EffectCommand, EffectExternal:
		return true
	default:
		return false
	}
}

type Decision string

const (
	DecisionAllow           Decision = "allow"
	DecisionWarn            Decision = "warn"
	DecisionRequireApproval Decision = "require_approval"
	DecisionBlock           Decision = "block"
)

func (d Decision) Valid() bool {
	switch d {
	case DecisionAllow, DecisionWarn, DecisionRequireApproval, DecisionBlock:
		return true
	default:
		return false
	}
}

func (d Decision) Strength() int {
	switch d {
	case DecisionBlock:
		return 3
	case DecisionRequireApproval:
		return 2
	case DecisionWarn:
		return 1
	case DecisionAllow:
		return 0
	default:
		return -1
	}
}

type Phase string

const (
	PhasePreCall     Phase = "pre_call"
	PhasePostResult  Phase = "post_result"
	PhaseProgress    Phase = "progress"
	PhaseObservation Phase = "observation"
)

func AllPhases() []Phase {
	return []Phase{PhasePreCall, PhasePostResult, PhaseProgress, PhaseObservation}
}

func (p Phase) Valid() bool {
	for _, candidate := range AllPhases() {
		if p == candidate {
			return true
		}
	}
	return false
}

type CachePolicy string

const (
	CacheExact CachePolicy = "exact"
	CacheNever CachePolicy = "never"
)

func (p CachePolicy) Valid() bool {
	return p == CacheExact || p == CacheNever
}

type Origin string

const (
	OriginActions   Origin = "actions"
	OriginLegacyMCP Origin = "legacy_mcp"
)

func (o Origin) Valid() bool {
	return o == OriginActions || o == OriginLegacyMCP
}

type Provenance string

const (
	ProvenanceAgentSupplied   Provenance = "agent_supplied"
	ProvenanceAdapterAsserted Provenance = "adapter_asserted"
	ProvenanceHostObserved    Provenance = "host_observed"
	ProvenanceOperatorBound   Provenance = "operator_bound"
)

func (p Provenance) Rank() int {
	switch p {
	case ProvenanceAgentSupplied:
		return 0
	case ProvenanceAdapterAsserted:
		return 1
	case ProvenanceHostObserved:
		return 2
	case ProvenanceOperatorBound:
		return 3
	default:
		return -1
	}
}

func (p Provenance) Valid() bool {
	return p.Rank() >= 0
}

type ValueSource string

const (
	SourceArguments ValueSource = "arguments"
	SourceContext   ValueSource = "context"
	SourceResult    ValueSource = "result"
	SourceProgress  ValueSource = "progress"
)

func (s ValueSource) Valid() bool {
	switch s {
	case SourceArguments, SourceContext, SourceResult, SourceProgress:
		return true
	default:
		return false
	}
}

type Operator string

const (
	OperatorExists     Operator = "exists"
	OperatorEqual      Operator = "eq"
	OperatorNotEqual   Operator = "neq"
	OperatorIn         Operator = "in"
	OperatorNotIn      Operator = "not_in"
	OperatorPrefix     Operator = "prefix"
	OperatorSuffix     Operator = "suffix"
	OperatorContains   Operator = "contains"
	OperatorGlob       Operator = "glob"
	OperatorRegex      Operator = "regex"
	OperatorGreater    Operator = "gt"
	OperatorGreaterEq  Operator = "gte"
	OperatorLess       Operator = "lt"
	OperatorLessEq     Operator = "lte"
	OperatorURL        Operator = "url"
	OperatorCIDR       Operator = "cidr"
	OperatorPathWithin Operator = "path_within"
)

func (o Operator) Valid() bool {
	switch o {
	case OperatorExists, OperatorEqual, OperatorNotEqual, OperatorIn,
		OperatorNotIn, OperatorPrefix, OperatorSuffix, OperatorContains,
		OperatorGlob, OperatorRegex, OperatorGreater, OperatorGreaterEq,
		OperatorLess, OperatorLessEq, OperatorURL, OperatorCIDR,
		OperatorPathWithin:
		return true
	default:
		return false
	}
}

type Effect struct {
	Kind         EffectKind `json:"kind"`
	PathFields   []string   `json:"path_fields,omitempty"`
	CommandField string     `json:"command_field,omitempty"`
}

type Tool struct {
	ID                string    `json:"id"`
	Transport         Transport `json:"transport"`
	Platform          Platform  `json:"platform,omitempty"`
	ServerLabel       string    `json:"server_label,omitempty"`
	ServerFingerprint string    `json:"server_fingerprint,omitempty"`
	Tool              string    `json:"tool"`
	Effect            Effect    `json:"effect"`
	CostUnits         *uint64   `json:"cost_units,omitempty"`
	// MaxResultBytes is zero only when the serialized field is absent. A
	// present policy or lock value is between 1 and MaxArgumentBytes.
	MaxResultBytes uint64 `json:"max_result_bytes,omitempty"`
	LedgerNameSafe bool   `json:"ledger_name_safe,omitempty"`
	Origin         Origin `json:"origin"`
	SourceIdentity string `json:"source_identity"`
}

type Defaults struct {
	DeclaredTool     Decision    `json:"declared_tool"`
	GatewayUnmatched Decision    `json:"gateway_unmatched"`
	HostUnmatched    Decision    `json:"host_unmatched"`
	EvaluationError  Decision    `json:"evaluation_error"`
	PostError        Decision    `json:"post_error"`
	ProgressError    Decision    `json:"progress_error"`
	Cache            CachePolicy `json:"cache"`
}

func FrozenDefaults() Defaults {
	return Defaults{
		DeclaredTool: DecisionAllow, GatewayUnmatched: DecisionBlock,
		HostUnmatched: DecisionAllow, EvaluationError: DecisionBlock,
		PostError: DecisionBlock, ProgressError: DecisionBlock,
		Cache: CacheExact,
	}
}

type Selector struct {
	ToolIDs             []string    `json:"tool_ids,omitempty"`
	Transports          []Transport `json:"transports,omitempty"`
	Platforms           []Platform  `json:"platforms,omitempty"`
	ServerLabels        []string    `json:"server_labels,omitempty"`
	ServerFingerprints  []string    `json:"server_fingerprints,omitempty"`
	Tools               []string    `json:"tools,omitempty"`
	ToolContractDigests []string    `json:"tool_contract_digests,omitempty"`
	Phases              []Phase     `json:"phases,omitempty"`
}

type Condition struct {
	All       []Condition `json:"all,omitempty"`
	Any       []Condition `json:"any,omitempty"`
	Not       *Condition  `json:"not,omitempty"`
	Predicate *Predicate  `json:"predicate,omitempty"`
}

type Predicate struct {
	Source            ValueSource `json:"source"`
	Pointer           string      `json:"pointer"`
	MinimumProvenance Provenance  `json:"minimum_provenance,omitempty"`
	Op                Operator    `json:"op"`
	Value             *Value      `json:"value,omitempty"`
}

type Rule struct {
	ID              string      `json:"id"`
	Selector        Selector    `json:"selector"`
	When            *Condition  `json:"when,omitempty"`
	Decision        Decision    `json:"decision"`
	OnIndeterminate Decision    `json:"on_indeterminate"`
	Cache           CachePolicy `json:"cache"`
	Message         string      `json:"message,omitempty"`
	SourceIdentity  string      `json:"source_identity"`
}

type BudgetReset string

const (
	BudgetResetNever           BudgetReset = "never"
	BudgetResetOperatorRun     BudgetReset = "operator_run"
	BudgetResetOperatorSession BudgetReset = "operator_session"
	BudgetResetFixedWindow     BudgetReset = "fixed_window"
)

func (r BudgetReset) Valid() bool {
	switch r {
	case BudgetResetNever, BudgetResetOperatorRun, BudgetResetOperatorSession,
		BudgetResetFixedWindow:
		return true
	default:
		return false
	}
}

// BudgetLimits is the closed cumulative-capacity vocabulary. Zero means that
// the dimension is absent; every present authoring value is strictly positive.
type BudgetLimits struct {
	CallCount     uint64 `json:"call_count,omitempty"`
	DeniedCount   uint64 `json:"denied_count,omitempty"`
	ApprovalCount uint64 `json:"approval_count,omitempty"`
	ArgumentBytes uint64 `json:"argument_bytes,omitempty"`
	ResultBytes   uint64 `json:"result_bytes,omitempty"`
	CostUnits     uint64 `json:"cost_units,omitempty"`
	Concurrent    uint64 `json:"concurrent,omitempty"`
	RateWindow    uint64 `json:"rate_window,omitempty"`
}

func (l BudgetLimits) Empty() bool {
	return l == (BudgetLimits{})
}

type Budget struct {
	ID             string       `json:"id"`
	Selector       Selector     `json:"selector"`
	Limits         BudgetLimits `json:"limits"`
	Reset          BudgetReset  `json:"reset"`
	WindowSeconds  uint32       `json:"window_seconds,omitempty"`
	OnExhaustion   Decision     `json:"on_exhaustion"`
	SourceIdentity string       `json:"source_identity"`
}

// ApprovalDisclosure selects only the argument fields whose keyed summaries
// may be rendered to an external approval authority. It never selects or
// defines authority keys; those remain operator-owned configuration.
type ApprovalDisclosure struct {
	ID                string   `json:"id"`
	Selector          Selector `json:"selector"`
	SelectedArguments []string `json:"selected_arguments"`
	SourceIdentity    string   `json:"source_identity"`
}

type LedgerMode string

const (
	LedgerRequired   LedgerMode = "required"
	LedgerBestEffort LedgerMode = "best_effort"
	LedgerOff        LedgerMode = "off"
)

func (m LedgerMode) Valid() bool {
	return m == LedgerRequired || m == LedgerBestEffort || m == LedgerOff
}

type LedgerToolIdentity string

const (
	LedgerDeclarationID LedgerToolIdentity = "declaration_id"
	LedgerExactName     LedgerToolIdentity = "exact_name"
	LedgerKeyedName     LedgerToolIdentity = "keyed_name"
)

func (i LedgerToolIdentity) Valid() bool {
	return i == LedgerDeclarationID || i == LedgerExactName || i == LedgerKeyedName
}

type LedgerField struct {
	Source  ValueSource `json:"source"`
	Pointer string      `json:"pointer"`
}

type LedgerPolicy struct {
	Mode           LedgerMode         `json:"mode"`
	ToolIdentity   LedgerToolIdentity `json:"tool_identity"`
	SelectedFields []LedgerField      `json:"selected_fields"`
}

type Plan struct {
	FormatVersion string               `json:"format_version"`
	Tools         []Tool               `json:"tools"`
	Rules         []Rule               `json:"rules"`
	Budgets       []Budget             `json:"budgets"`
	Approvals     []ApprovalDisclosure `json:"approvals"`
	Detectors     []DetectorPolicy     `json:"detectors"`
	Ledger        *LedgerPolicy        `json:"ledger,omitempty"`
	Defaults      Defaults             `json:"defaults"`
}

// CompiledPlan is the immutable runtime-owned form. Plan returns a defensive
// copy; matcher and pointer programs are never serialized into the lock.
type CompiledPlan struct {
	plan        Plan
	toolByID    map[string]int
	toolByExact map[string]int
	rules       []CompiledRule
	budgets     []Budget
	approvals   []ApprovalDisclosure
	detectors   []CompiledDetectorPolicy
}

type CompiledRule struct {
	Rule      Rule
	Condition *CompiledCondition
}

type CompiledCondition struct {
	Kind      ConditionKind
	Children  []*CompiledCondition
	Predicate *CompiledPredicate
}

type ConditionKind string

const (
	ConditionAll       ConditionKind = "all"
	ConditionAny       ConditionKind = "any"
	ConditionNot       ConditionKind = "not"
	ConditionPredicate ConditionKind = "predicate"
)

type CompiledPredicate struct {
	Predicate Predicate
	Tokens    []string
	Regex     *regexp.Regexp
	Glob      *CompiledGlob
	URL       *URLConstraint
	CIDRs     []netip.Prefix
	Path      *PathConstraint
}

// CompiledGlob owns immutable matcher programs compiled from validated
// doublestar syntax. Match never parses or consults policy source again.
type CompiledGlob struct {
	pattern      string
	programs     []globProgram
	logicalBytes int
}

type URLConstraint struct {
	Schemes        []string
	Hosts          []string
	Ports          []uint16
	PathPrefixes   []string
	AllowQuery     bool
	AllowIPLiteral bool
}

type PathStyle string

const (
	PathRepository PathStyle = "repository"
	PathPOSIX      PathStyle = "posix"
	PathWindows    PathStyle = "windows"
)

type PathConstraint struct {
	Style         PathStyle
	Base          string
	CaseSensitive bool
	matchBase     string
	matchVolume   string
	matchPrefix   string
	prepared      bool
}
