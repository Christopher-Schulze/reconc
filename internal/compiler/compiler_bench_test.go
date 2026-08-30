package compiler

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strconv"
	"strings"
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
	canonical, formattedSize, err := canonicalLockPayloadForEncoding(payload)
	if err != nil {
		b.Fatal(err)
	}
	digest := digestCanonicalJSON(canonical)

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
			if _, err := encodeCanonicalLockfileWithSize(canonical, digest, formattedSize); err != nil {
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
			canonical, formattedSize, err := canonicalLockPayloadForEncoding(payload)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := encodeCanonicalLockfileWithSize(canonical, digestCanonicalJSON(canonical), formattedSize); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCompilerSerializationMaximumSize(b *testing.B) {
	payload := benchmarkMaximumCompilerPayload()
	canonical, formattedSize, err := canonicalLockPayloadForEncoding(payload)
	if err != nil {
		b.Fatal(err)
	}
	want, err := encodeCanonicalLockfileWithSize(canonical, digestCanonicalJSON(canonical), formattedSize)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("legacy_multi_pass", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(want)))
		b.ReportMetric(float64(len(want)), "lock-bytes")
		runtime.GC()
		runtime.GC()
		b.ResetTimer()
		for range b.N {
			normalized, err := normalizeLockPayload(payload)
			if err != nil {
				b.Fatal(err)
			}
			canonical, err := encodeCanonicalJSON(normalized)
			if err != nil {
				b.Fatal(err)
			}
			normalized["lock_digest"] = digestCanonicalJSON(canonical)
			body, err := encodeLockfile(normalized)
			if err != nil {
				b.Fatal(err)
			}
			if !bytes.Equal(body, want) {
				b.Fatal("legacy serialization drifted from canonical reuse")
			}
		}
	})

	b.Run("canonical_reuse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(want)))
		b.ReportMetric(float64(len(want)), "lock-bytes")
		runtime.GC()
		runtime.GC()
		b.ResetTimer()
		for range b.N {
			canonical, formattedSize, err := canonicalLockPayloadForEncoding(payload)
			if err != nil {
				b.Fatal(err)
			}
			body, err := encodeCanonicalLockfileWithSize(canonical, digestCanonicalJSON(canonical), formattedSize)
			if err != nil {
				b.Fatal(err)
			}
			if !bytes.Equal(body, want) {
				b.Fatal("canonical serialization is not byte-stable")
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

func benchmarkMaximumCompilerPayload() map[string]interface{} {
	const (
		ruleCount    = 240
		messageBytes = 63 << 10
	)
	rules := make([]interface{}, ruleCount)
	message := strings.Repeat("m", messageBytes)
	for index := range rules {
		rules[index] = map[string]interface{}{
			"id": "maximum-entry-" + strconv.Itoa(index), "kind": "deny_write",
			"message": message, "paths": []string{"generated/**"},
		}
	}
	return map[string]interface{}{
		"format_version": LockfileFormatVersion,
		"rules":          rules,
		"actions": map[string]interface{}{
			"format_version": "1", "defaults": map[string]interface{}{},
			"tools": []interface{}{}, "rules": []interface{}{}, "budgets": []interface{}{},
		},
	}
}
