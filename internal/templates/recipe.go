package templates

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"regexp"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

// RecipeMetadata documents the evidence contract of a built-in or user
// template. Contract selects invocation and evidence validation on the
// expanded require_script rule; the other fields describe its use.
type RecipeMetadata struct {
	Contract           policy.RecipeContract `json:"contract,omitempty"`
	InputPaths         []string              `json:"input_paths"`
	CWD                string                `json:"cwd"`
	CommandIdentity    string                `json:"command_identity"`
	EvidenceIdentity   string                `json:"evidence_identity"`
	Applicability      string                `json:"applicability"`
	Limitations        []string              `json:"limitations"`
	Remediation        string                `json:"remediation"`
	RequiredRuleFields []string              `json:"required_rule_fields"`
	Examples           []RecipeExample       `json:"examples"`
}

// RecipeExample is an executable, project-owned command example shown with a
// recipe. Expect is the Reconc disposition the command is intended to produce.
type RecipeExample struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	CWD     string `json:"cwd"`
	Expect  string `json:"expect"`
}

// recipeFromBody decodes the optional template-only `recipe` mapping. Keeping
// this metadata outside policy.Rule lets templates describe a contract without
// inventing a second runtime rule kind or leaking unknown fields into parsing.
func recipeFromBody(body map[string]interface{}, contextPath string) (*RecipeMetadata, error) {
	raw, ok := body["recipe"]
	if !ok || raw == nil {
		return nil, nil
	}
	mapping, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("template %s recipe must be a mapping", contextPath)
	}
	allowed := map[string]bool{
		"contract":    true,
		"input_paths": true, "cwd": true, "command_identity": true,
		"evidence_identity": true, "applicability": true, "limitations": true,
		"remediation": true, "required_rule_fields": true, "examples": true,
	}
	for key := range mapping {
		if !allowed[key] {
			return nil, fmt.Errorf("template %s recipe field %q is not supported", contextPath, key)
		}
	}
	metadata := &RecipeMetadata{}
	var err error
	if rawContract, ok := mapping["contract"]; ok && rawContract != nil {
		value, ok := rawContract.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("template %s recipe field %q must be a non-empty string", contextPath, "contract")
		}
		metadata.Contract = policy.RecipeContract(strings.TrimSpace(value))
		if !metadata.Contract.Valid() || metadata.Contract == "" {
			return nil, fmt.Errorf("template %s recipe field %q is unsupported: %q", contextPath, "contract", value)
		}
	}
	if metadata.InputPaths, err = recipeStrings(mapping, "input_paths", contextPath, true); err != nil {
		return nil, err
	}
	metadata.CWD, err = recipeString(mapping, "cwd", contextPath, true)
	if err != nil {
		return nil, err
	}
	if metadata.CWD == "" {
		metadata.CWD = "."
	}
	if recipePathEscapes(metadata.CWD) {
		return nil, fmt.Errorf("template %s recipe cwd must stay repository-relative", contextPath)
	}
	if metadata.CommandIdentity, err = recipeString(mapping, "command_identity", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.EvidenceIdentity, err = recipeString(mapping, "evidence_identity", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.Applicability, err = recipeString(mapping, "applicability", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.Limitations, err = recipeStrings(mapping, "limitations", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.Remediation, err = recipeString(mapping, "remediation", contextPath, true); err != nil {
		return nil, err
	}
	if metadata.RequiredRuleFields, err = recipeStrings(mapping, "required_rule_fields", contextPath, true); err != nil {
		return nil, err
	}
	for index, field := range metadata.RequiredRuleFields {
		if field != "script" && field != "when_paths" && field != "args" && field != "cache_inputs" {
			return nil, fmt.Errorf("template %s recipe required_rule_fields[%d] names unsupported field %q", contextPath, index, field)
		}
	}
	metadata.Examples, err = recipeExamples(mapping, contextPath)
	if err != nil {
		return nil, err
	}
	return metadata, nil
}

// ValidateRequiredRuleFields enforces the rule-field contract declared by a
// recipe after template defaults and user overrides have been merged. The
// ordinary parser still owns the detailed type and value validation; this
// boundary guarantees that a recipe cannot silently become an unconfigured
// generic require_script rule.
func ValidateRequiredRuleFields(tmpl *Template, merged map[string]interface{}, contextPath string) error {
	if tmpl == nil || tmpl.Recipe == nil {
		return nil
	}
	for _, field := range tmpl.Recipe.RequiredRuleFields {
		value, ok := merged[field]
		if !ok || value == nil {
			return fmt.Errorf("template %s requires field '%s'", contextPath, field)
		}
		switch field {
		case "script":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("template %s requires field '%s' to be a non-empty string", contextPath, field)
			}
		case "when_paths", "args", "cache_inputs":
			if !nonEmptyStringList(value) {
				return fmt.Errorf("template %s requires field '%s' to be a non-empty list of strings", contextPath, field)
			}
		}
	}
	if tmpl.Recipe.Contract != "" {
		kind, ok := merged["kind"].(string)
		if !ok || strings.TrimSpace(kind) != "require_script" {
			return fmt.Errorf("template %s recipe contract requires kind 'require_script'", contextPath)
		}
		args, ok := merged["args"].([]interface{})
		if !ok {
			if values, typed := merged["args"].([]string); typed {
				args = make([]interface{}, len(values))
				for index := range values {
					args[index] = values[index]
				}
			}
		}
		values := make([]string, len(args))
		for index, value := range args {
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("template %s recipe field 'args' must contain strings", contextPath)
			}
			values[index] = text
		}
		if err := ValidateRecipeInvocation(tmpl.Recipe.Contract, values, contextPath); err != nil {
			return err
		}
	}
	return nil
}

var immutableRecipeIdentity = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

// ValidateRecipeInvocation enforces the identity-bearing argument contract of
// each built-in evidence recipe. The project-owned script still performs the
// domain check; this boundary prevents a recipe from silently degrading to an
// arbitrary exit-status gate or mutable default reference.
func ValidateRecipeInvocation(contract policy.RecipeContract, args []string, contextPath string) error {
	if !contract.Valid() || contract == "" {
		return fmt.Errorf("template %s recipe contract is unsupported: %q", contextPath, contract)
	}
	if len(args) == 0 {
		return fmt.Errorf("template %s recipe contract requires invocation arguments", contextPath)
	}
	values, err := recipeInvocationValues(args, contextPath)
	if err != nil {
		return err
	}
	requireIdentity := func(flag string) error {
		value, ok := values[flag]
		if !ok || !immutableRecipeIdentity.MatchString(value) {
			return fmt.Errorf("template %s recipe contract requires %s to be a full immutable Git identity", contextPath, flag)
		}
		return nil
	}
	requirePath := func(flag string) error {
		value, ok := values[flag]
		if !ok || strings.TrimSpace(value) == "" || value == "-" || recipePathEscapes(value) {
			return fmt.Errorf("template %s recipe contract requires %s to name a repository-local evidence file", contextPath, flag)
		}
		return nil
	}
	requireFlag := func(flag string) error {
		if value, present := values[flag]; present && value == "" {
			return nil
		}
		return fmt.Errorf("template %s recipe contract requires %s as an enabled flag", contextPath, flag)
	}
	switch contract {
	case policy.RecipeContractPublicAPI:
		if err := requireIdentity("--base"); err != nil {
			return err
		}
		return requireIdentity("--current")
	case policy.RecipeContractSchemaMigration:
		if _, ok := values["--engine"]; !ok {
			return fmt.Errorf("template %s recipe contract requires --engine", contextPath)
		}
		if strings.TrimSpace(values["--engine"]) == "" {
			return fmt.Errorf("template %s recipe contract requires --engine to name an engine", contextPath)
		}
		if err := requirePath("--database"); err != nil {
			return err
		}
		if err := requireFlag("--forward"); err != nil {
			return err
		}
		rollbackPolicies := 0
		for _, flag := range []string{"--rollback", "--rollback-required", "--rollback-not-supported"} {
			if _, present := values[flag]; present {
				if err := requireFlag(flag); err != nil {
					return err
				}
				rollbackPolicies++
			}
		}
		if rollbackPolicies != 1 {
			return fmt.Errorf("template %s recipe contract requires exactly one rollback policy: --rollback, --rollback-required, or --rollback-not-supported", contextPath)
		}
		if err := requireFlag("--isolated"); err != nil {
			return err
		}
	case policy.RecipeContractGenerated:
		if err := requireIdentity("--source"); err != nil {
			return err
		}
		if err := requirePath("--outputs"); err != nil {
			return err
		}
		seen := make(map[string]bool)
		for _, output := range strings.Split(values["--outputs"], ",") {
			if output == "" || output == "." || output == "-" || recipePathEscapes(output) || seen[output] {
				return fmt.Errorf("template %s recipe contract requires unique repository-local --outputs", contextPath)
			}
			seen[output] = true
		}
	case policy.RecipeContractPerformance:
		for _, flag := range []string{"--baseline", "--result", "--output"} {
			if err := requirePath(flag); err != nil {
				return err
			}
		}
		if strings.TrimSpace(values["--suite"]) == "" {
			return fmt.Errorf("template %s recipe contract requires --suite to name the workload suite", contextPath)
		}
		return requireIdentity("--current")
	}
	return nil
}

// RecipeEvidence is the strict stdout envelope emitted by a contract-aware
// recipe script. Reconc checks identity fields against the configured args;
// domain tooling remains responsible for producing the evidence itself.
type RecipeEvidence struct {
	Contract             policy.RecipeContract `json:"contract"`
	Result               string                `json:"result"`
	Base                 string                `json:"base,omitempty"`
	Current              string                `json:"current,omitempty"`
	Engine               string                `json:"engine,omitempty"`
	Database             string                `json:"database,omitempty"`
	Forward              bool                  `json:"forward,omitempty"`
	Rollback             bool                  `json:"rollback,omitempty"`
	Isolated             bool                  `json:"isolated,omitempty"`
	Source               string                `json:"source,omitempty"`
	Outputs              []string              `json:"outputs,omitempty"`
	Baseline             string                `json:"baseline,omitempty"`
	BenchmarkResult      string                `json:"benchmark_result,omitempty"`
	Comparison           string                `json:"comparison,omitempty"`
	Suite                string                `json:"suite,omitempty"`
	AbsoluteBudgetPass   bool                  `json:"absolute_budget_pass,omitempty"`
	NormalizedBudgetPass bool                  `json:"normalized_budget_pass,omitempty"`
	Evidence             string                `json:"evidence"`
}

// ValidateRecipeEvidence validates one strict recipe stdout envelope and
// binds its identity fields to the invocation arguments and exit disposition.
func ValidateRecipeEvidence(contract policy.RecipeContract, args []string, stdout, expectedResult, contextPath string) error {
	if err := ValidateRecipeInvocation(contract, args, contextPath); err != nil {
		return err
	}
	values, err := recipeInvocationValues(args, contextPath)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(stdout)))
	decoder.DisallowUnknownFields()
	var evidence RecipeEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return fmt.Errorf("template %s recipe evidence must be one JSON object: %w", contextPath, err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("template %s recipe evidence contains trailing output", contextPath)
	}
	if !jsontext.Value(stdout).IsValid() {
		return fmt.Errorf("template %s recipe evidence contains duplicate names or invalid Unicode", contextPath)
	}
	if evidence.Contract != contract {
		return fmt.Errorf("template %s recipe evidence contract %q does not match %q", contextPath, evidence.Contract, contract)
	}
	if evidence.Result != expectedResult {
		return fmt.Errorf("template %s recipe evidence result %q does not match script disposition %q", contextPath, evidence.Result, expectedResult)
	}
	if strings.TrimSpace(evidence.Evidence) == "" {
		return fmt.Errorf("template %s recipe evidence requires a non-empty evidence identity", contextPath)
	}
	compare := func(field, flag, actual string) error {
		want := values[flag]
		if actual == "" || actual != want {
			return fmt.Errorf("template %s recipe evidence %s must equal %s", contextPath, field, flag)
		}
		return nil
	}
	switch contract {
	case policy.RecipeContractPublicAPI:
		if err := compare("base", "--base", evidence.Base); err != nil {
			return err
		}
		return compare("current", "--current", evidence.Current)
	case policy.RecipeContractSchemaMigration:
		if err := compare("engine", "--engine", evidence.Engine); err != nil {
			return err
		}
		if err := compare("database", "--database", evidence.Database); err != nil {
			return err
		}
		_, rollbackUnsupported := values["--rollback-not-supported"]
		if expectedResult == "pass" && (!evidence.Forward || (!rollbackUnsupported && !evidence.Rollback) || !evidence.Isolated) {
			return fmt.Errorf("template %s recipe evidence must prove forward, declared rollback, and isolation", contextPath)
		}
	case policy.RecipeContractGenerated:
		if err := compare("source", "--source", evidence.Source); err != nil {
			return err
		}
		outputs := strings.Split(values["--outputs"], ",")
		if len(evidence.Outputs) != len(outputs) {
			return fmt.Errorf("template %s recipe evidence outputs must match --outputs in order and count", contextPath)
		}
		for index, output := range outputs {
			if evidence.Outputs[index] != output {
				return fmt.Errorf("template %s recipe evidence outputs must match --outputs in order and count", contextPath)
			}
		}
	case policy.RecipeContractPerformance:
		if err := compare("current", "--current", evidence.Current); err != nil {
			return err
		}
		for _, item := range []struct {
			field, flag, actual string
		}{
			{field: "baseline", flag: "--baseline", actual: evidence.Baseline},
			{field: "benchmark_result", flag: "--result", actual: evidence.BenchmarkResult},
			{field: "comparison", flag: "--output", actual: evidence.Comparison},
			{field: "suite", flag: "--suite", actual: evidence.Suite},
		} {
			if err := compare(item.field, item.flag, item.actual); err != nil {
				return err
			}
		}
		if expectedResult == "pass" && (!evidence.AbsoluteBudgetPass || !evidence.NormalizedBudgetPass) {
			return fmt.Errorf("template %s recipe evidence must prove absolute and normalized budget pass", contextPath)
		}
	}
	return nil
}

func recipeInvocationValues(args []string, contextPath string) (map[string]string, error) {
	values := make(map[string]string)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		flag, value, hasValue := strings.Cut(arg, "=")
		boolean := flag == "--forward" || flag == "--rollback" || flag == "--rollback-required" || flag == "--rollback-not-supported" || flag == "--isolated"
		if boolean && hasValue {
			return nil, fmt.Errorf("template %s recipe contract requires %s as an enabled flag without a value", contextPath, flag)
		}
		if !hasValue {
			if index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
				if boolean {
					return nil, fmt.Errorf("template %s recipe contract requires %s without a value", contextPath, flag)
				}
				index++
				value = args[index]
				hasValue = true
			}
		}
		if _, exists := values[flag]; exists {
			return nil, fmt.Errorf("template %s recipe contract repeats argument %q", contextPath, flag)
		}
		if hasValue {
			values[flag] = value
		} else {
			values[flag] = ""
		}
	}
	return values, nil
}

func nonEmptyStringList(value interface{}) bool {
	switch values := value.(type) {
	case []interface{}:
		if len(values) == 0 {
			return false
		}
		for _, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return false
			}
		}
		return true
	case []string:
		if len(values) == 0 {
			return false
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func recipeString(mapping map[string]interface{}, key, contextPath string, required bool) (string, error) {
	raw, ok := mapping[key]
	if !ok || raw == nil {
		if required {
			return "", fmt.Errorf("template %s recipe field %q is required", contextPath, key)
		}
		return "", nil
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("template %s recipe field %q must be a non-empty string", contextPath, key)
	}
	return strings.TrimSpace(value), nil
}

func recipeStrings(mapping map[string]interface{}, key, contextPath string, required bool) ([]string, error) {
	raw, ok := mapping[key]
	if !ok || raw == nil {
		if required {
			return nil, fmt.Errorf("template %s recipe field %q is required", contextPath, key)
		}
		return nil, nil
	}
	values, ok := raw.([]interface{})
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("template %s recipe field %q must be a non-empty list of strings", contextPath, key)
	}
	out := make([]string, len(values))
	for index, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("template %s recipe field %q[%d] must be a non-empty string", contextPath, key, index)
		}
		value = strings.TrimSpace(value)
		if key == "input_paths" && recipePathEscapes(value) {
			return nil, fmt.Errorf("template %s recipe input path %q must stay repository-relative", contextPath, value)
		}
		out[index] = value
	}
	return out, nil
}

func recipePathEscapes(value string) bool {
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		return true
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func recipeExamples(mapping map[string]interface{}, contextPath string) ([]RecipeExample, error) {
	raw, ok := mapping["examples"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("template %s recipe field %q is required", contextPath, "examples")
	}
	values, ok := raw.([]interface{})
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("template %s recipe field %q must be a non-empty list", contextPath, "examples")
	}
	out := make([]RecipeExample, len(values))
	for index, rawValue := range values {
		entry, ok := rawValue.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("template %s recipe examples[%d] must be a mapping", contextPath, index)
		}
		for key := range entry {
			if key != "name" && key != "command" && key != "cwd" && key != "expect" {
				return nil, fmt.Errorf("template %s recipe examples[%d] field %q is not supported", contextPath, index, key)
			}
		}
		name, err := recipeString(entry, "name", contextPath, true)
		if err != nil {
			return nil, err
		}
		command, err := recipeString(entry, "command", contextPath, true)
		if err != nil {
			return nil, err
		}
		cwd, err := recipeString(entry, "cwd", contextPath, false)
		if err != nil {
			return nil, err
		}
		if cwd == "" {
			cwd = "."
		}
		if recipePathEscapes(cwd) {
			return nil, fmt.Errorf("template %s recipe examples[%d].cwd must stay repository-relative", contextPath, index)
		}
		expect, err := recipeString(entry, "expect", contextPath, true)
		if err != nil {
			return nil, err
		}
		if expect != "pass" && expect != "block" {
			return nil, fmt.Errorf("template %s recipe examples[%d].expect must be pass or block", contextPath, index)
		}
		out[index] = RecipeExample{Name: name, Command: command, CWD: cwd, Expect: expect}
	}
	return out, nil
}
