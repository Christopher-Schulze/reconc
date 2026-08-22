package actioninspect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"reconc.dev/reconc/internal/action"
)

const maxSchemaValidationWork = 4_000_000

type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema resources are disabled")
}

func CompileOutputSchema(raw []byte) (*OutputSchema, error) {
	if len(raw) == 0 || len(raw) > MaxOutputSchemaBytes {
		return nil, fmt.Errorf("output schema must contain 1 to %d bytes", MaxOutputSchemaBytes)
	}
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode output schema: %s", action.JSONErrorKindOf(err))
	}
	items, _, err := inspectSchemaValue(value, 0)
	if err != nil {
		return nil, err
	}
	if items > MaxOutputSchemaItems {
		return nil, fmt.Errorf("output schema exceeds %d values", MaxOutputSchemaItems)
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("canonicalize output schema: %w", err)
	}
	digest := sha256.Sum256(canonical)
	identity := "sha256:" + hex.EncodeToString(digest[:])
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(canonical))
	if err != nil {
		return nil, fmt.Errorf("decode canonical output schema: %w", err)
	}
	location := "urn:reconc:output-schema:" + hex.EncodeToString(digest[:])
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(offlineSchemaLoader{})
	compiler.UseRegexpEngine(compileLinearSchemaRegexp)
	if err := compiler.AddResource(location, document); err != nil {
		return nil, fmt.Errorf("register output schema")
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("compile output schema")
	}
	return &OutputSchema{identity: identity, schema: compiled, items: items}, nil
}

func (s *OutputSchema) Validate(value action.Value) error {
	if s == nil || s.schema == nil || !action.ValidSHA256Identity(s.identity) || s.items <= 0 {
		return fmt.Errorf("output schema is unavailable")
	}
	items, _, err := countValue(value, 0, action.MaxJSONDepth)
	if err != nil {
		return err
	}
	if items > action.MaxJSONItems || items*s.items > maxSchemaValidationWork {
		return fmt.Errorf("output schema validation work exceeds its boundary")
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return fmt.Errorf("canonicalize structured content: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(canonical))
	if err != nil {
		return fmt.Errorf("decode structured content: %w", err)
	}
	if err := s.schema.Validate(document); err != nil {
		return fmt.Errorf("structured content does not match output schema")
	}
	return nil
}

func inspectSchemaValue(value action.Value, depth int) (int, int, error) {
	items, maximumDepth, err := countValue(value, depth, action.MaxJSONDepth)
	if err != nil {
		return 0, 0, err
	}
	if value.Kind() != action.ValueObject && depth == 0 {
		return 0, 0, fmt.Errorf("output schema root must be an object")
	}
	if err := validateSchemaReferences(value); err != nil {
		return 0, 0, err
	}
	return items, maximumDepth, nil
}

func validateSchemaReferences(value action.Value) error {
	if value.Kind() == action.ValueBool {
		return nil
	}
	length, ok := value.ObjectLen()
	if !ok {
		return fmt.Errorf("output subschema must be an object or boolean")
	}
	for index := 0; index < length; index++ {
		member, _ := value.ObjectMember(index)
		if err := validateSchemaKeyword(member); err != nil {
			return err
		}
	}
	return validateChildSchemas(value)
}

func validateSchemaKeyword(member action.Member) error {
	if member.Name == "$schema" {
		value, ok := member.Value.Text()
		if !ok || value != "https://json-schema.org/draft/2020-12/schema" {
			return fmt.Errorf("output schema must use Draft 2020-12")
		}
	}
	if member.Name == "$ref" || member.Name == "$dynamicRef" {
		value, ok := member.Value.Text()
		if !ok || value != "" && !strings.HasPrefix(value, "#") {
			return fmt.Errorf("output schema references must remain inside the declared document")
		}
	}
	if member.Name == "pattern" {
		return validateSchemaPattern(member.Value)
	}
	return nil
}

func validateChildSchemas(value action.Value) error {
	for _, keyword := range []string{
		"additionalProperties", "contains", "contentSchema", "else", "if", "items", "not",
		"propertyNames", "then", "unevaluatedItems", "unevaluatedProperties",
	} {
		if child, ok := value.Lookup(keyword); ok {
			if err := validateSchemaReferences(child); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := value.Lookup(keyword); ok {
			if err := validateSchemaArray(children); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"$defs", "dependentSchemas", "patternProperties", "properties"} {
		if children, ok := value.Lookup(keyword); ok {
			if err := validateSchemaMap(children, keyword == "patternProperties"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSchemaArray(value action.Value) error {
	length, ok := value.ArrayLen()
	if !ok {
		return fmt.Errorf("output schema combinator must be an array")
	}
	for index := 0; index < length; index++ {
		item, _ := value.ArrayItem(index)
		if err := validateSchemaReferences(item); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaMap(value action.Value, keysArePatterns bool) error {
	length, ok := value.ObjectLen()
	if !ok {
		return fmt.Errorf("output schema map keyword must be an object")
	}
	for index := 0; index < length; index++ {
		member, _ := value.ObjectMember(index)
		if keysArePatterns {
			if err := validateSchemaPatternText(member.Name); err != nil {
				return err
			}
		}
		if err := validateSchemaReferences(member.Value); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaPattern(value action.Value) error {
	pattern, ok := value.Text()
	if !ok {
		return fmt.Errorf("output schema pattern must be a string")
	}
	return validateSchemaPatternText(pattern)
}

func validateSchemaPatternText(pattern string) error {
	if len(pattern) > MaxOutputSchemaPatternBytes {
		return fmt.Errorf("output schema pattern exceeds %d bytes", MaxOutputSchemaPatternBytes)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("output schema pattern is not linear-regexp compatible")
	}
	return nil
}

func compileLinearSchemaRegexp(pattern string) (jsonschema.Regexp, error) {
	if err := validateSchemaPatternText(pattern); err != nil {
		return nil, err
	}
	return regexp.Compile(pattern)
}

func countValue(value action.Value, depth, maxDepth int) (int, int, error) {
	if depth > maxDepth {
		return 0, depth, fmt.Errorf("JSON value exceeds %d container levels", maxDepth)
	}
	items := 1
	maximumDepth := depth
	switch value.Kind() {
	case action.ValueArray:
		length, _ := value.ArrayLen()
		for index := 0; index < length; index++ {
			item, _ := value.ArrayItem(index)
			count, childDepth, err := countValue(item, depth+1, maxDepth)
			if err != nil {
				return 0, childDepth, err
			}
			items += count
			maximumDepth = max(maximumDepth, childDepth)
		}
	case action.ValueObject:
		length, _ := value.ObjectLen()
		for index := 0; index < length; index++ {
			member, _ := value.ObjectMember(index)
			count, childDepth, err := countValue(member.Value, depth+1, maxDepth)
			if err != nil {
				return 0, childDepth, err
			}
			items += count
			maximumDepth = max(maximumDepth, childDepth)
		}
	}
	return items, maximumDepth, nil
}
