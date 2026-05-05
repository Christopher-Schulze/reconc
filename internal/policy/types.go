// Package policy defines the core data types shared by the ingest,
// parser, compiler, and runtime layers of reconc.
//
// The types here define the policy schema using Go idioms: typed string
// aliases for enums, struct tags for JSON/YAML, and slices for ordered data.
//
// Everything in this package is pure data - no IO, no globals.
package policy

import "fmt"

// Mode controls how a rule's outcome is reported and enforced.
//
//   - observe: record only, never fails
//   - warn:    report but non-blocking (exit 0)
//   - block:   blocking violation (exit 2)
//   - fix:     blocking violation + remediation-plan signal (exit 2)
type Mode string

const (
	ModeObserve Mode = "observe"
	ModeWarn    Mode = "warn"
	ModeBlock   Mode = "block"
	ModeFix     Mode = "fix"
)

// AllModes returns every supported rule mode in declaration order.
func AllModes() []Mode {
	return []Mode{ModeObserve, ModeWarn, ModeBlock, ModeFix}
}

// IsBlocking reports whether a mode produces an exit-2 verdict.
func (m Mode) IsBlocking() bool {
	return m == ModeBlock || m == ModeFix
}

// Valid reports whether m is a recognized mode.
func (m Mode) Valid() bool {
	for _, x := range AllModes() {
		if m == x {
			return true
		}
	}
	return false
}

// Kind identifies the semantic class of a rule.
//
// The kinds below are the core rule kinds plus native reconc extensions such
// as require_script, require_evidence, require_fresh_file, and all_of.
type Kind string

const (
	KindDenyWrite             Kind = "deny_write"
	KindRequireRead           Kind = "require_read"
	KindRequireCommand        Kind = "require_command"
	KindRequireCommandSuccess Kind = "require_command_success"
	KindForbidCommand         Kind = "forbid_command"
	KindCoupleChange          Kind = "couple_change"
	KindRequireClaim          Kind = "require_claim"

	// Phase 4A: filesystem-evidence rule kinds (W22).
	KindRequireFreshFile Kind = "require_fresh_file"
	KindRequireEvidence  Kind = "require_evidence"

	// Phase 4C: composite rule kinds (W26). Combine sub-checks via
	// boolean operators. Sub-checks use inline form, see Check below.
	KindAllOf Kind = "all_of"
	KindAnyOf Kind = "any_of"
	KindNot   Kind = "not"

	// Phase 4D: subprocess-based custom assertion (W21 + W28).
	// Runs an external script under timeout enforcement.
	KindRequireScript Kind = "require_script"
)

// AllKinds returns every supported rule kind in declaration order.
func AllKinds() []Kind {
	return []Kind{
		KindDenyWrite,
		KindRequireRead,
		KindRequireCommand,
		KindRequireCommandSuccess,
		KindForbidCommand,
		KindCoupleChange,
		KindRequireClaim,
		KindRequireFreshFile,
		KindRequireEvidence,
		KindAllOf,
		KindAnyOf,
		KindNot,
		KindRequireScript,
	}
}

// IsComposite reports whether kind is a meta rule kind (all_of /
// any_of / not) that combines other checks rather than evaluating
// evidence directly.
func (k Kind) IsComposite() bool {
	return k == KindAllOf || k == KindAnyOf || k == KindNot
}

// Valid reports whether k is a recognized kind.
func (k Kind) Valid() bool {
	for _, x := range AllKinds() {
		if k == x {
			return true
		}
	}
	return false
}

// Rule is a single parsed, validated policy rule. Field presence depends
// on Kind; the parser layer validates that required fields are populated
// for each kind.
//
// JSON field names are stable for lockfile compatibility. Omitempty is used
// so a rule that doesn't need a field doesn't emit the empty key.
type Rule struct {
	ID          string   `json:"id" yaml:"id"`
	Kind        Kind     `json:"kind" yaml:"kind"`
	Mode        Mode     `json:"mode" yaml:"mode"`
	Message     string   `json:"message" yaml:"message"`
	Paths       []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	BeforePaths []string `json:"before_paths,omitempty" yaml:"before_paths,omitempty"`
	WhenPaths   []string `json:"when_paths,omitempty" yaml:"when_paths,omitempty"`
	Commands    []string `json:"commands,omitempty" yaml:"commands,omitempty"`
	Claims      []string `json:"claims,omitempty" yaml:"claims,omitempty"`

	// Phase 4A (W22) fields: filesystem-evidence checks.
	RequiredFiles []RequiredFile  `json:"required_files,omitempty" yaml:"required_files,omitempty"`
	Evidence      []EvidenceCheck `json:"evidence,omitempty" yaml:"evidence,omitempty"`

	// Phase 4C (W26) fields: composite rule sub-checks. Used by
	// kinds all_of / any_of / not. Each Check uses INLINE form:
	// `path: x` instead of `required_files: [{path: x}]`, etc.
	Checks []Check `json:"checks,omitempty" yaml:"checks,omitempty"`

	// Phase 4D (W21+W28) fields: require_script subprocess execution.
	//
	// Script is the repo-relative path to an executable (no absolute
	// paths, no `..` escapes). Args are passed verbatim (post template
	// substitution) as exec args. TimeoutSec defaults to 60 when zero;
	// hard-capped by global config.
	Script         string   `json:"script,omitempty" yaml:"script,omitempty"`
	Args           []string `json:"args,omitempty" yaml:"args,omitempty"`
	TimeoutSec     int      `json:"timeout_sec,omitempty" yaml:"timeout_sec,omitempty"`
	KillTimeoutSec int      `json:"kill_timeout_sec,omitempty" yaml:"kill_timeout_sec,omitempty"`

	// Provenance: where did this rule come from? Set by the parser so
	// violations can point back to the authoring location.
	SourcePath    string `json:"source_path,omitempty" yaml:"-"`
	SourceBlockID string `json:"source_block_id,omitempty" yaml:"-"`

	// Phase 5L (W31) fields: rule deprecation lifecycle. Let rule
	// authors mark an old rule as deprecated without removing it, so
	// it can stay in the lockfile for back-compat while users migrate.
	//
	// Deprecated rules still evaluate (their decisions still fire), but
	// reconc emits a warning at compile time and doctor lists them in
	// a dedicated section. DeprecatedReplacedBy points at the
	// successor rule's id when migration is direct.
	Deprecated           bool   `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	DeprecatedReason     string `json:"deprecated_reason,omitempty" yaml:"deprecated_reason,omitempty"`
	DeprecatedSince      string `json:"deprecated_since,omitempty" yaml:"deprecated_since,omitempty"`
	DeprecatedReplacedBy string `json:"deprecated_replaced_by,omitempty" yaml:"deprecated_replaced_by,omitempty"`

	// Phase 5M (W17) field: monorepo scoping. When non-empty, the rule
	// only fires when at least one input path matches one of these
	// glob patterns. Set by the parser when the rule is declared
	// inside a `scopes:` block in .reconc.yml. Empty = global rule
	// (current default behaviour, applies to all input paths).
	ScopePaths []string `json:"scope_paths,omitempty" yaml:"scope_paths,omitempty"`
	// ScopeID names the scope this rule was declared in; empty for
	// global rules. Useful for `reconc why` output and audit log
	// filtering on a per-scope basis.
	ScopeID string `json:"scope_id,omitempty" yaml:"scope_id,omitempty"`
}

// RequiredFile is one file-presence-and-freshness assertion used by the
// require_fresh_file rule kind.
//
// MaxAgeHours = 0 means "no freshness requirement" (just existence).
// Optional skips the entry silently if the file is absent (useful for
// pipeline outputs that may not exist on early-stage tasks).
type RequiredFile struct {
	Path        string `json:"path" yaml:"path"`
	MaxAgeHours int    `json:"max_age_hours,omitempty" yaml:"max_age_hours,omitempty"`
	Optional    bool   `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// EvidenceCheck is one filesystem assertion used by the require_evidence
// rule kind. At least one of MustExist/MustContain/MustNotContain/
// MaxLineCount must be specified for the check to be meaningful;
// the parser validates this.
//
// Semantics:
//   - MustExist:      file must exist (regular file, not dir/symlink target absent)
//   - MustContain:    file content must contain ALL listed substrings
//   - MustNotContain: file content must NOT contain this substring
//   - MaxLineCount:   file's line count (separator-counted) must be <= this
//   - Optional:       skip the check if the file doesn't exist
type EvidenceCheck struct {
	File           string   `json:"file" yaml:"file"`
	MustExist      bool     `json:"must_exist,omitempty" yaml:"must_exist,omitempty"`
	MustContain    []string `json:"must_contain,omitempty" yaml:"must_contain,omitempty"`
	MustNotContain string   `json:"must_not_contain,omitempty" yaml:"must_not_contain,omitempty"`
	MaxLineCount   int      `json:"max_line_count,omitempty" yaml:"max_line_count,omitempty"`
	Optional       bool     `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// Check is one INLINE sub-check inside a composite rule (all_of /
// any_of / not).
//
// Differs from a top-level Rule:
//   - No id / mode / message / source_path (composite rule owns those)
//   - File-evidence kinds use INLINE single-value fields:
//     require_fresh_file: `path` + `max_age_hours` + `optional`
//     (NOT required_files: [{...}])
//     require_evidence: `file` + `must_exist` + `must_contain` + ...
//     (NOT evidence: [{...}])
//   - require_script: `script` + `args` + `timeout_sec` (Phase 4D)
//   - Other kinds (require_claim/require_command/etc.) reuse the
//     usual list fields (claims, commands, paths, etc.)
//
// All template-variable substitution is applied at evaluation time
// using the parent composite rule's when_paths captures.
type Check struct {
	Kind Kind `json:"kind" yaml:"kind"`

	// require_fresh_file inline form
	Path        string `json:"path,omitempty" yaml:"path,omitempty"`
	MaxAgeHours int    `json:"max_age_hours,omitempty" yaml:"max_age_hours,omitempty"`

	// require_evidence inline form
	File           string   `json:"file,omitempty" yaml:"file,omitempty"`
	MustExist      bool     `json:"must_exist,omitempty" yaml:"must_exist,omitempty"`
	MustContain    []string `json:"must_contain,omitempty" yaml:"must_contain,omitempty"`
	MustNotContain string   `json:"must_not_contain,omitempty" yaml:"must_not_contain,omitempty"`
	MaxLineCount   int      `json:"max_line_count,omitempty" yaml:"max_line_count,omitempty"`

	// require_script (Phase 4D)
	Script     string   `json:"script,omitempty" yaml:"script,omitempty"`
	Args       []string `json:"args,omitempty" yaml:"args,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty" yaml:"timeout_sec,omitempty"`

	// Shared list-shaped fields (require_claim, require_command,
	// forbid_command, deny_write, require_read, couple_change)
	Paths       []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	BeforePaths []string `json:"before_paths,omitempty" yaml:"before_paths,omitempty"`
	WhenPaths   []string `json:"when_paths,omitempty" yaml:"when_paths,omitempty"`
	Commands    []string `json:"commands,omitempty" yaml:"commands,omitempty"`
	Claims      []string `json:"claims,omitempty" yaml:"claims,omitempty"`

	// Optional skips the check on a soft failure (e.g. file missing
	// when it's a source-of-information lookup, not an assertion).
	Optional bool `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// String returns a compact human-readable rule identifier for diagnostics.
func (r Rule) String() string {
	return fmt.Sprintf("%s (%s, %s)", r.ID, r.Kind, r.Mode)
}

// SourceKind tags the origin of a PolicySource so the compiler can order
// and digest sources deterministically.
//
// Order is lowest-precedence first:
//
//	global -> claude_md -> agents_md -> start_md -> inline_block ->
//	compiler_config -> preset -> policy_file
type SourceKind string

const (
	SourceGlobal         SourceKind = "global"
	SourceClaudeMD       SourceKind = "claude_md"
	SourceAgentsMD       SourceKind = "agents_md"
	SourceStartMD        SourceKind = "start_md"
	SourceInlineBlock    SourceKind = "inline_block"
	SourceCompilerConfig SourceKind = "compiler_config"
	SourcePreset         SourceKind = "preset"
	SourcePolicyFile     SourceKind = "policy_file"
)

// SourcePrecedence is the canonical order in which sources contribute to
// the compiled lockfile. A source's precedence determines tie-breaking
// when multiple sources target the same rule id (higher precedence wins,
// with an error on true duplicate ids within the same precedence level).
func SourcePrecedence() []SourceKind {
	return []SourceKind{
		SourceGlobal,
		SourceClaudeMD,
		SourceAgentsMD,
		SourceStartMD,
		SourceInlineBlock,
		SourceCompilerConfig,
		SourcePreset,
		SourcePolicyFile,
	}
}

// PolicySource is one canonical policy input with preserved provenance.
// Content is the raw YAML or markdown text; the parser layer decodes it.
type PolicySource struct {
	Kind      SourceKind `json:"kind"`
	Path      string     `json:"path"`
	Content   string     `json:"content"`
	BlockID   string     `json:"block_id,omitempty"`
	LineStart int        `json:"line_start,omitempty"`
}
