package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
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
