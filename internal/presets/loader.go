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
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

// HomeEnvVar overrides the location of the reconc home directory.
// Default is ~/.reconc/.
const HomeEnvVar = "RECONC_HOME"

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
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source Source `json:"source"`
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
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load returns the raw YAML content of the named preset, preferring
// user over bundled.
//
// Returns *PresetNotFoundError when the name does not resolve.
func Load(name string) (string, error) {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "", &rerrors.PresetError{Message: "preset name must be a non-empty string"}
	}

	// User wins over bundled.
	userPath := filepath.Join(userPresetsDir(), cleaned+PresetSuffix)
	if data, err := os.ReadFile(userPath); err == nil {
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
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "", "", &rerrors.PresetError{Message: "preset name must be a non-empty string"}
	}

	userPath := filepath.Join(userPresetsDir(), cleaned+PresetSuffix)
	if info, err := os.Stat(userPath); err == nil && info.Mode().IsRegular() {
		return userPath, SourceUser, nil
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
	entries, err := os.ReadDir(dir)
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
		out = append(out, Metadata{Name: name, Path: full, Source: SourceUser})
	}
	return out, nil
}
