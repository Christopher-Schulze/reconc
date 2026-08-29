package customruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/schema"
)

const (
	ConformanceSchemaURL     = schema.CustomRuntimeConformanceURL
	ConformanceFormatVersion = "reconc-custom-runtime-conformance/v1"
	MaxConformanceBytes      = 1 << 20
)

type ConformanceCapability string

const (
	ConformanceRequest  ConformanceCapability = "request"
	ConformanceResponse ConformanceCapability = "response"
	ConformanceTimeout  ConformanceCapability = "timeout"
	ConformanceFailure  ConformanceCapability = "failure"
	ConformanceLiveness ConformanceCapability = "liveness"
	ConformancePrivacy  ConformanceCapability = "privacy"
)

type ConformanceResult struct {
	ExitCode         int    `json:"exit_code"`
	Stdout           string `json:"stdout,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
	OperationalError string `json:"operational_error,omitempty"`
	TimedOut         bool   `json:"timed_out,omitempty"`
}

type ConformanceCase struct {
	Name             string                  `json:"name"`
	HostEvent        string                  `json:"host_event"`
	HostPayload      json.RawMessage         `json:"host_payload"`
	ExpectedPayload  map[string]interface{}  `json:"expected_payload"`
	Result           ConformanceResult       `json:"result"`
	ExpectedDecision Decision                `json:"expected_decision"`
	PrivateMarkers   []string                `json:"private_markers,omitempty"`
	Proves           []ConformanceCapability `json:"proves"`
}

type ConformanceSuite struct {
	Schema        string            `json:"$schema"`
	FormatVersion string            `json:"format_version"`
	Runtime       string            `json:"runtime"`
	Cases         []ConformanceCase `json:"cases"`
}

type ConformanceReport struct {
	FormatVersion string                  `json:"format_version"`
	Runtime       string                  `json:"runtime"`
	Passed        bool                    `json:"passed"`
	CaseCount     int                     `json:"case_count"`
	Capabilities  []ConformanceCapability `json:"capabilities"`
}

func DecodeConformanceSuite(body []byte) (ConformanceSuite, error) {
	if len(body) == 0 || len(body) > MaxConformanceBytes {
		return ConformanceSuite{}, fmt.Errorf("custom runtime conformance suite must be 1..%d bytes", MaxConformanceBytes)
	}
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return ConformanceSuite{}, err
	}
	if err := validateConformanceFieldPresence(body); err != nil {
		return ConformanceSuite{}, err
	}
	var suite ConformanceSuite
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&suite); err != nil {
		return ConformanceSuite{}, fmt.Errorf("decode custom runtime conformance suite: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ConformanceSuite{}, fmt.Errorf("custom runtime conformance suite must contain exactly one JSON object")
	}
	if !schema.AcceptsFormat(schema.CustomRuntimeConformance, suite.Schema, suite.FormatVersion) || suite.Cases == nil || len(suite.Cases) == 0 || len(suite.Cases) > 128 {
		return ConformanceSuite{}, fmt.Errorf("custom runtime conformance suite schema, format_version, or case count is invalid")
	}
	return suite, nil
}

func validateConformanceFieldPresence(body []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("decode custom runtime conformance fields: %w", err)
	}
	if err := requireJSONFields(document, "$schema", "format_version", "runtime", "cases"); err != nil {
		return fmt.Errorf("custom runtime conformance suite: %w", err)
	}
	var cases []map[string]json.RawMessage
	if err := json.Unmarshal(document["cases"], &cases); err != nil || cases == nil {
		return fmt.Errorf("custom runtime conformance cases must contain a list of objects")
	}
	for index, testCase := range cases {
		if err := requireJSONFields(testCase, "name", "host_event", "host_payload", "expected_payload", "result", "expected_decision", "proves"); err != nil {
			return fmt.Errorf("custom runtime conformance case[%d]: %w", index, err)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(testCase["result"], &result); err != nil || result == nil {
			return fmt.Errorf("custom runtime conformance case[%d].result must contain an object", index)
		}
		if err := requireJSONFields(result, "exit_code"); err != nil {
			return fmt.Errorf("custom runtime conformance case[%d].result: %w", index, err)
		}
	}
	return nil
}

func RunConformance(manifest Manifest, suite ConformanceSuite) (ConformanceReport, error) {
	if suite.Runtime != manifest.Runtime() {
		return ConformanceReport{}, fmt.Errorf("conformance runtime %q does not match manifest %q", suite.Runtime, manifest.Runtime())
	}
	proved := map[ConformanceCapability]struct{}{}
	names := map[string]struct{}{}
	for index, testCase := range suite.Cases {
		if strings.TrimSpace(testCase.Name) == "" {
			return ConformanceReport{}, fmt.Errorf("conformance case[%d] has no name", index)
		}
		if _, duplicate := names[testCase.Name]; duplicate {
			return ConformanceReport{}, fmt.Errorf("conformance case name %q is duplicated", testCase.Name)
		}
		names[testCase.Name] = struct{}{}
		route, ok := manifest.Route(testCase.HostEvent)
		if !ok {
			return ConformanceReport{}, fmt.Errorf("conformance case %q references unknown host event %q", testCase.Name, testCase.HostEvent)
		}
		request, _, err := NormalizeHostPayload(manifest, route, testCase.HostPayload)
		if err != nil {
			return ConformanceReport{}, fmt.Errorf("conformance case %q request: %w", testCase.Name, err)
		}
		if !reflect.DeepEqual(request.Payload, testCase.ExpectedPayload) {
			return ConformanceReport{}, fmt.Errorf("conformance case %q neutral payload does not match expected_payload", testCase.Name)
		}
		var operationalError error
		if testCase.Result.OperationalError != "" {
			operationalError = errors.New(testCase.Result.OperationalError)
		}
		response := BuildResponse(manifest, route, testCase.Result.ExitCode, testCase.Result.Stdout, testCase.Result.Stderr, operationalError, testCase.Result.TimedOut)
		if response.Decision != testCase.ExpectedDecision {
			return ConformanceReport{}, fmt.Errorf("conformance case %q decision %q does not match %q", testCase.Name, response.Decision, testCase.ExpectedDecision)
		}
		responseBody, err := BoundResponse(response, route.MaxOutputBytes)
		if err != nil {
			return ConformanceReport{}, fmt.Errorf("conformance case %q response: %w", testCase.Name, err)
		}
		requestBody, err := json.Marshal(request)
		if err != nil {
			return ConformanceReport{}, fmt.Errorf("conformance case %q request identity: %w", testCase.Name, err)
		}
		for _, marker := range testCase.PrivateMarkers {
			if marker == "" || bytes.Contains(requestBody, []byte(marker)) || bytes.Contains(responseBody, []byte(marker)) {
				return ConformanceReport{}, fmt.Errorf("conformance case %q leaked private marker", testCase.Name)
			}
		}
		for _, capability := range testCase.Proves {
			if err := validateConformanceClaim(capability, testCase, manifest, route); err != nil {
				return ConformanceReport{}, fmt.Errorf("conformance case %q: %w", testCase.Name, err)
			}
			proved[capability] = struct{}{}
		}
	}
	required := []ConformanceCapability{ConformanceRequest, ConformanceResponse, ConformanceTimeout, ConformanceFailure, ConformanceLiveness, ConformancePrivacy}
	for _, capability := range required {
		if _, ok := proved[capability]; !ok {
			return ConformanceReport{}, fmt.Errorf("conformance suite does not prove %s behavior", capability)
		}
	}
	capabilities := make([]ConformanceCapability, 0, len(proved))
	for capability := range proved {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(left, right int) bool { return capabilities[left] < capabilities[right] })
	return ConformanceReport{FormatVersion: ConformanceFormatVersion, Runtime: manifest.Runtime(), Passed: true, CaseCount: len(suite.Cases), Capabilities: capabilities}, nil
}

func validateConformanceClaim(capability ConformanceCapability, testCase ConformanceCase, manifest Manifest, route Route) error {
	switch capability {
	case ConformanceRequest, ConformanceResponse:
		return nil
	case ConformanceLiveness:
		digest, err := manifest.Digest()
		if err != nil {
			return fmt.Errorf("encode manifest identity for liveness: %w", err)
		}
		return ValidateLivenessRecord(LivenessRecord{
			Schema: schema.Resolve(schema.CustomRuntimeLiveness), FormatVersion: LivenessFormatVersion,
			Runtime: manifest.Runtime(), HostEvent: route.HostEvent,
			ObservedAt: "2000-01-01T00:00:00Z", ManifestDigest: digest,
		})
	case ConformanceTimeout:
		if !testCase.Result.TimedOut {
			return fmt.Errorf("timeout proof requires result.timed_out")
		}
	case ConformanceFailure:
		if testCase.Result.OperationalError == "" {
			return fmt.Errorf("failure proof requires result.operational_error")
		}
	case ConformancePrivacy:
		if len(testCase.PrivateMarkers) == 0 {
			return fmt.Errorf("privacy proof requires private_markers")
		}
	default:
		return fmt.Errorf("unknown conformance capability %q", capability)
	}
	return nil
}
