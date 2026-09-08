package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"reconc.dev/reconc/internal/safename"
)

// Dependency identifies the exact selected template without publishing its
// private filesystem path or raw contents.
type Dependency struct {
	Name          string `json:"name"`
	Source        Source `json:"source"`
	ContentSHA256 string `json:"content_sha256"`
}

// Dependency returns provenance from the same bytes that populated Body.
func (t *Template) Dependency() Dependency {
	return Dependency{Name: t.Name, Source: t.Source, ContentSHA256: hex.EncodeToString(t.contentDigest[:])}
}

// ContentBytes is the raw input size retained by this resolution snapshot.
func (t *Template) ContentBytes() int { return t.contentBytes }

// ValidateDependencies admits only bounded, canonical, sorted provenance.
func ValidateDependencies(dependencies []Dependency) error {
	if len(dependencies) > maxUserTemplateEntries {
		return fmt.Errorf("template dependencies exceed %d entries", maxUserTemplateEntries)
	}
	previous := ""
	for _, dependency := range dependencies {
		name, err := safename.Normalize("template", dependency.Name)
		if err != nil || name != dependency.Name || name <= previous {
			return fmt.Errorf("template dependencies must have canonical, sorted, unique names")
		}
		if dependency.Source != SourceBuiltin && dependency.Source != SourceUser {
			return fmt.Errorf("template %q has an invalid source", name)
		}
		digest, err := hex.DecodeString(dependency.ContentSHA256)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != dependency.ContentSHA256 {
			return fmt.Errorf("template %q has an invalid content digest", name)
		}
		previous = name
	}
	return nil
}

// ValidateCurrentDependencies checks the current override selection and exact
// contents. A missing override must not silently select different built-ins.
func ValidateCurrentDependencies(dependencies []Dependency) error {
	if err := ValidateDependencies(dependencies); err != nil {
		return err
	}
	for _, dependency := range dependencies {
		current, err := Resolve(dependency.Name)
		if err != nil {
			return fmt.Errorf("resolve template dependency %q: %w", dependency.Name, err)
		}
		if current.Dependency() != dependency {
			return fmt.Errorf("template %q changed; refresh required", dependency.Name)
		}
	}
	return nil
}
