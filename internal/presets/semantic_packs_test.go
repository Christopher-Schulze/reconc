package presets_test

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/presets"
	policyruntime "reconc.dev/reconc/internal/runtime"
)

type packTrigger struct {
	Path       string
	Body       string
	Additional map[string]string
}

func TestEveryBundledPackCompilesAndEvaluatesPassAndViolationFixtures(t *testing.T) {
	triggers := map[string]packTrigger{
		"agent":                {Path: "src/main.go", Body: "package main\n"},
		"default":              {Path: "generated/output.json", Body: "{}\n"},
		"docs-sync":            {Path: "internal/cli/command.go", Body: "package cli\n"},
		"release":              {Path: "Makefile", Body: "test:\n\ttrue\n"},
		"strict":               {Path: "src/main.go", Body: "package main\n"},
		"bun-assurance":        {Path: "bun.lock", Body: "lockfileVersion = 1\n", Additional: map[string]string{"package.json": `{"dependencies":{"example":"^1.0.0"}}`}},
		"npm-assurance":        {Path: "app/package.json", Body: `{"dependencies":{"example":"^1.0.0"}}`, Additional: map[string]string{"app/package-lock.json": `{}`}},
		"pnpm-assurance":       {Path: "app/package.json", Body: `{"dependencies":{"example":"^1.0.0"}}`, Additional: map[string]string{"app/pnpm-lock.yaml": "lockfileVersion: '9.0'\n"}},
		"yarn-assurance":       {Path: "app/package.json", Body: `{"dependencies":{"example":"^1.0.0"}}`, Additional: map[string]string{"app/yarn.lock": "# yarn lockfile v1\n"}},
		"typescript-assurance": {Path: "src/main.ts", Body: `throw new Error("not implemented")`, Additional: map[string]string{"tsconfig.json": `{}`}},
		"nextjs-assurance":     {Path: "package.json", Body: `{"scripts":{}}`},
		"svelte-assurance":     {Path: "package.json", Body: `{"scripts":{}}`},
		"go-assurance":         {Path: "go.mod", Body: "module example.test/fixture\n\ngo 1.26\n"},
		"rust-assurance":       {Path: "Cargo.toml", Body: "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n"},
		"python-assurance":     {Path: "pyproject.toml", Body: "[project]\nname = \"fixture\"\n"},
		"java-assurance":       {Path: "src/Main.java", Body: `throw new UnsupportedOperationException("not implemented");`},
		"csharp-assurance":     {Path: "src/Main.cs", Body: "throw new NotImplementedException();\n"},
		"cpp-assurance":        {Path: "src/main.cpp", Body: "#error not implemented\n"},
		"php-assurance":        {Path: "src/main.php", Body: "<?php\n// TODO: implement\n"},
		"elixir-assurance":     {Path: "mix.exs", Body: "defmodule Fixture.MixProject do\nend\n"},
		"zig-assurance":        {Path: "build.zig", Body: "const std = @import(\"std\");\n"},
		"shell-assurance":      {Path: "scripts/check.sh", Body: "# TODO: implement\n"},
		"powershell-assurance": {Path: "scripts/check.ps1", Body: "throw [System.NotImplementedException]::new()\n"},
	}

	t.Setenv(presets.HomeEnvVar, t.TempDir())
	metadata, err := presets.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, pack := range metadata {
		if pack.Source != presets.SourceBundled {
			continue
		}
		trigger, ok := triggers[pack.Name]
		if !ok {
			t.Errorf("bundled pack %s has no semantic fixture", pack.Name)
			continue
		}
		t.Run(pack.Name, func(t *testing.T) {
			repo := t.TempDir()
			writePackFixture(t, repo, "AGENTS.md", "# Fixture\n")
			writePackFixture(t, repo, ".reconc.yml", "extends:\n  - "+pack.Name+"\n")
			writePackFixture(t, repo, trigger.Path, trigger.Body)
			for relative, body := range trigger.Additional {
				writePackFixture(t, repo, relative, body)
			}
			if _, err := compiler.CompileRepoPolicy(repo, "pack-test"); err != nil {
				t.Fatalf("compile bundled pack: %v", err)
			}
			pass, err := policyruntime.CheckRepoPolicy(repo, policyruntime.Empty())
			if err != nil {
				t.Fatalf("evaluate no-change fixture: %v", err)
			}
			if len(pass.Violations) != 0 {
				t.Fatalf("no-change fixture produced violations: %+v", pass.Violations)
			}
			violation, err := policyruntime.CheckRepoPolicy(repo, policyruntime.ExecutionInputs{WritePaths: []string{trigger.Path}})
			if err != nil {
				t.Fatalf("evaluate trigger fixture: %v", err)
			}
			if len(violation.Violations) == 0 {
				t.Fatal("trigger fixture did not exercise any declared pack rule")
			}
		})
	}
	if len(metadata) != len(triggers) {
		t.Fatalf("fixture count=%d, listed pack count=%d", len(triggers), len(metadata))
	}
}

func writePackFixture(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
