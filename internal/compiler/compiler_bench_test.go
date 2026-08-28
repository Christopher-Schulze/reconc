package compiler

import (
	"encoding/json"
	"strconv"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

func benchmarkSourceBundle() *ingest.SourceBundle {
	sources := make([]policy.PolicySource, 256)
	for index := range sources {
		sources[index] = policy.PolicySource{
			Kind: policy.SourcePolicyFile, Path: "policies/r" + strconv.Itoa(index) + ".yml",
			Content: "rules:\n  - id: rule-" + strconv.Itoa(index) + "\n    kind: deny_write\n    paths: ['src/**']\n",
		}
	}
	return &ingest.SourceBundle{Sources: sources}
}

func BenchmarkCompileSourceProvenancePrepared(b *testing.B) {
	bundle := benchmarkSourceBundle()
	provenance, err := compileSourceProvenance(bundle)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := computeSerializedSourceDigest(provenance.records); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileSourceProvenanceRebuild(b *testing.B) {
	bundle := benchmarkSourceBundle()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		first := make([]interface{}, 0, len(bundle.Sources))
		for _, source := range bundle.Sources {
			first = append(first, sourceToMap(source))
		}
		if _, err := computeSerializedSourceDigest(first); err != nil {
			b.Fatal(err)
		}
		second := make([]interface{}, 0, len(bundle.Sources))
		for _, source := range bundle.Sources {
			second = append(second, sourceToMap(source))
		}
		if _, err := computeSerializedSourceDigest(second); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNormalizeJSONValueOnce(b *testing.B) {
	value := map[string]interface{}{"rules": []interface{}{map[string]interface{}{"id": "rule", "count": json.Number("9007199254740993")}}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := normalizeJSONValueWithBytes(value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNormalizeJSONValueTwice(b *testing.B) {
	value := map[string]interface{}{"rules": []interface{}{map[string]interface{}{"id": "rule", "count": json.Number("9007199254740993")}}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := normalizeJSONValue(value); err != nil {
			b.Fatal(err)
		}
		if _, err := normalizeJSONValue(value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompilerSerializationStages(b *testing.B) {
	payload := benchmarkCompilerPayload()
	normalizedValue, canonical, err := normalizeJSONValueWithBytes(payload)
	if err != nil {
		b.Fatal(err)
	}
	normalized := normalizedValue.(map[string]interface{})
	digest, err := ComputeLockDigest(normalized)
	if err != nil {
		b.Fatal(err)
	}
	withDigest := make(map[string]interface{}, len(normalized)+1)
	for key, value := range normalized {
		withDigest[key] = value
	}
	withDigest["lock_digest"] = digest

	b.Run("normalize_payload", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, _, err := normalizeJSONValueWithBytes(payload); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("digest_canonical_payload", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if digestCanonicalJSON(canonical) == "" {
				b.Fatal("empty digest")
			}
		}
	})
	b.Run("encode_lockfile", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := encodeLockfile(withDigest); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("normalize_expected_actions", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := normalizeJSONValue(payload["actions"]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("compile_serialization_pipeline", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			value, canonical, err := normalizeLockPayloadWithBytes(payload)
			if err != nil {
				b.Fatal(err)
			}
			value["lock_digest"] = digestCanonicalJSON(canonical)
			if _, err := encodeLockfile(value); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchmarkCompilerPayload() map[string]interface{} {
	rules := make([]interface{}, 256)
	tools := make([]interface{}, 256)
	for index := range rules {
		id := "entry-" + strconv.Itoa(index)
		rules[index] = map[string]interface{}{
			"id": id, "kind": "deny_write", "message": "blocked",
			"paths": []string{"src/**", "generated/**"},
		}
		tools[index] = map[string]interface{}{
			"id": id, "transport": "host_mcp", "platform": "codex",
			"tool": "tool_" + strconv.Itoa(index), "effect": map[string]interface{}{"kind": "external"},
		}
	}
	return map[string]interface{}{
		"format_version": LockfileFormatVersion,
		"rules":          rules,
		"actions": map[string]interface{}{
			"format_version": "1", "defaults": map[string]interface{}{}, "tools": tools,
			"rules": []interface{}{}, "budgets": []interface{}{},
		},
	}
}
