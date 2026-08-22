package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/dlclark/regexp2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"reconc.dev/reconc/internal/yamlbound"
	schemaassets "reconc.dev/reconc/schemas"
)

var loadPolicyConfigSchema = sync.OnceValues(compilePolicyConfigSchema)

// ValidatePolicyConfigYAML validates one YAML document against the exact
// shipped current policy-config JSON Schema. It performs no network access.
func ValidatePolicyConfigYAML(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("policy YAML must contain an object")
	}
	_, document, err := yamlbound.DecodeMapping(body, "policy-config schema candidate")
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("policy YAML must contain an object")
	}
	if err != nil {
		return fmt.Errorf("bound policy YAML for schema validation: %w", err)
	}
	definition, err := loadPolicyConfigSchema()
	if err != nil {
		return err
	}
	if err := definition.Validate(document); err != nil {
		return fmt.Errorf("policy-config schema: %w", err)
	}
	return nil
}

func compilePolicyConfigSchema() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseRegexpEngine(compilePolicyConfigRegexp)
	for _, version := range []string{"2", "4"} {
		contract, ok := ContractVersion(PolicyConfig, version)
		if !ok {
			return nil, fmt.Errorf("policy-config schema v%s is not registered", version)
		}
		document, err := decodePolicyConfigSchema(version)
		if err != nil {
			return nil, err
		}
		if err := compiler.AddResource(contract.DefaultURL, document); err != nil {
			return nil, fmt.Errorf("register policy-config schema v%s: %w", version, err)
		}
	}
	compiler.UseLoader(rejectPolicyConfigNetworkLoader{})
	definition, err := compiler.Compile(PolicyConfigURL)
	if err != nil {
		return nil, fmt.Errorf("compile shipped policy-config schema: %w", err)
	}
	return definition, nil
}

func decodePolicyConfigSchema(version string) (any, error) {
	body, err := schemaassets.PolicyConfig(version)
	if err != nil {
		return nil, err
	}
	var document any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode embedded policy-config schema v%s: %w", version, err)
	}
	return document, nil
}

type rejectPolicyConfigNetworkLoader struct{}

func (rejectPolicyConfigNetworkLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("unregistered policy-config schema URL %q", url)
}

type policyConfigRegexp regexp2.Regexp

func (regexp *policyConfigRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(regexp).MatchString(value)
	return err == nil && matched
}

func (regexp *policyConfigRegexp) String() string {
	return (*regexp2.Regexp)(regexp).String()
}

func compilePolicyConfigRegexp(expression string) (jsonschema.Regexp, error) {
	compiled, err := regexp2.Compile(expression, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*policyConfigRegexp)(compiled), nil
}
