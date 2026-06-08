package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const stackConfigRel = "tools/reconc/harness/template/config/workflow/stack-config.yaml"

type stackConfig struct {
	Stack                  string                     `yaml:"stack"`
	Project                string                     `yaml:"project"`
	Layout                 string                     `yaml:"layout"`
	Build                  buildStackConfig           `yaml:"build"`
	DurableStore           durableStoreStackConfig    `yaml:"durable_store"`
	GeneratedReferences    generatedReferenceConfig   `yaml:"generated_references"`
	ArchitectureBoundaries architectureBoundaryConfig `yaml:"architecture_boundaries"`
	AgentHooks             agentHooksConfig           `yaml:"agent_hooks"`
}

type buildStackConfig struct {
	Enabled                 bool     `yaml:"enabled"`
	Language                string   `yaml:"language"`
	RequireGoMod            bool     `yaml:"require_go_mod"`
	RequireCargoToml        bool     `yaml:"require_cargo_toml"`
	RequireBuildRunner      bool     `yaml:"require_build_runner"`
	RequireBuildRunnerTest  bool     `yaml:"require_build_runner_test"`
	RequireFrontendPackage  bool     `yaml:"require_frontend_package"`
	BackendEntrypoints      []string `yaml:"backend_entrypoints"`
	GoModTokens             []string `yaml:"go_mod_tokens"`
	CargoTomlTokens         []string `yaml:"cargo_toml_tokens"`
	FrontendPackageTokens   []string `yaml:"frontend_package_tokens"`
	ForbiddenFrontendTokens []string `yaml:"forbidden_frontend_tokens"`
	BuildRunnerTokens       []string `yaml:"build_runner_tokens"`
}

type durableStoreStackConfig struct {
	Enabled          bool     `yaml:"enabled"`
	StoreFiles       []string `yaml:"store_files"`
	StoreGoTokens    []string `yaml:"store_go_tokens"`
	InitialSQL       string   `yaml:"initial_sql"`
	InitialSQLTokens []string `yaml:"initial_sql_tokens"`
	MigrationGoFiles []string `yaml:"migration_go_files"`
}

type generatedReferenceConfig struct {
	Enabled bool `yaml:"enabled"`
}

type architectureBoundaryConfig struct {
	Required bool `yaml:"required"`
}

type agentHooksConfig struct {
	RequireCodexConfig      bool `yaml:"require_codex_config"`
	RequireCodexHookFile    bool `yaml:"require_codex_hook_file"`
	RequireCursorHooks      bool `yaml:"require_cursor_hooks"`
	RequireClaudeSettings   bool `yaml:"require_claude_settings"`
	RequireOpenCodePlugin   bool `yaml:"require_opencode_plugin"`
	RequireAntigravityHooks bool `yaml:"require_antigravity_hooks"`
}

func stackConfigPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(stackConfigRel))
}

func loadStackConfig(root string) (stackConfig, []string) {
	cfg := defaultStackConfig()
	path := stackConfigPath(root)
	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, []string{fmt.Sprintf("%s missing or unreadable: %v", stackConfigRel, err)}
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return cfg, []string{fmt.Sprintf("%s invalid YAML: %v", stackConfigRel, err)}
	}
	normalizeStackConfig(&cfg)
	return cfg, nil
}

func defaultStackConfig() stackConfig {
	cfg := stackConfig{
		Stack:   "go-cli",
		Project: "project",
		Layout:  "auto",
		Build: buildStackConfig{
			Enabled:                true,
			Language:               "go",
			RequireGoMod:           true,
			RequireBuildRunner:     true,
			RequireBuildRunnerTest: true,
			BackendEntrypoints:     []string{"project"},
			GoModTokens:            []string{"module ", "go "},
			BuildRunnerTokens:      []string{`case "build":`, `case "test":`, `case "lint":`, `case "validate":`, `case "clean":`},
		},
		DurableStore: durableStoreStackConfig{
			Enabled: false,
		},
		GeneratedReferences: generatedReferenceConfig{
			Enabled: false,
		},
		ArchitectureBoundaries: architectureBoundaryConfig{
			Required: false,
		},
		AgentHooks: agentHooksConfig{
			RequireCodexConfig:      true,
			RequireCodexHookFile:    true,
			RequireCursorHooks:      true,
			RequireClaudeSettings:   true,
			RequireOpenCodePlugin:   true,
			RequireAntigravityHooks: true,
		},
	}
	return cfg
}

func stackProjectRel(root string, cfg stackConfig, relPath string) string {
	return projectRel(root, stackRootRel(cfg, relPath))
}

func stackRootRel(cfg stackConfig, relPath string) string {
	relPath = strings.ReplaceAll(relPath, "{project}", cfg.Project)
	return strings.ReplaceAll(relPath, "project", cfg.Project)
}

func normalizeStackConfig(cfg *stackConfig) {
	if cfg.Stack == "" {
		cfg.Stack = "go-cli"
	}
	if cfg.Project == "" {
		cfg.Project = "project"
	}
	if cfg.Layout == "" {
		cfg.Layout = "auto"
	}
	if cfg.Build.Language == "" {
		cfg.Build.Language = "go"
	}
	if cfg.Build.Enabled && len(cfg.Build.BackendEntrypoints) == 0 && cfg.Build.Language == "go" {
		cfg.Build.BackendEntrypoints = []string{cfg.Project}
	}
	if cfg.Build.Enabled && cfg.Build.RequireGoMod && len(cfg.Build.GoModTokens) == 0 {
		cfg.Build.GoModTokens = []string{"module ", "go "}
	}
	if cfg.Build.Enabled && cfg.Build.RequireCargoToml && len(cfg.Build.CargoTomlTokens) == 0 {
		cfg.Build.CargoTomlTokens = []string{"[package]", "name ="}
	}
	if cfg.Build.Enabled && cfg.Build.RequireFrontendPackage && len(cfg.Build.FrontendPackageTokens) == 0 {
		cfg.Build.FrontendPackageTokens = []string{`"private": true`, `"packageManager": "bun@`, `"build"`, `"test"`, `"typecheck"`}
	}
	if cfg.Build.Enabled && cfg.Build.RequireFrontendPackage && len(cfg.Build.ForbiddenFrontendTokens) == 0 {
		cfg.Build.ForbiddenFrontendTokens = []string{`"packageManager": "npm@`, `"packageManager": "yarn@`, `"packageManager": "pnpm@`}
	}
	if cfg.Build.Enabled && cfg.Build.RequireBuildRunner && len(cfg.Build.BuildRunnerTokens) == 0 {
		cfg.Build.BuildRunnerTokens = []string{`case "build":`, `case "test":`, `case "lint":`, `case "validate":`, `case "clean":`}
	}
	if cfg.DurableStore.Enabled && len(cfg.DurableStore.StoreFiles) == 0 {
		cfg.DurableStore.StoreFiles = []string{
			"backend/{project}/internal/store/store.go",
			"backend/{project}/internal/store/hash.go",
			"backend/{project}/internal/store/store_test.go",
		}
	}
	if cfg.DurableStore.Enabled && len(cfg.DurableStore.MigrationGoFiles) == 0 {
		cfg.DurableStore.MigrationGoFiles = []string{
			"db/migrations/migrations.go",
			"db/migrations/migrations_test.go",
		}
	}
	if cfg.DurableStore.Enabled && cfg.DurableStore.InitialSQL == "" {
		cfg.DurableStore.InitialSQL = "db/migrations/{project}/core/001_initial.sql"
	}
	if cfg.DurableStore.Enabled && len(cfg.DurableStore.StoreGoTokens) == 0 {
		cfg.DurableStore.StoreGoTokens = []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA auto_vacuum=INCREMENTAL",
			"migration_run_ledger",
			"SnapshotCore",
			"IntegrityCheck",
		}
	}
}
