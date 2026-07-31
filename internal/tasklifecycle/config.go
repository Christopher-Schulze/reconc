// Package tasklifecycle owns repository TASK state independently of any
// project-specific audit harness.
package tasklifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/pathidentity"
)

// Profile selects one of the two TASK formats Reconc can adopt without
// rewriting the repository.
type Profile string

const (
	ProfileAuto     Profile = "auto"
	ProfileSections Profile = "sections-v1"
	ProfileLogbook  Profile = "logbook-v1"
)

const (
	defaultOverviewPath = "docs/tasks.md"
	defaultDetailDir    = "docs/tasks"
	defaultDoneDir      = "docs/tasks/done"
	defaultDoneVisible  = 10
	maxConfiguredFields = 32
	maxConfiguredName   = 120
)

// CompletionConfig adds repository-specific evidence fields without changing
// either built-in TASK grammar.
type CompletionConfig struct {
	RequiredSections       []string `json:"required_sections,omitempty"`
	RequiredEvidenceFields []string `json:"required_evidence_fields,omitempty"`
	RequireCommitted       bool     `json:"require_committed,omitempty"`
}

// Config is the resolved task_lifecycle section from .reconc.yml.
type Config struct {
	Configured   bool             `json:"configured"`
	Enabled      bool             `json:"enabled"`
	Profile      Profile          `json:"profile"`
	OverviewPath string           `json:"overview_path"`
	DetailDir    string           `json:"detail_dir"`
	DoneDir      string           `json:"done_dir"`
	DoneVisible  int              `json:"done_visible"`
	Completion   CompletionConfig `json:"completion"`
}

type fileConfig struct {
	TaskLifecycle *rawConfig `yaml:"task_lifecycle"`
}

type rawConfig struct {
	Enabled      *bool         `yaml:"enabled"`
	Profile      Profile       `yaml:"profile"`
	OverviewPath string        `yaml:"overview_path"`
	DetailDir    string        `yaml:"detail_dir"`
	DoneDir      string        `yaml:"done_dir"`
	DoneVisible  int           `yaml:"done_visible"`
	Completion   rawCompletion `yaml:"completion"`
}

type rawCompletion struct {
	RequiredSections       []string `yaml:"required_sections"`
	RequiredEvidenceFields []string `yaml:"required_evidence_fields"`
	RequireCommitted       bool     `yaml:"require_committed"`
}

func (raw *rawConfig) UnmarshalYAML(node *yaml.Node) error {
	if err := validateTaskLifecycleNode(node); err != nil {
		return err
	}
	type plainRawConfig rawConfig
	return node.Decode((*plainRawConfig)(raw))
}

// LoadConfig reads only task_lifecycle from .reconc.yml. Unknown policy keys
// remain owned by the policy parser and are deliberately ignored here.
func LoadConfig(repoRoot string) (Config, error) {
	cfg := defaultConfig()
	configPath := filepath.Join(repoRoot, ".reconc.yml")
	if err := rejectSymlinkComponents(repoRoot, configPath); err != nil {
		return Config{}, fmt.Errorf("unsafe .reconc.yml: %w", err)
	}
	body, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read .reconc.yml: %w", err)
	}
	var file fileConfig
	if err := yaml.Unmarshal(body, &file); err != nil {
		return Config{}, fmt.Errorf("parse .reconc.yml task_lifecycle: %w", err)
	}
	if file.TaskLifecycle == nil {
		return cfg, nil
	}
	cfg.Configured = true
	cfg = mergeRawConfig(cfg, file.TaskLifecycle)
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateTaskLifecycleNode(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("must be a mapping")
	}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		switch key {
		case "enabled", "profile", "overview_path", "detail_dir", "done_dir", "done_visible":
		case "completion":
			if err := validateTaskCompletionNode(node.Content[index+1]); err != nil {
				return err
			}
		default:
			return fmt.Errorf("field %s not found in task_lifecycle", key)
		}
	}
	return nil
}

func validateTaskCompletionNode(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("task_lifecycle.completion must be a mapping")
	}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		switch key {
		case "required_sections", "required_evidence_fields", "require_committed":
		default:
			return fmt.Errorf("field %s not found in task_lifecycle.completion", key)
		}
	}
	return nil
}

func mergeRawConfig(cfg Config, raw *rawConfig) Config {
	if raw.Enabled != nil {
		cfg.Enabled = *raw.Enabled
	}
	if raw.Profile != "" {
		cfg.Profile = raw.Profile
	}
	if raw.OverviewPath != "" {
		cfg.OverviewPath = filepath.ToSlash(raw.OverviewPath)
	}
	if raw.DetailDir != "" {
		cfg.DetailDir = filepath.ToSlash(raw.DetailDir)
	}
	if raw.DoneDir != "" {
		cfg.DoneDir = filepath.ToSlash(raw.DoneDir)
	}
	if raw.DoneVisible != 0 {
		cfg.DoneVisible = raw.DoneVisible
	}
	cfg.Completion.RequiredSections = cleanUnique(raw.Completion.RequiredSections)
	cfg.Completion.RequiredEvidenceFields = cleanUnique(raw.Completion.RequiredEvidenceFields)
	cfg.Completion.RequireCommitted = raw.Completion.RequireCommitted
	return cfg
}

func defaultConfig() Config {
	return Config{
		Enabled:      true,
		Profile:      ProfileAuto,
		OverviewPath: defaultOverviewPath,
		DetailDir:    defaultDetailDir,
		DoneDir:      defaultDoneDir,
		DoneVisible:  defaultDoneVisible,
	}
}

func (cfg Config) validate() error {
	switch cfg.Profile {
	case ProfileAuto, ProfileSections, ProfileLogbook:
	default:
		return fmt.Errorf("task_lifecycle.profile must be auto, sections-v1, or logbook-v1 (got %q)", cfg.Profile)
	}
	paths := []struct {
		label string
		path  string
	}{
		{label: "overview_path", path: cfg.OverviewPath},
		{label: "detail_dir", path: cfg.DetailDir},
		{label: "done_dir", path: cfg.DoneDir},
	}
	for _, item := range paths {
		if err := validateRepoRelativePath(item.path); err != nil {
			return fmt.Errorf("task_lifecycle.%s: %w", item.label, err)
		}
	}
	if cfg.DoneVisible < 1 || cfg.DoneVisible > 1000 {
		return fmt.Errorf("task_lifecycle.done_visible must be between 1 and 1000")
	}
	if err := validateConfiguredNames("completion.required_sections", cfg.Completion.RequiredSections); err != nil {
		return err
	}
	if err := validateConfiguredNames("completion.required_evidence_fields", cfg.Completion.RequiredEvidenceFields); err != nil {
		return err
	}
	return nil
}

func validateConfiguredNames(label string, values []string) error {
	if len(values) > maxConfiguredFields {
		return fmt.Errorf("task_lifecycle.%s must contain at most %d entries", label, maxConfiguredFields)
	}
	for _, value := range values {
		if len([]rune(value)) > maxConfiguredName || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("task_lifecycle.%s entry %q must be one line and at most %d characters", label, value, maxConfiguredName)
		}
	}
	return nil
}

func validateRepoRelativePath(path string) error {
	path = filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must be a non-empty repository-relative path")
	}
	return nil
}

func canonicalRepoRoot(repoRoot string) (string, error) {
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root filesystem identity: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("task repository root is not a directory: %s", root)
	}
	return root, nil
}

func rejectSymlinkComponents(repoRoot, abs string) error {
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes the repository")
	}
	current := repoRoot
	components := strings.Split(rel, string(filepath.Separator))
	for index, component := range components {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect path component %s: %w", current, statErr)
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("path uses symlink component %s", current)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("path component %s is not a directory", current)
		}
	}
	return nil
}

func cleanUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
