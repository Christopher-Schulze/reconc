// Package presets discovers and loads bundled and user preset policy
// packs.
//
// Bundled presets are embedded into the reconc binary at build time; they
// ship with every install. User presets live under
// $RECONC_HOME/presets/ (default ~/.reconc/presets/) and follow the same
// naming convention.
//
// When a bundled preset and a user preset share a name, the user preset
// wins. This lets users override bundled defaults without copying bundled code.
//
// All public functions are deterministic (sorted output) so the
// compiler digest stays stable across runs.
package presets

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/boundedio"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/safename"
)

// HomeEnvVar overrides the location of the reconc home directory.
// Default is ~/.reconc/.
const HomeEnvVar = "RECONC_HOME"

const (
	maxUserPresetBytes   = 8 << 20
	maxUserPresetEntries = 4096
)

// PresetSuffix is the only filename suffix that counts as a preset.
const PresetSuffix = ".yml"

// bundledPacks holds every preset YAML file shipped with the reconc
// binary, embedded at compile time. Keys are filenames, values are
// raw bytes.
//
//go:embed packs/*.yml
var bundledPacks embed.FS

// Source identifies whether a preset comes from the embedded bundle or
// the user's home directory.
type Source string

const (
	SourceBundled Source = "bundled"
	SourceUser    Source = "user"
)

// Metadata describes one preset by name + canonical location.
//
// For bundled presets, Path is a virtual fs path inside the embedded
// filesystem ("packs/<name>.yml"). For user presets, Path is the
// absolute on-disk path. Either way, Load(name) is the canonical way
// to read the content.
type Metadata struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Source   Source    `json:"source"`
	Manifest *Manifest `json:"manifest,omitempty"`
}

// Manifest is the explicit composition contract embedded in a preset file.
// Legacy user presets without a manifest remain loadable, but cannot be
// stack-recommended and contribute no declared capabilities.
type Manifest struct {
	FormatVersion string       `json:"format_version" yaml:"format_version"`
	Name          string       `json:"name" yaml:"name"`
	Summary       string       `json:"summary" yaml:"summary"`
	Stacks        []string     `json:"stacks" yaml:"stacks"`
	Capabilities  []Capability `json:"capabilities" yaml:"capabilities"`
	Conflicts     []string     `json:"conflicts,omitempty" yaml:"conflicts,omitempty"`
}

// Capability binds one semantic promise to its triggering inputs, accepted
// evidence classes, and implementing rule IDs.
type Capability struct {
	ID       string   `json:"id" yaml:"id"`
	Inputs   []string `json:"inputs" yaml:"inputs"`
	Evidence []string `json:"evidence" yaml:"evidence"`
	Rules    []string `json:"rules" yaml:"rules"`
}

// Home returns the reconc home directory. RECONC_HOME wins; falls back
// to $HOME/.reconc.
func Home() string {
	if v := os.Getenv(HomeEnvVar); v != "" {
		return os.ExpandEnv(v)
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".reconc")
	}
	return ".reconc" // last resort - relative
}

// userPresetsDir returns the on-disk directory where user presets live.
func userPresetsDir() string {
	return filepath.Join(Home(), "presets")
}

// List returns every preset (bundled + user) sorted by name. User
// presets override bundled ones when names collide.
func List() ([]Metadata, error) {
	bundled, err := scanBundled()
	if err != nil {
		return nil, &rerrors.PresetError{Message: "scan bundled presets", Cause: err}
	}
	user, err := scanUser()
	if err != nil {
		return nil, &rerrors.PresetError{Message: "scan user presets", Cause: err}
	}

	merged := map[string]Metadata{}
	for _, p := range bundled {
		merged[p.Name] = p
	}
	// User entries written second so they overwrite bundled ones.
	for _, p := range user {
		merged[p.Name] = p
	}

	out := make([]Metadata, 0, len(merged))
	for _, p := range merged {
		content, err := Load(p.Name)
		if err != nil {
			return nil, err
		}
		manifest, err := parseManifest(p.Name, content)
		if err != nil {
			return nil, err
		}
		p.Manifest = manifest
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Inspect returns one preset's metadata plus validated manifest when present.
func Inspect(name string) (Metadata, error) {
	cleaned, err := safename.Normalize("preset", name)
	if err != nil {
		return Metadata{}, &rerrors.PresetError{Message: err.Error()}
	}
	content, err := Load(cleaned)
	if err != nil {
		return Metadata{}, err
	}
	path, source, err := Path(cleaned)
	if err != nil {
		return Metadata{}, err
	}
	manifest, err := parseManifest(cleaned, content)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{Name: cleaned, Path: path, Source: source, Manifest: manifest}, nil
}

// ValidateSelection rejects explicitly conflicting selected packs. Conflict
// lookup is symmetric and diagnostics are sorted, so selection order cannot
// change the result.
func ValidateSelection(names []string) error {
	selected := map[string]*Manifest{}
	for _, name := range names {
		metadata, err := Inspect(name)
		if err != nil {
			return err
		}
		selected[metadata.Name] = metadata.Manifest
	}
	pairs := []string{}
	seen := map[string]bool{}
	for name, manifest := range selected {
		if manifest == nil {
			continue
		}
		for _, conflict := range manifest.Conflicts {
			if _, ok := selected[conflict]; !ok {
				continue
			}
			a, b := name, conflict
			if b < a {
				a, b = b, a
			}
			pair := a + " <-> " + b
			if !seen[pair] {
				seen[pair] = true
				pairs = append(pairs, pair)
			}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Strings(pairs)
	return &rerrors.PresetError{Message: "conflicting preset selection: " + strings.Join(pairs, ", ")}
}

// SuggestForStacks returns manifested packs whose selectors match at least one
// detected stack. Generic "*" packs are intentionally omitted: detection must
// recommend only a specific, evidence-backed fit.
func SuggestForStacks(stacks []string) ([]Metadata, error) {
	stackSet := map[string]bool{}
	for _, stack := range stacks {
		stackSet[strings.ToLower(strings.TrimSpace(stack))] = true
	}
	list, err := List()
	if err != nil {
		return nil, err
	}
	out := []Metadata{}
	for _, metadata := range list {
		if metadata.Manifest == nil {
			continue
		}
		for _, stack := range metadata.Manifest.Stacks {
			selector := strings.ToLower(strings.TrimSpace(stack))
			if selector != "*" && stackSet[selector] {
				out = append(out, metadata)
				break
			}
		}
	}
	return out, nil
}

func parseManifest(name, content string) (*Manifest, error) {
	var document struct {
		Pack  yaml.Node `yaml:"pack"`
		Rules []struct {
			ID string `yaml:"id"`
		} `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return nil, &rerrors.PresetError{Message: "parse preset " + name, Cause: err}
	}
	if document.Pack.Kind == 0 {
		return nil, nil
	}
	data, err := yaml.Marshal(&document.Pack)
	if err != nil {
		return nil, &rerrors.PresetError{Message: "encode manifest for preset " + name, Cause: err}
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, &rerrors.PresetError{Message: "parse manifest for preset " + name, Cause: err}
	}
	ruleIDs := map[string]bool{}
	for _, rule := range document.Rules {
		ruleIDs[strings.TrimSpace(rule.ID)] = true
	}
	if err := validateManifest(name, &manifest, ruleIDs); err != nil {
		return nil, &rerrors.PresetError{Message: "invalid manifest for preset " + name, Cause: err}
	}
	return &manifest, nil
}

func validateManifest(name string, manifest *Manifest, ruleIDs map[string]bool) error {
	if manifest.FormatVersion != "1" {
		return fmt.Errorf("format_version must be 1")
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Summary = strings.TrimSpace(manifest.Summary)
	if manifest.Name != name {
		return fmt.Errorf("name %q must match preset filename %q", manifest.Name, name)
	}
	if strings.TrimSpace(manifest.Summary) == "" || len(manifest.Stacks) == 0 || len(manifest.Capabilities) == 0 {
		return fmt.Errorf("summary, stacks, and capabilities are required")
	}
	for index := range manifest.Stacks {
		manifest.Stacks[index] = strings.ToLower(strings.TrimSpace(manifest.Stacks[index]))
	}
	if err := normalizeManifestList("stacks", &manifest.Stacks); err != nil {
		return err
	}
	seen := map[string]bool{}
	for index := range manifest.Capabilities {
		capability := &manifest.Capabilities[index]
		capability.ID = strings.TrimSpace(capability.ID)
		if capability.ID == "" || seen[capability.ID] {
			return fmt.Errorf("capabilities[%d].id must be unique and non-empty", index)
		}
		seen[capability.ID] = true
		if len(capability.Inputs) == 0 || len(capability.Evidence) == 0 || len(capability.Rules) == 0 {
			return fmt.Errorf("capability %q requires inputs, evidence, and rules", capability.ID)
		}
		for _, field := range []struct {
			name   string
			values *[]string
		}{
			{"inputs", &capability.Inputs},
			{"evidence", &capability.Evidence},
			{"rules", &capability.Rules},
		} {
			if err := normalizeManifestList("capability "+capability.ID+" "+field.name, field.values); err != nil {
				return err
			}
		}
		for _, ruleID := range capability.Rules {
			if !ruleIDs[ruleID] {
				return fmt.Errorf("capability %q references missing rule %q", capability.ID, ruleID)
			}
		}
	}
	if err := normalizeManifestList("conflicts", &manifest.Conflicts); err != nil {
		return err
	}
	for _, conflict := range manifest.Conflicts {
		if conflict == name {
			return fmt.Errorf("conflicts must contain non-empty other preset names")
		}
	}
	return nil
}

func normalizeManifestList(name string, values *[]string) error {
	seen := map[string]bool{}
	for index := range *values {
		value := strings.TrimSpace((*values)[index])
		if value == "" {
			return fmt.Errorf("%s cannot contain an empty value", name)
		}
		if seen[value] {
			return fmt.Errorf("%s cannot contain duplicate value %q", name, value)
		}
		seen[value] = true
		(*values)[index] = value
	}
	return nil
}

// Load returns the raw YAML content of the named preset, preferring
// user over bundled.
//
// Returns *PresetNotFoundError when the name does not resolve.
func Load(name string) (string, error) {
	cleaned, err := safename.Normalize("preset", name)
	if err != nil {
		return "", &rerrors.PresetError{Message: err.Error()}
	}

	// User wins over bundled.
	userPath := filepath.Join(userPresetsDir(), cleaned+PresetSuffix)
	if data, err := readRegularFile(userPath); err == nil {
		return string(data), nil
	} else if !os.IsNotExist(err) {
		return "", &rerrors.PresetError{Message: "read user preset " + cleaned, Cause: err}
	}

	bundledPath := "packs/" + cleaned + PresetSuffix
	if data, err := bundledPacks.ReadFile(bundledPath); err == nil {
		return string(data), nil
	}

	return "", &rerrors.PresetNotFoundError{Name: cleaned}
}

// Path returns the canonical location of the named preset (either an
// absolute on-disk path for user presets, or the virtual embedded path
// for bundled). Useful for diagnostics and provenance reporting.
func Path(name string) (string, Source, error) {
	cleaned, err := safename.Normalize("preset", name)
	if err != nil {
		return "", "", &rerrors.PresetError{Message: err.Error()}
	}

	userPath := filepath.Join(userPresetsDir(), cleaned+PresetSuffix)
	if info, statErr := os.Lstat(userPath); statErr == nil {
		if !info.Mode().IsRegular() {
			return "", "", &rerrors.PresetError{Message: "user preset " + cleaned + " must be a regular file and not a symlink"}
		}
		return userPath, SourceUser, nil
	} else if !os.IsNotExist(statErr) {
		return "", "", &rerrors.PresetError{Message: "inspect user preset " + cleaned, Cause: statErr}
	}

	bundledPath := "packs/" + cleaned + PresetSuffix
	if _, err := bundledPacks.ReadFile(bundledPath); err == nil {
		return bundledPath, SourceBundled, nil
	}

	return "", "", &rerrors.PresetNotFoundError{Name: cleaned}
}

// scanBundled walks the embedded packs directory.
func scanBundled() ([]Metadata, error) {
	out := []Metadata{}
	err := fs.WalkDir(bundledPacks, "packs", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, PresetSuffix) {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), PresetSuffix)
		out = append(out, Metadata{Name: name, Path: path, Source: SourceBundled})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanUser walks the user presets directory if it exists. Missing
// directory is not an error - returns empty slice.
func scanUser() ([]Metadata, error) {
	dir := userPresetsDir()
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	out := []Metadata{}
	entries, err := boundedio.ReadDirNoSymlink(dir, maxUserPresetEntries)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), PresetSuffix) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		name := strings.TrimSuffix(e.Name(), PresetSuffix)
		if _, err := safename.Normalize("preset", name); err != nil {
			return nil, err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("user preset %s must be a regular file and not a symlink", name)
		}
		out = append(out, Metadata{Name: name, Path: full, Source: SourceUser})
	}
	return out, nil
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file and not a symlink", path)
	}
	return boundedio.ReadRegularFile(path, maxUserPresetBytes)
}
