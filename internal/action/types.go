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
	Origin            Origin    `json:"origin"`
	SourceIdentity    string    `json:"source_identity"`
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

type Plan struct {
	FormatVersion string   `json:"format_version"`
	Tools         []Tool   `json:"tools"`
	Rules         []Rule   `json:"rules"`
	Defaults      Defaults `json:"defaults"`
}

// CompiledPlan is the immutable runtime-owned form. Plan returns a defensive
// copy; matcher and pointer programs are never serialized into the lock.
type CompiledPlan struct {
	plan        Plan
	toolByID    map[string]int
	toolByExact map[string]int
	rules       []CompiledRule
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
}
