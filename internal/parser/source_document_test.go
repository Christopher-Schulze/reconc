package parser

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func TestParseRuleDocumentsDecodesEachRuleSourceOnce(t *testing.T) {
	t.Parallel()
	bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{
		{
			Kind:    policy.SourceCompilerConfig,
			Path:    ".reconc.yml",
			Content: "rules:\n  - id: compiler\n    kind: deny_write\n    paths: [generated/**]\n    message: blocked\nactions:\n  defaults:\n    declared_tool: warn\n",
		},
		{
			Kind:    policy.SourcePolicyFile,
			Path:    ".reconc/impact/candidate.yml",
			BlockID: policy.ImpactCandidateBlockPrefix + "candidate",
			Content: "actions:\n  rules:\n    - id: candidate-action\n      decision: block\n",
		},
		{
			Kind:    policy.SourcePolicyFile,
			Path:    "policies/repository.yml",
			BlockID: "repository",
			Content: "rules:\n  - id: repository\n    kind: deny_write\n    paths: [vendor/**]\n    message: blocked\n",
		},
		{Kind: policy.SourceAgentsMD, Path: "AGENTS.md", Content: "prose"},
	}}
	counts := make(map[string]int)
	decode := func(source policy.PolicySource) (*parserSourceDocument, error) {
		counts[source.Path]++
		return decodeRuleSourceDocumentBounded(source)
	}
	parsed, err := parseRuleDocumentsWithDecoder(bundle, decode)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Rules) != 2 || parsed.Actions == nil || len(parsed.Actions.Rules) != 1 {
		t.Fatalf("parsed policy = %#v", parsed)
	}
	want := map[string]int{
		".reconc.yml":                  1,
		".reconc/impact/candidate.yml": 1,
		"policies/repository.yml":      1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("decode counts = %#v, want %#v", counts, want)
	}
}

func TestSharedSourceDocumentMatchesLegacyTwoDecoderBehavior(t *testing.T) {
	t.Parallel()
	corpus := []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "null", body: "null\n"},
		{name: "rules and actions", body: "rules:\n  - id: deny\n    kind: deny_write\n    paths: [generated/**]\n    message: blocked\nactions:\n  defaults:\n    declared_tool: warn\n"},
		{name: "canonical scalars", body: "actions:\n  rules:\n    - id: scalar\n      decision: block\n      when:\n        predicate:\n          source: context\n          pointer: /value\n          op: eq\n          value: {null: null, bool: true, int: 42, float: 4.2, string: value}\n"},
		{name: "explicit empty string", body: "actions:\n  rules:\n    - id: empty\n      decision: block\n      message: \"\"\n"},
		{name: "ambiguous boolean", body: "actions:\n  tools:\n    - id: tool\n      transport: mcp_stdio\n      server_label: server\n      tool: query\n      effect: {kind: external}\n      ledger_name_safe: yes\n"},
		{name: "ambiguous integer", body: "actions:\n  rules:\n    - id: number\n      decision: block\n      when:\n        predicate: {source: context, pointer: /value, op: eq, value: 01}\n"},
		{name: "ambiguous null", body: "actions:\n  rules:\n    - id: null\n      decision: block\n      when:\n        predicate: {source: context, pointer: /value, op: eq, value: ~}\n"},
		{name: "alias", body: "message: &message blocked\nactions:\n  rules:\n    - id: alias\n      decision: block\n      when:\n        predicate: {source: context, pointer: /value, op: eq, value: *message}\n"},
		{name: "custom tag", body: "actions:\n  rules:\n    - id: tagged\n      decision: block\n      when:\n        predicate: {source: context, pointer: /value, op: eq, value: !secret value}\n"},
		{name: "duplicate key", body: "actions:\n  defaults:\n    cache: never\n    cache: always\n"},
		{name: "trailing document", body: "actions: {}\n---\nactions: {}\n"},
		{name: "non mapping", body: "[one, two]\n"},
		{name: "malformed", body: "actions: [\n"},
	}
	for _, test := range corpus {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bundle := actionBundle(test.body)
			gotPolicy, gotErr := parseRuleDocumentsWithDecoder(bundle, decodeRuleSourceDocumentBounded)
			wantPolicy, wantErr := parseRuleDocumentsWithDecoder(bundle, legacyTwoPassSourceDocumentDecoder)
			assertParserParity(t, gotPolicy, gotErr, wantPolicy, wantErr)
		})
	}
}

func BenchmarkParseMixedSourceDocument(b *testing.B) {
	benchmarks := []struct {
		name string
		body string
	}{
		{
			name: "small",
			body: "rules:\n  - id: deny\n    kind: deny_write\n    paths: [generated/**]\n    message: blocked\nactions:\n  defaults:\n    declared_tool: warn\n",
		},
		{name: "maximum-legal-rules", body: maximumLegalMixedDocument()},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			bundle := actionBundle(benchmark.body)
			decodes := 0
			decode := func(source policy.PolicySource) (*parserSourceDocument, error) {
				decodes++
				return decodeRuleSourceDocumentBounded(source)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(benchmark.body)))
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if _, err := parseRuleDocumentsWithDecoder(bundle, decode); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if decodes != b.N {
				b.Fatalf("yaml decodes = %d, want %d", decodes, b.N)
			}
		})
	}
}

func maximumLegalMixedDocument() string {
	var body strings.Builder
	body.WriteString("rules:\n")
	for index := 0; index < maxParserRules; index++ {
		fmt.Fprintf(&body, "  - id: rule-%d\n    kind: deny_write\n    paths: [generated/%d]\n    message: blocked\n", index, index)
	}
	body.WriteString("actions:\n  defaults:\n    declared_tool: warn\n")
	return body.String()
}

func legacyTwoPassSourceDocumentDecoder(source policy.PolicySource) (*parserSourceDocument, error) {
	mapping, err := decodeYAMLMappingBounded(source.Content, source.Path)
	if err != nil {
		return nil, err
	}
	root, err := legacyDecodeActionDocument(source.Content, source.Path)
	if err != nil {
		return nil, err
	}
	return &parserSourceDocument{root: root, mapping: mapping}, nil
}

func legacyDecodeActionDocument(raw, context string) (*yaml.Node, error) {
	if strings.TrimSpace(raw) == "" {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, actionErrorWithCause("invalid yaml in "+context, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, actionErrorWithCause("invalid trailing yaml in "+context, err)
		}
		return nil, actionError("expected exactly one document in " + context)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, actionError("expected a YAML mapping in " + context)
	}
	return document.Content[0], nil
}

func assertParserParity(t *testing.T, gotPolicy *ParsedPolicy, gotErr error, wantPolicy *ParsedPolicy, wantErr error) {
	t.Helper()
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("shared error = %v, legacy error = %v", gotErr, wantErr)
	}
	if gotErr != nil {
		var gotValidation, wantValidation *rerrors.RuleValidationError
		if !errors.As(gotErr, &gotValidation) || !errors.As(wantErr, &wantValidation) {
			t.Fatalf("error classes differ: shared=%T legacy=%T", gotErr, wantErr)
		}
		return
	}
	if !reflect.DeepEqual(gotPolicy, wantPolicy) {
		t.Fatalf("parsed policies differ:\nshared: %#v\nlegacy: %#v", gotPolicy, wantPolicy)
	}
}
