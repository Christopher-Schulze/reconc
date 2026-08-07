package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/customruntime"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
)

// Evaluator owns immutable compiled plans. A short-lived evaluator preserves
// the public one-shot behavior; the hook worker keeps one evaluator for its
// lifetime so unchanged repositories skip lock JSON decoding and plan build.
type Evaluator struct {
	mu    sync.Mutex
	plans map[string]runtimePlanCacheEntry
}

type runtimePlanCacheEntry struct {
	lockHash [sha256.Size]byte
	plan     *runtimePlan
}

type runtimePlan struct {
	defaultMode          policy.Mode
	rules                []policy.Rule
	ruleByID             map[string]int
	rulesByKind          map[policy.Kind][]int
	preCommandRules      []int
	sourceDigest         string
	sourceCount          int
	mcp                  *policy.MCPPolicy
	customRuntimeDigests map[string]string
}

type runtimeEnvelope struct {
	Schema           string                  `json:"$schema"`
	CompilerVersion  string                  `json:"compiler_version"`
	FormatVersion    string                  `json:"format_version"`
	RepoRoot         string                  `json:"repo_root"`
	DefaultMode      policy.Mode             `json:"default_mode"`
	RuleCount        int                     `json:"rule_count"`
	SourceCount      int                     `json:"source_count"`
	SourceDigest     string                  `json:"source_digest"`
	LockDigest       string                  `json:"lock_digest"`
	SourcePrecedence []policy.SourceKind     `json:"source_precedence"`
	Discovery        ingest.DiscoveryResult  `json:"discovery"`
	Sources          []runtimeSource         `json:"sources"`
	Rules            json.RawMessage         `json:"rules"`
	MCP              *policy.MCPPolicy       `json:"mcp,omitempty"`
	CustomRuntimes   []customruntime.Summary `json:"custom_runtimes,omitempty"`
}

type runtimeSource struct {
	Kind          policy.SourceKind `json:"kind"`
	Path          string            `json:"path"`
	ContentSHA256 string            `json:"content_sha256"`
	BlockID       string            `json:"block_id,omitempty"`
	LineStart     int               `json:"line_start,omitempty"`
}

// NewEvaluator returns an isolated plan owner with no process-global state.
func NewEvaluator() *Evaluator {
	return &Evaluator{plans: make(map[string]runtimePlanCacheEntry)}
}

func (e *Evaluator) loadFreshRuntimePlan(root string) (*runtimePlan, error) {
	if e == nil {
		e = NewEvaluator()
	}
	plan, err := e.loadRuntimePlan(root)
	if err != nil {
		return nil, lockfileRefreshRequired(err)
	}
	return plan, nil
}

func (e *Evaluator) loadRuntimePlan(root string) (*runtimePlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.plans == nil {
		e.plans = make(map[string]runtimePlanCacheEntry)
	}

	data, err := readLockfileBytes(root)
	if err != nil {
		delete(e.plans, root)
		return nil, err
	}
	lockHash := sha256.Sum256(data)
	bundle, err := ingest.LoadPolicySources(root)
	if err != nil {
		delete(e.plans, root)
		return nil, err
	}
	currentDigest, err := compiler.ComputeSourceDigest(bundle)
	if err != nil {
		delete(e.plans, root)
		return nil, &rerrors.LockfileError{Message: "compute current source digest", Cause: err}
	}
	if cached, ok := e.plans[root]; ok && cached.lockHash == lockHash {
		if cached.plan.sourceCount != len(bundle.Sources) || cached.plan.sourceDigest != currentDigest {
			delete(e.plans, root)
			return nil, &rerrors.LockfileError{Message: "compiled lockfile source_digest does not match the current policy sources"}
		}
		return cached.plan, nil
	}

	lock, err := decodeLockfile(data)
	if err != nil {
		delete(e.plans, root)
		return nil, err
	}
	lock.byteHash = lockHash
	if err := validateLockfileFreshnessBundle(lock.payload, lock.migrated, bundle, currentDigest); err != nil {
		delete(e.plans, root)
		return nil, err
	}
	plan, err := compileRuntimePlan(lock.payload)
	if err != nil {
		delete(e.plans, root)
		return nil, err
	}
	e.plans[root] = runtimePlanCacheEntry{lockHash: lockHash, plan: plan}
	return plan, nil
}

func compileRuntimePlan(payload map[string]interface{}) (*runtimePlan, error) {
	envelope, err := decodeRuntimeEnvelope(payload)
	if err != nil {
		return nil, err
	}
	rules, err := decodeRuntimeRulesJSON(envelope.Rules)
	if err != nil {
		return nil, err
	}
	if envelope.RuleCount < 0 || len(rules) != envelope.RuleCount {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile rule_count does not match the typed runtime plan"}
	}
	if envelope.SourceCount < 0 || len(envelope.Sources) != envelope.SourceCount {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile source_count does not match the typed runtime plan"}
	}
	customRuntimeDigests := map[string]string{}
	for _, summary := range envelope.CustomRuntimes {
		if err := customruntime.ValidateSummary(summary); err != nil {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile custom runtime is invalid: " + err.Error()}
		}
		if _, duplicate := customRuntimeDigests[summary.Runtime]; duplicate {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile contains duplicate custom runtime " + summary.Runtime}
		}
		customRuntimeDigests[summary.Runtime] = summary.ManifestDigest
	}
	customSourceNames := map[string]struct{}{}
	for _, source := range envelope.Sources {
		if source.Kind != policy.SourceCustomRuntime {
			continue
		}
		const prefix = ".reconc/runtimes/"
		if !strings.HasPrefix(source.Path, prefix) || !strings.HasSuffix(source.Path, ".json") {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile custom runtime source path is invalid"}
		}
		name := strings.TrimSuffix(strings.TrimPrefix(source.Path, prefix), ".json")
		if strings.Contains(name, "/") {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile custom runtime source path is invalid"}
		}
		customSourceNames["custom:"+name] = struct{}{}
	}
	if len(customSourceNames) != len(customRuntimeDigests) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile custom runtime summaries do not match manifest sources"}
	}
	for runtimeName := range customSourceNames {
		if _, ok := customRuntimeDigests[runtimeName]; !ok {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile custom runtime summaries do not match manifest sources"}
		}
	}
	mcp := envelope.MCP
	if mcp != nil {
		if err := mcp.Validate(); err != nil {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile MCP contract is invalid: " + err.Error()}
		}
		mcp.Tools = policy.SortedMCPTools(mcp.Tools)
	}
	plan := &runtimePlan{
		defaultMode:          envelope.DefaultMode,
		rules:                rules,
		ruleByID:             make(map[string]int, len(rules)),
		rulesByKind:          make(map[policy.Kind][]int, len(policy.AllKinds())),
		preCommandRules:      make([]int, 0),
		sourceDigest:         envelope.SourceDigest,
		sourceCount:          envelope.SourceCount,
		mcp:                  mcp,
		customRuntimeDigests: customRuntimeDigests,
	}
	for index := range plan.rules {
		rule := &plan.rules[index]
		if err := validateRuntimeRule(rule); err != nil {
			return nil, &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile rule[%d] is invalid: %s", index, err)}
		}
		if _, duplicate := plan.ruleByID[rule.ID]; duplicate {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile contains duplicate rule id: " + rule.ID}
		}
		plan.ruleByID[rule.ID] = index
		plan.rulesByKind[rule.Kind] = append(plan.rulesByKind[rule.Kind], index)
		if runtimeRuleContainsForbidCommand(rule) {
			plan.preCommandRules = append(plan.preCommandRules, index)
		}
	}
	return plan, nil
}

func decodeRuntimeRules(raw interface{}) ([]policy.Rule, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "encode compiled rules for typed runtime plan", Cause: err}
	}
	return decodeRuntimeRulesJSON(data)
}

func decodeRuntimeRulesJSON(data []byte) ([]policy.Rule, error) {
	var rules []policy.Rule
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile rules are invalid", Cause: err}
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile rules contain trailing JSON"}
	}
	if rules == nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile rules must contain a list"}
	}
	if err := validateRuntimeRuleFieldPresence(data, rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func decodeRuntimeEnvelope(payload map[string]interface{}) (*runtimeEnvelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "encode compiled lockfile for typed runtime plan", Cause: err}
	}
	var envelope runtimeEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile typed envelope is invalid", Cause: err}
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile typed envelope contains trailing JSON"}
	}
	if envelope.CompilerVersion == "" || !envelope.DefaultMode.Valid() || envelope.Rules == nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile typed envelope is incomplete"}
	}
	precedence := policy.SourcePrecedence()
	if len(envelope.SourcePrecedence) != len(precedence) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile source_precedence is invalid"}
	}
	for index := range precedence {
		if envelope.SourcePrecedence[index] != precedence[index] {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile source_precedence is invalid"}
		}
	}
	return &envelope, nil
}

func validateRuntimeRuleFieldPresence(data []byte, rules []policy.Rule) error {
	var rawRules []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawRules); err != nil || len(rawRules) != len(rules) {
		return &rerrors.LockfileError{Message: "compiled lockfile rule field layout is invalid", Cause: err}
	}
	for index, rawRule := range rawRules {
		allowed := runtimeFieldSet(
			"id", "kind", "mode", "message", "paths", "before_paths", "when_paths",
			"commands", "claims", "command_match", "required_files", "evidence", "checks",
			"script", "args", "timeout_sec", "kill_timeout_sec", "cache_inputs", "assurance",
			"source_path", "source_block_id",
			"deprecated", "deprecated_reason", "deprecated_since", "deprecated_replaced_by",
			"scope_paths", "scope_id",
		)
		if err := rejectRuntimeFields(rawRule, allowed, fmt.Sprintf("rules[%d]", index)); err != nil {
			return err
		}
		if rawChecks, present := rawRule["checks"]; present {
			if err := validateRuntimeCheckFieldPresence(rawChecks, rules[index].Checks, index); err != nil {
				return err
			}
		}
		if rawAssurance, present := rawRule["assurance"]; present {
			if err := validateRuntimeAssuranceFieldPresence(rawAssurance, rules[index].Assurance, index); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRuntimeCheckFieldPresence(data json.RawMessage, checks []policy.Check, ruleIndex int) error {
	var rawChecks []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawChecks); err != nil || len(rawChecks) != len(checks) {
		return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile rules[%d].checks field layout is invalid", ruleIndex), Cause: err}
	}
	for index, rawCheck := range rawChecks {
		allowed := runtimeFieldSet("kind", "optional")
		switch checks[index].Kind {
		case policy.KindRequireFreshFile:
			runtimeAddFields(allowed, "path", "max_age_hours")
		case policy.KindRequireEvidence:
			runtimeAddFields(allowed, "file", "must_exist", "must_contain", "must_not_contain", "max_line_count")
		case policy.KindRequireClaim:
			runtimeAddFields(allowed, "claims")
		case policy.KindRequireCommand, policy.KindRequireCommandSuccess, policy.KindForbidCommand:
			runtimeAddFields(allowed, "commands", "command_match")
		case policy.KindDenyWrite:
			runtimeAddFields(allowed, "paths")
		case policy.KindRequireScript:
			runtimeAddFields(allowed, "script", "args", "timeout_sec", "cache_inputs")
		}
		if err := rejectRuntimeFields(rawCheck, allowed, fmt.Sprintf("rules[%d].checks[%d]", ruleIndex, index)); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeAssuranceFieldPresence(data json.RawMessage, gates []policy.AssuranceGate, ruleIndex int) error {
	var rawGates []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawGates); err != nil || len(rawGates) != len(gates) {
		return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile rules[%d].assurance field layout is invalid", ruleIndex), Cause: err}
	}
	for index, rawGate := range rawGates {
		allowed := runtimeFieldSet("id", "type", "applicable_if")
		switch gates[index].Type {
		case policy.AssuranceRepositoryLayout:
			runtimeAddFields(allowed, "allowed_root_entries", "required_root_entries", "forbidden_root_entries", "reserved_dirs", "allow_hidden_entries")
		case policy.AssuranceGeneratedReference, policy.AssuranceLiveVerification:
			runtimeAddFields(allowed, "commands", "command_policy")
		case policy.AssuranceLanguageBoundary:
			runtimeAddFields(allowed, "scan_paths", "exclude_paths", "exemptions", "allowed_extensions")
		case policy.AssuranceDependencyPins:
			runtimeAddFields(allowed, "manifest_paths", "dependency_sections", "allowed_version_prefixes")
		case policy.AssurancePackageScripts:
			runtimeAddFields(allowed, "manifest_paths", "manifest_markers", "exclude_paths", "package_manager", "commands")
		case policy.AssuranceNetworkBoundary, policy.AssuranceProcessBoundary:
			runtimeAddFields(allowed, "scan_paths", "exclude_paths", "exemptions", "site_patterns", "guard_markers", "marker_window_lines")
		case policy.AssuranceSubstantiveProof:
			runtimeAddFields(allowed, "proof_file", "min_samples", "max_age_hours")
		case policy.AssuranceGoConcurrency, policy.AssuranceGoFormat, policy.AssuranceSourceHygiene:
			runtimeAddFields(allowed, "scan_paths", "exclude_paths", "exemptions")
		}
		if err := rejectRuntimeFields(rawGate, allowed, fmt.Sprintf("rules[%d].assurance[%d]", ruleIndex, index)); err != nil {
			return err
		}
	}
	return nil
}

func rejectRuntimeFields(fields map[string]json.RawMessage, allowed map[string]struct{}, context string) error {
	unknown := make([]string, 0)
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return &rerrors.LockfileError{Message: fmt.Sprintf("compiled lockfile %s contains field(s) invalid for its kind: %s", context, strings.Join(unknown, ", "))}
}

func runtimeFieldSet(fields ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(fields))
	runtimeAddFields(out, fields...)
	return out
}

func runtimeAddFields(set map[string]struct{}, fields ...string) {
	for _, field := range fields {
		set[field] = struct{}{}
	}
}

func validateRuntimeRule(rule *policy.Rule) error {
	if rule.ID == "" || strings.TrimSpace(rule.ID) != rule.ID {
		return fmt.Errorf("id must be a non-empty string without surrounding whitespace")
	}
	if !rule.Kind.Valid() {
		return fmt.Errorf("unsupported kind %q", rule.Kind)
	}
	if rule.Mode != "" && !rule.Mode.Valid() {
		return fmt.Errorf("unsupported mode %q", rule.Mode)
	}
	if strings.TrimSpace(rule.Message) == "" {
		return fmt.Errorf("message must be non-empty")
	}
	if !rule.CommandMatch.Valid() {
		return fmt.Errorf("unsupported command_match %q", rule.CommandMatch)
	}
	if rule.CommandMatch != "" && rule.Kind != policy.KindRequireCommand && rule.Kind != policy.KindRequireCommandSuccess && rule.Kind != policy.KindForbidCommand {
		return fmt.Errorf("command_match is invalid for kind %s", rule.Kind)
	}
	stringLists := []struct {
		name   string
		values []string
	}{
		{"paths", rule.Paths},
		{"before_paths", rule.BeforePaths},
		{"when_paths", rule.WhenPaths},
		{"commands", rule.Commands},
		{"claims", rule.Claims},
		{"args", rule.Args},
		{"scope_paths", rule.ScopePaths},
	}
	for _, field := range stringLists {
		if err := validateRuntimeStrings(field.name, field.values); err != nil {
			return err
		}
	}
	if rule.ScopeID != "" && len(rule.ScopePaths) == 0 {
		return fmt.Errorf("scope_id requires non-empty scope_paths")
	}
	for _, pattern := range append(append(append(append([]string{}, rule.Paths...), rule.BeforePaths...), rule.WhenPaths...), rule.ScopePaths...) {
		if _, err := MatchPath(pattern, "runtime-plan-validation"); err != nil {
			return fmt.Errorf("invalid path pattern %q: %w", pattern, err)
		}
	}
	if err := validateRuntimeRuleShape(rule); err != nil {
		return err
	}
	for index, required := range rule.RequiredFiles {
		if !runtimePlanRepoRelativePath(required.Path) {
			return fmt.Errorf("required_files[%d].path must be a safe repo-relative path", index)
		}
		if required.MaxAgeHours < 0 {
			return fmt.Errorf("required_files[%d].max_age_hours must be non-negative", index)
		}
	}
	for index, evidence := range rule.Evidence {
		if err := validateRuntimeEvidence(evidence.File, evidence.MustExist, evidence.MustContain, evidence.MustNotContain, evidence.MaxLineCount); err != nil {
			return fmt.Errorf("evidence[%d]: %w", index, err)
		}
	}
	for index := range rule.Checks {
		if err := validateRuntimeCheck(&rule.Checks[index]); err != nil {
			return fmt.Errorf("checks[%d]: %w", index, err)
		}
	}
	seenGates := make(map[string]struct{}, len(rule.Assurance))
	for index := range rule.Assurance {
		gate := &rule.Assurance[index]
		if gate.ID == "" || strings.TrimSpace(gate.ID) != gate.ID || !gate.Type.Valid() {
			return fmt.Errorf("assurance[%d] has invalid id or type", index)
		}
		if _, duplicate := seenGates[gate.ID]; duplicate {
			return fmt.Errorf("assurance[%d] duplicates gate id %q", index, gate.ID)
		}
		seenGates[gate.ID] = struct{}{}
		if err := parser.ValidateCompiledAssuranceGate(*gate); err != nil {
			return fmt.Errorf("assurance[%d]: %w", index, err)
		}
	}
	return nil
}

func validateRuntimeRuleShape(rule *policy.Rule) error {
	require := func(field string, values []string) error {
		if len(values) == 0 {
			return fmt.Errorf("kind %s requires non-empty %s", rule.Kind, field)
		}
		return nil
	}
	if rule.Kind != policy.KindRequireAssurance && len(rule.Assurance) > 0 {
		return fmt.Errorf("assurance is only valid for kind require_assurance")
	}
	switch rule.Kind {
	case policy.KindDenyWrite:
		return require("paths", rule.Paths)
	case policy.KindRequireRead:
		if err := require("paths", rule.Paths); err != nil {
			return err
		}
		return require("before_paths", rule.BeforePaths)
	case policy.KindRequireCommand, policy.KindRequireCommandSuccess:
		if err := require("when_paths", rule.WhenPaths); err != nil {
			return err
		}
		return require("commands", rule.Commands)
	case policy.KindForbidCommand:
		return require("commands", rule.Commands)
	case policy.KindCoupleChange:
		if err := require("paths", rule.Paths); err != nil {
			return err
		}
		return require("when_paths", rule.WhenPaths)
	case policy.KindRequireClaim:
		if err := require("when_paths", rule.WhenPaths); err != nil {
			return err
		}
		return require("claims", rule.Claims)
	case policy.KindRequireFreshFile:
		if len(rule.RequiredFiles) == 0 {
			return fmt.Errorf("kind require_fresh_file requires required_files")
		}
		return require("when_paths", rule.WhenPaths)
	case policy.KindRequireEvidence:
		if len(rule.Evidence) == 0 {
			return fmt.Errorf("kind require_evidence requires evidence")
		}
		return require("when_paths", rule.WhenPaths)
	case policy.KindRequireScript:
		if !runtimePlanRepoRelativePath(rule.Script) {
			return fmt.Errorf("script must be a safe repo-relative path")
		}
		if rule.TimeoutSec < 0 || rule.KillTimeoutSec < 0 {
			return fmt.Errorf("script timeouts must be non-negative")
		}
		if err := validateRuntimePlanCacheInputs(rule.CacheInputs); err != nil {
			return err
		}
		return require("when_paths", rule.WhenPaths)
	case policy.KindAllOf, policy.KindAnyOf, policy.KindNot:
		if len(rule.Checks) == 0 {
			return fmt.Errorf("kind %s requires checks", rule.Kind)
		}
		if rule.Kind == policy.KindNot && len(rule.Checks) != 1 {
			return fmt.Errorf("kind not requires exactly one check")
		}
		return require("when_paths", rule.WhenPaths)
	case policy.KindRequireAssurance:
		if len(rule.Assurance) == 0 {
			return fmt.Errorf("kind require_assurance requires assurance")
		}
		if len(rule.Paths) > 0 || len(rule.BeforePaths) > 0 || len(rule.Commands) > 0 || len(rule.Claims) > 0 || len(rule.RequiredFiles) > 0 || len(rule.Evidence) > 0 || rule.Script != "" || len(rule.Args) > 0 || rule.TimeoutSec != 0 || rule.KillTimeoutSec != 0 || len(rule.CacheInputs) > 0 || len(rule.Checks) > 0 {
			return fmt.Errorf("kind require_assurance contains fields that are invalid for this kind")
		}
		return require("when_paths", rule.WhenPaths)
	}
	return nil
}

func validateRuntimeCheck(check *policy.Check) error {
	if !check.Kind.Valid() || check.Kind.IsComposite() || check.Kind == policy.KindRequireAssurance || check.Kind == policy.KindCoupleChange || check.Kind == policy.KindRequireRead {
		return fmt.Errorf("unsupported composite check kind %q", check.Kind)
	}
	if !check.CommandMatch.Valid() {
		return fmt.Errorf("unsupported command_match %q", check.CommandMatch)
	}
	if check.CommandMatch != "" && check.Kind != policy.KindRequireCommand && check.Kind != policy.KindRequireCommandSuccess && check.Kind != policy.KindForbidCommand {
		return fmt.Errorf("command_match is invalid for kind %s", check.Kind)
	}
	stringLists := []struct {
		name   string
		values []string
	}{
		{"paths", check.Paths},
		{"before_paths", check.BeforePaths},
		{"when_paths", check.WhenPaths},
		{"commands", check.Commands},
		{"claims", check.Claims},
		{"args", check.Args},
	}
	for _, field := range stringLists {
		if err := validateRuntimeStrings(field.name, field.values); err != nil {
			return err
		}
	}
	for _, pattern := range append(append(append([]string{}, check.Paths...), check.BeforePaths...), check.WhenPaths...) {
		if _, err := MatchPath(pattern, "runtime-plan-validation"); err != nil {
			return fmt.Errorf("invalid path pattern %q: %w", pattern, err)
		}
	}
	switch check.Kind {
	case policy.KindRequireFreshFile:
		if !runtimePlanRepoRelativePath(check.Path) {
			return fmt.Errorf("path must be a safe repo-relative path")
		}
		if check.MaxAgeHours < 0 {
			return fmt.Errorf("max_age_hours must be non-negative")
		}
	case policy.KindRequireEvidence:
		return validateRuntimeEvidence(check.File, check.MustExist, check.MustContain, check.MustNotContain, check.MaxLineCount)
	case policy.KindRequireClaim:
		if len(check.Claims) == 0 {
			return fmt.Errorf("require_claim requires claims")
		}
	case policy.KindRequireCommand, policy.KindRequireCommandSuccess, policy.KindForbidCommand:
		if len(check.Commands) == 0 {
			return fmt.Errorf("command check requires commands")
		}
	case policy.KindDenyWrite:
		if len(check.Paths) == 0 {
			return fmt.Errorf("deny_write requires paths")
		}
	case policy.KindRequireScript:
		if !runtimePlanRepoRelativePath(check.Script) {
			return fmt.Errorf("script must be a safe repo-relative path")
		}
		if check.TimeoutSec < 0 {
			return fmt.Errorf("timeout_sec must be non-negative")
		}
		if err := validateRuntimePlanCacheInputs(check.CacheInputs); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeEvidence(file string, mustExist bool, mustContain []string, mustNotContain string, maxLineCount int) error {
	if !runtimePlanRepoRelativePath(file) {
		return fmt.Errorf("file must be a safe repo-relative path")
	}
	if err := validateRuntimeStrings("must_contain", mustContain); err != nil {
		return err
	}
	if mustNotContain != "" && strings.TrimSpace(mustNotContain) == "" {
		return fmt.Errorf("must_not_contain must be non-empty when present")
	}
	if maxLineCount < 0 {
		return fmt.Errorf("max_line_count must be non-negative")
	}
	if !mustExist && len(mustContain) == 0 && mustNotContain == "" && maxLineCount == 0 {
		return fmt.Errorf("must specify at least one evidence assertion")
	}
	return nil
}

func validateRuntimeStrings(field string, values []string) error {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s[%d] must be non-empty", field, index)
		}
	}
	return nil
}

// validateRuntimePlanCacheInputs re-checks the declared script inputs against
// the shape Stop report reuse can bind, so a hand-edited lockfile cannot smuggle
// a glob or an escaping path past the compiler.
func validateRuntimePlanCacheInputs(inputs []string) error {
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if !runtimePlanRepoRelativePath(input) || strings.ContainsAny(input, "*?[]{}") {
			return fmt.Errorf("cache_inputs must name literal repo-relative files")
		}
		if _, duplicate := seen[input]; duplicate {
			return fmt.Errorf("cache_inputs lists %s more than once", input)
		}
		seen[input] = struct{}{}
	}
	return nil
}

func runtimePlanRepoRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || pathidentity.Rooted(value) || strings.Contains(value, ":") {
		return false
	}
	return !pathidentity.EscapesLexically(value)
}

func runtimeRuleContainsForbidCommand(rule *policy.Rule) bool {
	if rule.Kind == policy.KindForbidCommand {
		return true
	}
	if !rule.Kind.IsComposite() {
		return false
	}
	for _, check := range rule.Checks {
		if check.Kind == policy.KindForbidCommand {
			return true
		}
	}
	return false
}
