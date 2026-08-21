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

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/customruntime"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

// Evaluator owns immutable compiled plans. A short-lived evaluator preserves
// the public one-shot behavior; the hook worker keeps one evaluator for its
// lifetime so unchanged repositories skip lock JSON decoding and plan build.
type Evaluator struct {
	mu        sync.Mutex
	plans     map[string]runtimePlanCacheEntry
	useSerial uint64
}

const maxRuntimePlanCacheEntries = 32

type runtimePlanCacheEntry struct {
	lockHash       [sha256.Size]byte
	freshness      [sha256.Size]byte
	freshnessValid bool
	lastUsed       uint64
	plan           *runtimePlan
}

type runtimePlan struct {
	defaultMode          policy.Mode
	rules                []policy.Rule
	ruleByID             map[string]int
	rulesByKind          map[policy.Kind][]int
	preCommandRules      []int
	sourceDigest         string
	lockDigest           string
	sourceCount          int
	actions              *action.CompiledPlan
	customRuntimeDigests map[string]string
	sources              []runtimeSource
	sourceFreshness      sourceFreshnessRecipe
	pathMatchers         *runtimePathMatchers
	templateMatchers     *runtimeTemplateMatchers
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
	Actions          json.RawMessage         `json:"actions"`
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
	if cached, ok := e.plans[root]; ok {
		if cached.lockHash == lockHash && cached.freshnessValid {
			freshness, freshnessErr := observeRuntimeSourceFreshness(root, cached.plan)
			if freshnessErr == nil && freshness == cached.freshness {
				e.useSerial++
				cached.lastUsed = e.useSerial
				e.plans[root] = cached
				return cached.plan, nil
			}
		}
		delete(e.plans, root)
	}
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
	plan, err := compileRuntimePlanWithParts(lock.payload, lock.rulesJSON, lock.actionsJSON, lock.actions)
	if err != nil {
		delete(e.plans, root)
		return nil, err
	}
	plan.sourceFreshness, err = newSourceFreshnessRecipe(root, bundle.PolicyIncludePatterns())
	if err != nil {
		delete(e.plans, root)
		return nil, err
	}
	freshness, freshnessErr := observeRuntimeSourceFreshnessFromBundle(root, plan, bundle)
	e.useSerial++
	e.evictRuntimePlanCache(root)
	e.plans[root] = runtimePlanCacheEntry{
		lockHash: lockHash, freshness: freshness, freshnessValid: freshnessErr == nil,
		lastUsed: e.useSerial, plan: plan,
	}
	return plan, nil
}

func (e *Evaluator) evictRuntimePlanCache(incomingRoot string) {
	if len(e.plans) < maxRuntimePlanCacheEntries {
		return
	}
	var oldestRoot string
	var oldest uint64
	for root, entry := range e.plans {
		if root == incomingRoot {
			continue
		}
		if oldestRoot == "" || entry.lastUsed < oldest {
			oldestRoot, oldest = root, entry.lastUsed
		}
	}
	if oldestRoot != "" {
		delete(e.plans, oldestRoot)
	}
}

func compileRuntimePlan(payload map[string]interface{}) (*runtimePlan, error) {
	return compileRuntimePlanWithParts(payload, nil, nil, nil)
}

func compileRuntimePlanWithParts(payload map[string]interface{}, rulesJSON, actionsJSON []byte, compiledActions *action.CompiledPlan) (*runtimePlan, error) {
	envelope, err := decodeRuntimeEnvelopeWithParts(payload, rulesJSON, actionsJSON)
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
	actions := compiledActions
	if actions == nil {
		actions, err = decodeActionPlanJSON(envelope.Actions)
		if err != nil {
			return nil, err
		}
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
	plan := &runtimePlan{
		defaultMode:          envelope.DefaultMode,
		rules:                rules,
		ruleByID:             make(map[string]int, len(rules)),
		rulesByKind:          make(map[policy.Kind][]int, len(policy.AllKinds())),
		preCommandRules:      make([]int, 0),
		sourceDigest:         envelope.SourceDigest,
		lockDigest:           envelope.LockDigest,
		sourceCount:          envelope.SourceCount,
		actions:              actions,
		customRuntimeDigests: customRuntimeDigests,
		sources:              append([]runtimeSource(nil), envelope.Sources...),
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
	pathMatchers, err := compileRuntimePathMatchers(plan.rules)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile path matcher preparation failed", Cause: err}
	}
	plan.pathMatchers = pathMatchers
	templateMatchers, err := compileRuntimeTemplateMatchers(plan.rules)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile template matcher preparation failed", Cause: err}
	}
	plan.templateMatchers = templateMatchers
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

func decodeActionPlanJSON(data []byte) (*action.CompiledPlan, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain an actions object"}
	}
	var plan action.Plan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile action plan is invalid", Cause: err}
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile action plan contains trailing JSON"}
	}
	compiled, err := action.CompilePlan(plan)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile action plan is invalid: " + err.Error()}
	}
	return compiled, nil
}

func decodeRuntimeEnvelopeWithParts(payload map[string]interface{}, rulesJSON, actionsJSON []byte) (*runtimeEnvelope, error) {
	if len(rulesJSON) == 0 {
		var err error
		rulesJSON, err = json.Marshal(payload["rules"])
		if err != nil {
			return nil, &rerrors.LockfileError{Message: "encode compiled lockfile rules for typed runtime plan", Cause: err}
		}
	}
	if len(actionsJSON) == 0 {
		var err error
		actionsJSON, err = json.Marshal(payload["actions"])
		if err != nil {
			return nil, &rerrors.LockfileError{Message: "encode compiled lockfile action plan for typed runtime plan", Cause: err}
		}
	}
	// Keep the large rules/actions arrays as already encoded raw messages. The
	// typed envelope conversion still validates every scalar and source field,
	// but does not walk and re-encode those nested plans a second time.
	envelopePayload := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		switch key {
		case "rules", "actions":
			continue
		default:
			envelopePayload[key] = value
		}
	}
	envelopePayload["rules"] = json.RawMessage(rulesJSON)
	envelopePayload["actions"] = json.RawMessage(actionsJSON)
	data, err := json.Marshal(envelopePayload)
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
	if envelope.CompilerVersion == "" || !envelope.DefaultMode.Valid() || envelope.Rules == nil || envelope.Actions == nil {
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
			"source_path", "source_block_id",
			"scope_paths", "scope_id",
		)
		for _, field := range parser.RuleKindFields(rules[index].Kind) {
			// template is an authoring-only field and is consumed before a
			// rule reaches the compiled lockfile.
			if field != "template" {
				runtimeAddFields(allowed, field)
			}
		}
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
	for _, field := range runtimeRuleFieldValues(rule) {
		if !field.present {
			continue
		}
		if field.name != "scope_paths" && field.name != "scope_id" && !parser.RuleKindFieldAllowed(rule.Kind, field.name) {
			return fmt.Errorf("field %q is invalid for kind %s", field.name, rule.Kind)
		}
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
		if _, err := templates.Variables(pattern); err != nil {
			return fmt.Errorf("invalid template syntax in path pattern %q: %w", pattern, err)
		}
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

func runtimeRuleFieldValues(rule *policy.Rule) []struct {
	name    string
	present bool
} {
	return []struct {
		name    string
		present bool
	}{
		{"paths", len(rule.Paths) > 0},
		{"before_paths", len(rule.BeforePaths) > 0},
		{"when_paths", len(rule.WhenPaths) > 0},
		{"commands", len(rule.Commands) > 0},
		{"claims", len(rule.Claims) > 0},
		{"command_match", rule.CommandMatch != ""},
		{"required_files", len(rule.RequiredFiles) > 0},
		{"evidence", len(rule.Evidence) > 0},
		{"checks", len(rule.Checks) > 0},
		{"script", rule.Script != ""},
		{"args", len(rule.Args) > 0},
		{"timeout_sec", rule.TimeoutSec != 0},
		{"kill_timeout_sec", rule.KillTimeoutSec != 0},
		{"cache_inputs", len(rule.CacheInputs) > 0},
		{"assurance", len(rule.Assurance) > 0},
		{"scope_paths", len(rule.ScopePaths) > 0},
		{"scope_id", rule.ScopeID != ""},
	}
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
		{"commands", check.Commands},
		{"claims", check.Claims},
		{"args", check.Args},
	}
	for _, field := range stringLists {
		if err := validateRuntimeStrings(field.name, field.values); err != nil {
			return err
		}
	}
	for _, pattern := range check.Paths {
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
