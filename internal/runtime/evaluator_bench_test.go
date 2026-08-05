package runtime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
)

// writeFileBench is the *testing.B-flavoured sibling of writeFile in
// evaluator_test.go. Inlined so the bench file has no helper-bridge
// indirection.
func writeFileBench(b *testing.B, dir, name, content string) {
	b.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		b.Fatalf("write: %v", err)
	}
}

// BenchmarkCheckSingleDenyWrite measures the hot-path cost of a
// single-rule evaluation: one deny_write rule, one --write input,
// no other evidence. Representative of the `reconc can` / `reconc
// check --terse` fast-path.
func BenchmarkCheckSingleDenyWrite(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml",
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatalf("compile: %v", err)
	}

	inputs := ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "gen/x.go")},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := CheckRepoPolicy(repo, inputs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadLockfile(b *testing.B) {
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml", "rules: []\n")
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatalf("compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := loadLockfile(repo); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateCurrentLockfileFreshness(b *testing.B) {
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml", "rules: []\n")
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatalf("compile: %v", err)
	}
	lock, err := readLockfile(repo)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := validateLockfileFreshness(repo, lock.payload, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimePipelineStages(b *testing.B) {
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml", "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatal(err)
	}
	lockData, err := readLockfileBytes(repo)
	if err != nil {
		b.Fatal(err)
	}
	lock, err := decodeLockfile(lockData)
	if err != nil {
		b.Fatal(err)
	}
	bundle, err := ingest.LoadPolicySources(repo)
	if err != nil {
		b.Fatal(err)
	}
	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		b.Fatal(err)
	}
	defaultMode, err := lockfileDefaultMode(lock.payload)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("lock-decode", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := decodeLockfile(lockData); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("source-load", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := ingest.LoadPolicySources(repo); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("source-parse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := parser.ParseRuleDocuments(bundle); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("embedded-parity", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := compiler.ValidateEmbeddedRules(lock.payload, parsed); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("typed-plan", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := compileRuntimePlan(lock.payload); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("path-match", func(b *testing.B) {
		paths := []string{"gen/a.go", "src/main.go", "docs/readme.md"}
		patterns := []string{"gen/**", "generated/**", "vendor/**"}
		b.ReportAllocs()
		for range b.N {
			if _, err := matchingPaths(paths, patterns); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("report", func(b *testing.B) {
		inputs := ExecutionInputs{WritePaths: []string{"gen/a.go"}}
		b.ReportAllocs()
		for range b.N {
			report := NewEmptyReport(repo, ingest.LockfilePath, defaultMode, inputs)
			report.Violations = append(report.Violations, Violation{RuleID: "r1", Kind: "deny_write", Mode: "block", Message: "m"})
			report.Finalize()
		}
	})
}

func BenchmarkEvaluatorWorkerReuse(b *testing.B) {
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml", "rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: m\n")
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatalf("compile: %v", err)
	}
	evaluator := NewEvaluator()
	inputs := ExecutionInputs{WritePaths: []string{filepath.Join(repo, "src/main.go")}}
	if _, err := evaluator.CheckRepoPolicy(repo, inputs); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := evaluator.CheckRepoPolicy(repo, inputs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCheckLargeRuleset measures scaling: 100 deny_write rules
// against 10 write paths. Any accidental N*N regression in the
// evaluator jumps out in CI.
func BenchmarkCheckLargeRuleset(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	rulesYaml := "rules:\n"
	for i := 0; i < 100; i++ {
		id := itoaBench(i)
		rulesYaml += "  - id: r" + id + "\n    kind: deny_write\n    paths: ['dir" + id + "/**']\n    mode: warn\n    message: m\n"
	}
	writeFileBench(b, repo, "policies/rules.yml", rulesYaml)
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatalf("compile: %v", err)
	}

	writes := []string{}
	for i := 0; i < 10; i++ {
		writes = append(writes, filepath.Join(repo, "src/a"+itoaBench(i)+".go"))
	}
	inputs := ExecutionInputs{WritePaths: writes}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := CheckRepoPolicy(repo, inputs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckMixedRuleset(b *testing.B) {
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml", `default_mode: warn
rules:
  - id: deny
    kind: deny_write
    paths: ['generated/**']
    message: deny
  - id: read
    kind: require_read
    paths: ['src/**']
    before_paths: ['docs/**']
    message: read
  - id: command
    kind: require_command_success
    when_paths: ['src/**']
    commands: ['go test ./...']
    message: command
  - id: claim
    kind: require_claim
    when_paths: ['src/**']
    claims: ['reviewed']
    message: claim
  - id: composite
    kind: all_of
    when_paths: ['src/**']
    checks:
      - kind: require_claim
        claims: ['reviewed']
      - kind: forbid_command
        commands: ['rm -rf']
    message: composite
`)
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatal(err)
	}
	evaluator := NewEvaluator()
	inputs := ExecutionInputs{ReadPaths: []string{"docs/spec.md"}, WritePaths: []string{"src/main.go"}, Claims: []string{"reviewed"}, CommandResults: []CommandResult{{Command: "go test ./...", Outcome: CommandOutcomeSuccess}}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := evaluator.CheckRepoPolicy(repo, inputs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckPreCommandSubset(b *testing.B) {
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	rules := "rules:\n"
	for index := 0; index < 100; index++ {
		rules += "  - id: deny-" + itoaBench(index) + "\n    kind: deny_write\n    paths: ['generated/" + itoaBench(index) + "/**']\n    mode: block\n    message: deny\n"
	}
	rules += "  - id: shell\n    kind: forbid_command\n    commands: ['rm -rf']\n    mode: block\n    message: shell\n"
	writeFileBench(b, repo, "policies/rules.yml", rules)
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatal(err)
	}
	evaluator := NewEvaluator()
	inputs := ExecutionInputs{Commands: []string{"go test ./..."}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := evaluator.CheckRepoPolicyForPreCommand(repo, inputs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLegacyMigrationPlan(b *testing.B) {
	policyText := "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: warn\n    message: x\n"
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# project\n")
	writeFileBench(b, repo, "policies/rules.yml", policyText)
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatal(err)
	}
	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		b.Fatal(err)
	}
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		b.Fatal(err)
	}
	payload["$schema"] = compiler.LegacyLockfileSchemaV1
	payload["format_version"] = "1"
	payload["repo_root"] = repo
	payload["sources"] = []interface{}{
		map[string]interface{}{"kind": "agents_md", "path": "AGENTS.md", "content": "# project\n"},
		map[string]interface{}{"kind": "policy_file", "path": "policies/rules.yml", "content": policyText},
	}
	discovery := payload["discovery"].(map[string]interface{})
	discovery["repo_root"] = repo
	discovery["start_path"] = repo
	legacy, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(legacy, '\n'), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := NewEvaluator().CheckRepoPolicy(repo, Empty()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCheckScopedRule measures scope-filter short-circuit cost
// for a rule that doesn't apply. Dominant cold-path in monorepos.
func BenchmarkCheckScopedRule(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml", `default_mode: warn
scopes:
  - id: web
    paths: ['apps/web/**']
    rules:
      - id: web-gen
        kind: deny_write
        paths: ['apps/web/generated/**']
        mode: block
        message: m
`)
	if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
		b.Fatalf("compile: %v", err)
	}

	inputs := ExecutionInputs{
		WritePaths: []string{filepath.Join(repo, "libs/shared/x.ts")},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := CheckRepoPolicy(repo, inputs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompile measures the full ingest+parse+digest+write path.
// The compile-lock cost is included so regressions in lock acquisition
// show up here too.
func BenchmarkCompile(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	repo := b.TempDir()
	writeFileBench(b, repo, "AGENTS.md", "# t\n")
	writeFileBench(b, repo, "policies/rules.yml",
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := compiler.CompileRepoPolicy(repo, "bench"); err != nil {
			b.Fatal(err)
		}
	}
}

func itoaBench(n int) string {
	if n == 0 {
		return "0"
	}
	d := []byte{}
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
