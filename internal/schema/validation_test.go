package schema_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dlclark/regexp2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"reconc.dev/reconc/internal/compiler"
	contractschema "reconc.dev/reconc/internal/schema"
)

func TestEveryRegisteredSchemaCompilesOffline(t *testing.T) {
	compiled := compileRegisteredSchemas(t)
	if got, want := len(compiled), len(contractschema.Contracts()); got != want {
		t.Fatalf("compiled schema count = %d, want %d", got, want)
	}
}

func TestEveryRegisteredSchemaHasUniqueJSONObjectKeys(t *testing.T) {
	for _, contract := range contractschema.Contracts() {
		t.Run(string(contract.Artifact)+"-v"+contract.SchemaVersion, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(contract.LocalPath)))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateUniqueSchemaJSONKeys(body); err != nil {
				t.Fatalf("%s: %v", contract.LocalPath, err)
			}
		})
	}
}

func validateUniqueSchemaJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := validateUniqueSchemaJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("schema JSON contains trailing data")
	}
	return nil
}

func validateUniqueSchemaJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 256 {
		return fmt.Errorf("schema JSON exceeds 256 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("schema JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("schema JSON contains duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateUniqueSchemaJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueSchemaJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("schema JSON contains unexpected delimiter %q", delimiter)
	}
	closing, err := decoder.Token()
	if err != nil || closing != matchingSchemaJSONDelimiter(delimiter) {
		return fmt.Errorf("schema JSON container is malformed")
	}
	return nil
}

func matchingSchemaJSONDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}

func TestEveryCurrentSchemaValidatesARepresentativeArtifact(t *testing.T) {
	compiled := compileRegisteredSchemas(t)
	for _, contract := range contractschema.Contracts() {
		if contract.State != contractschema.StateCurrent {
			continue
		}
		t.Run(string(contract.Artifact), func(t *testing.T) {
			artifact := representativeArtifact(t, compiled[contract.DefaultURL])
			if contract.Artifact == contractschema.ActionLedger {
				completeActionLedgerRepresentative(artifact)
			}
			if err := compiled[contract.DefaultURL].Validate(artifact); err != nil {
				t.Fatalf("representative artifact is invalid: %v\nartifact: %s", err, mustJSON(t, artifact))
			}
			mutated := cloneJSONValue(t, artifact).(map[string]any)
			mutated["__unregistered_schema_field"] = true
			if err := compiled[contract.DefaultURL].Validate(mutated); err == nil {
				t.Fatal("schema accepted an unregistered semantic mutation")
			}
		})
	}
}

func TestEveryLegacySchemaValidatesARepresentativeArtifact(t *testing.T) {
	compiled := compileRegisteredSchemas(t)
	for _, contract := range contractschema.Contracts() {
		if contract.State != contractschema.StateLegacy {
			continue
		}
		t.Run(string(contract.Artifact)+"-v"+contract.SchemaVersion, func(t *testing.T) {
			artifact := representativeArtifact(t, compiled[contract.DefaultURL])
			if contract.Artifact == contractschema.ActionLedger {
				completeActionLedgerRepresentative(artifact)
			}
			if err := compiled[contract.DefaultURL].Validate(artifact); err != nil {
				t.Fatalf("representative legacy artifact is invalid: %v\nartifact: %s", err, mustJSON(t, artifact))
			}
			mutated := cloneJSONValue(t, artifact).(map[string]any)
			mutated["__unregistered_schema_field"] = true
			if err := compiled[contract.DefaultURL].Validate(mutated); err == nil {
				t.Fatal("legacy schema accepted an unregistered semantic mutation")
			}
		})
	}
}

func completeActionLedgerRepresentative(artifact map[string]any) {
	completeness := artifact["decision"].(map[string]any)["completeness"].(map[string]any)
	for _, field := range []string{
		"request_complete", "policy_complete", "identity_complete",
		"context_complete", "state_complete", "phase_complete",
	} {
		completeness[field] = true
	}
	completeness["missing"] = []any{}
}

func TestEveryLegacyPolicyLockSchemaMigratesToAValidCurrentArtifact(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	compiled := compileRegisteredSchemas(t)
	current, ok := contractschema.CurrentContract(contractschema.PolicyLock)
	if !ok {
		t.Fatal("registry has no current policy-lock contract")
	}
	currentSchema := compiled[current.DefaultURL]
	for _, contract := range contractschema.Contracts() {
		if contract.Artifact != contractschema.PolicyLock || contract.State != contractschema.StateLegacy {
			continue
		}
		t.Run("v"+contract.SchemaVersion, func(t *testing.T) {
			artifact := representativeArtifact(t, compiled[contract.DefaultURL])
			artifact["$schema"] = contract.DefaultURL
			if contract.SchemaVersion == "1" {
				canonical := mustJSON(t, map[string]any{
					"source_precedence": artifact["source_precedence"],
					"sources":           artifact["sources"],
				})
				digest := sha256.Sum256(canonical)
				artifact["source_digest"] = hex.EncodeToString(digest[:])
			} else {
				digest, err := compiler.ComputeLockDigest(artifact)
				if err != nil {
					t.Fatalf("compute legacy lock digest: %v", err)
				}
				artifact["lock_digest"] = digest
			}
			if err := compiled[contract.DefaultURL].Validate(artifact); err != nil {
				t.Fatalf("legacy migration input is invalid: %v", err)
			}
			migrated, applied, err := compiler.MigrateLockfile(artifact)
			if err != nil {
				t.Fatalf("migrate lockfile: %v", err)
			}
			if len(applied) == 0 {
				t.Fatal("legacy lockfile migration applied no steps")
			}
			if err := currentSchema.Validate(migrated); err != nil {
				t.Fatalf("migrated current lockfile is invalid: %v\nartifact: %s", err, mustJSON(t, migrated))
			}
		})
	}
}

func TestCurrentPolicyLockSchemaRejectsDeadCheckPathFields(t *testing.T) {
	t.Setenv("RECONC_SCHEMA_BASE_URL", "")
	compiled := compileRegisteredSchemas(t)
	current, ok := contractschema.CurrentContract(contractschema.PolicyLock)
	if !ok {
		t.Fatal("registry has no current policy-lock contract")
	}
	definition := compiled[current.DefaultURL]
	for _, field := range []string{"before_paths", "when_paths"} {
		artifact := representativeArtifact(t, definition)
		artifact["rule_count"] = json.Number("1")
		artifact["rules"] = []any{map[string]any{
			"id": "gate", "kind": "all_of", "message": "gate", "when_paths": []any{"src/**"},
			"checks": []any{map[string]any{
				"kind": "require_claim", "claims": []any{"approved"}, field: []any{"dead/**"},
			}},
		}}
		if err := definition.Validate(artifact); err == nil {
			t.Fatalf("current policy-lock schema accepted check.%s", field)
		}
	}
}

func compileRegisteredSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseRegexpEngine(compileECMAScriptRegexp)
	for _, contract := range contractschema.Contracts() {
		document := readSchemaResource(t, contract.LocalPath)
		if err := compiler.AddResource(contract.DefaultURL, document); err != nil {
			t.Fatalf("register %s: %v", contract.DefaultURL, err)
		}
		for _, alias := range contract.Aliases {
			if err := compiler.AddResource(alias.URL, document); err != nil {
				t.Fatalf("register compatibility alias %s: %v", alias.URL, err)
			}
		}
	}
	compiler.UseLoader(rejectNetworkSchemaLoader{})

	compiled := make(map[string]*jsonschema.Schema, len(contractschema.Contracts()))
	for _, contract := range contractschema.Contracts() {
		value, err := compiler.Compile(contract.DefaultURL)
		if err != nil {
			t.Fatalf("compile %s: %v", contract.LocalPath, err)
		}
		compiled[contract.DefaultURL] = value
	}
	return compiled
}

func readSchemaResource(t *testing.T, localPath string) any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(localPath)))
	if err != nil {
		t.Fatalf("read %s: %v", localPath, err)
	}
	var document any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode %s: %v", localPath, err)
	}
	return document
}

func representativeArtifact(t *testing.T, definition *jsonschema.Schema) map[string]any {
	t.Helper()
	value := exampleForSchema(t, definition, 0)
	artifact, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("root schema generated %T, want object", value)
	}
	return artifact
}

func exampleForSchema(t *testing.T, definition *jsonschema.Schema, depth int) any {
	t.Helper()
	if definition == nil {
		return nil
	}
	if depth > 128 {
		t.Fatalf("schema example recursion exceeded 128 levels at %s", definition.Location)
	}
	if definition.Const != nil {
		return cloneJSONValue(t, *definition.Const)
	}
	if definition.Enum != nil && len(definition.Enum.Values) > 0 {
		return cloneJSONValue(t, definition.Enum.Values[0])
	}

	var value any
	if definition.Ref != nil {
		value = exampleForSchema(t, definition.Ref, depth+1)
	}
	if value == nil {
		value = exampleForDeclaredType(t, definition, depth)
	}
	value = mergeSchemaExamples(t, value, definition.AllOf, depth)
	if len(definition.AnyOf) > 0 {
		value = mergeJSONValues(value, exampleForSchema(t, definition.AnyOf[0], depth+1))
	}
	if len(definition.OneOf) > 0 {
		value = mergeJSONValues(value, exampleForSchema(t, definition.OneOf[0], depth+1))
	}
	value = applyConditionalExample(t, value, definition, depth)
	return value
}

func exampleForDeclaredType(t *testing.T, definition *jsonschema.Schema, depth int) any {
	t.Helper()
	types := []string{}
	if definition.Types != nil {
		types = definition.Types.ToStrings()
	}
	if len(definition.Properties) > 0 || len(definition.Required) > 0 {
		types = append(types, "object")
	}
	for _, preferred := range []string{"object", "array", "string", "integer", "number", "boolean", "null"} {
		if !containsString(types, preferred) {
			continue
		}
		switch preferred {
		case "object":
			value := map[string]any{}
			fields := append([]string(nil), definition.Required...)
			sort.Strings(fields)
			required := make(map[string]bool, len(fields))
			for _, name := range fields {
				required[name] = true
				property, ok := definition.Properties[name]
				if !ok {
					continue
				}
				value[name] = exampleForSchema(t, property, depth+1)
			}
			propertyNames := make([]string, 0, len(definition.Properties))
			for name := range definition.Properties {
				propertyNames = append(propertyNames, name)
			}
			sort.Strings(propertyNames)
			for _, name := range propertyNames {
				property := definition.Properties[name]
				if !required[name] && schemaHasConst(property) {
					value[name] = exampleForSchema(t, property, depth+1)
				}
			}
			return value
		case "array":
			count := 0
			if definition.MinItems != nil {
				count = *definition.MinItems
			}
			values := make([]any, count)
			for index := range values {
				if definition.Items2020 != nil {
					values[index] = exampleForSchema(t, definition.Items2020, depth+1)
				}
			}
			return values
		case "string":
			return exampleString(t, definition)
		case "integer":
			return minimumInteger(definition.Minimum)
		case "number":
			if definition.Minimum == nil {
				return float64(0)
			}
			value, _ := definition.Minimum.Float64()
			return value
		case "boolean":
			return false
		case "null":
			return nil
		}
	}
	if definition.Default != nil {
		return cloneJSONValue(t, *definition.Default)
	}
	return map[string]any{}
}

func schemaHasConst(definition *jsonschema.Schema) bool {
	if definition == nil {
		return false
	}
	if definition.Const != nil {
		return true
	}
	return definition.Ref != nil && schemaHasConst(definition.Ref)
}

func mergeSchemaExamples(t *testing.T, base any, definitions []*jsonschema.Schema, depth int) any {
	t.Helper()
	value := base
	for _, definition := range definitions {
		if definition.If != nil {
			value = applyConditionalExample(t, value, definition, depth+1)
			continue
		}
		value = mergeJSONValues(value, exampleForSchema(t, definition, depth+1))
	}
	return value
}

func applyConditionalExample(t *testing.T, value any, definition *jsonschema.Schema, depth int) any {
	t.Helper()
	if definition.If == nil {
		return value
	}
	selected := definition.Else
	if definition.If.Validate(value) == nil {
		selected = definition.Then
	}
	if selected == nil {
		return value
	}
	return mergeJSONValues(value, exampleForSchema(t, selected, depth+1))
}

func mergeJSONValues(base any, overlay any) any {
	baseObject, baseOK := base.(map[string]any)
	overlayObject, overlayOK := overlay.(map[string]any)
	if !baseOK || !overlayOK {
		if overlay != nil {
			return overlay
		}
		return base
	}
	for name, value := range overlayObject {
		if current, exists := baseObject[name]; exists {
			baseObject[name] = mergeJSONValues(current, value)
			continue
		}
		baseObject[name] = value
	}
	return baseObject
}

func exampleString(t *testing.T, definition *jsonschema.Schema) string {
	t.Helper()
	candidates := []string{
		"a",
		"/a",
		"0.9.6",
		"reconc-v0.9.6",
		"custom:a",
		strings.Repeat("a", 40),
		strings.Repeat("a", 43),
		strings.Repeat("a", 64),
		strings.Repeat("a", 86),
		"sha256:" + strings.Repeat("a", 64),
		"hmac-sha256:v1:ledger-key:" + strings.Repeat("a", 64),
		"hmac-sha256:v1:" + strings.Repeat("a", 32) + ":" + strings.Repeat("a", 64),
		"act_" + strings.Repeat("a", 26),
		"2000-01-01",
		"2000-01-01T00:00:00Z",
		"https://example.test/schema.json",
	}
	if definition.Format != nil {
		switch definition.Format.Name {
		case "date-time":
			candidates = append([]string{"2000-01-01T00:00:00Z"}, candidates...)
		case "date":
			candidates = append([]string{"2000-01-01"}, candidates...)
		case "uri":
			candidates = append([]string{"https://example.test/schema.json"}, candidates...)
		}
	}
	minimum := 0
	if definition.MinLength != nil {
		minimum = *definition.MinLength
	}
	maximum := int(^uint(0) >> 1)
	if definition.MaxLength != nil {
		maximum = *definition.MaxLength
	}
	if minimum > 0 {
		candidates = append(candidates, strings.Repeat("a", minimum))
	}
	for _, candidate := range candidates {
		length := len([]rune(candidate))
		if length < minimum || length > maximum || (definition.Pattern != nil && !definition.Pattern.MatchString(candidate)) {
			continue
		}
		if definition.Format != nil && definition.Format.Validate(candidate) != nil {
			continue
		}
		if definition.Not != nil && definition.Not.Validate(candidate) == nil {
			continue
		}
		return candidate
	}
	t.Fatalf("no deterministic representative string satisfies %s", definition.Location)
	return ""
}

func minimumInteger(minimum *big.Rat) int64 {
	if minimum == nil {
		return 0
	}
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(minimum.Num(), minimum.Denom(), remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneJSONValue(t *testing.T, value any) any {
	t.Helper()
	body := mustJSON(t, value)
	var clone any
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatalf("clone JSON value: %v", err)
	}
	return clone
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON value: %v", err)
	}
	return body
}

type rejectNetworkSchemaLoader struct{}

func (rejectNetworkSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("unregistered schema URL %q; registry validation is offline", url)
}

type ecmaScriptRegexp regexp2.Regexp

func (regexp *ecmaScriptRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(regexp).MatchString(value)
	return err == nil && matched
}

func (regexp *ecmaScriptRegexp) String() string {
	return (*regexp2.Regexp)(regexp).String()
}

func compileECMAScriptRegexp(expression string) (jsonschema.Regexp, error) {
	compiled, err := regexp2.Compile(expression, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*ecmaScriptRegexp)(compiled), nil
}
