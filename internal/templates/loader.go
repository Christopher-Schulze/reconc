// Package templates provides named rule-shape templates that expand
// into full rule definitions at parse time (W18).
//
// A template is a partial rule declaration (kind + mode + message +
// optional defaults) that a user can reference by name in .reconc.yml:
//
//	rules:
//	  - id: my-tests
//	    template: js-tests-on-src
//	    when_paths: ["src/**/*.ts"]
//
// At parse time, the template's fields are merged into the rule as
// defaults (user-supplied fields always win). The resolved rule is
// then validated by the normal rule parser.
//
// Built-in templates live in builtin/*.yml (embedded). User-defined
// templates live in $RECONC_HOME/templates/*.yml and take precedence
// over built-ins if a name collides.
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/safename"
)

const (
	maxUserTemplateBytes   = 8 << 20
	maxUserTemplateEntries = 4096
)

//go:embed builtin/*.yml
var builtinFS embed.FS

// Source identifies where a template came from. Useful for
// `reconc template show NAME` output so the user can tell whether a
// given name is overriding a built-in.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
)

// Template is one resolved template definition. Fields mirror the
// subset of rule fields that make sense as defaults. We keep the
// parsed body as a generic map so merging into the rule item is a
// simple field-level merge -- no special cases per rule kind.
type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      Source `json:"source"`
	Path        string `json:"path"`
	// Body is the raw YAML body as a map. Fields that aren't recognised
	// by the rule parser will be rejected at parse time, which is the
	// correct behaviour -- templates can't invent new rule kinds.
	Body map[string]interface{} `json:"body"`
}

// Resolve looks up a template by name. Lookup order:
//  1. $RECONC_HOME/templates/<name>.yml  (user override)
//  2. builtin/<name>.yml                 (bundled default)
//
// Returns ErrNotFound when neither location has the template. Errors
// are descriptive enough to surface directly to the user.
func Resolve(name string) (*Template, error) {
	cleaned, err := safename.Normalize("template", name)
	if err != nil {
		return nil, err
	}
	// User override wins.
	home, err := userTemplatesDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user template directory: %w", err)
	}
	if home != "" {
		path := filepath.Join(home, cleaned+".yml")
		if data, err := readRegularFile(path); err == nil {
			body, description, perr := parseTemplateBytes(data, path)
			if perr != nil {
				return nil, perr
			}
			return &Template{
				Name: cleaned, Description: description, Source: SourceUser,
				Path: path, Body: body,
			}, nil
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read user template %s: %w", cleaned, err)
		}
	}

	// Built-in fallback.
	path := "builtin/" + cleaned + ".yml"
	data, err := builtinFS.ReadFile(path)
	if err != nil {
		return nil, &ErrNotFound{Name: cleaned}
	}
	body, description, perr := parseTemplateBytes(data, path)
	if perr != nil {
		return nil, perr
	}
	return &Template{
		Name: cleaned, Description: description, Source: SourceBuiltin,
		Path: path, Body: body,
	}, nil
}

// List enumerates all available templates (user + builtin, merged,
// user-wins on name collisions). Sorted by name ascending.
func List() ([]Template, error) {
	out := map[string]Template{}

	// Built-ins first.
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil, fmt.Errorf("list builtin templates: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yml")
		if _, err := safename.Normalize("template", name); err != nil {
			return nil, fmt.Errorf("invalid builtin template filename %q: %w", e.Name(), err)
		}
		path := "builtin/" + e.Name()
		data, err := builtinFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read builtin template %s: %w", name, err)
		}
		body, description, err := parseTemplateBytes(data, path)
		if err != nil {
			return nil, err
		}
		out[name] = Template{
			Name: name, Description: description, Source: SourceBuiltin,
			Path: path, Body: body,
		}
	}

	// User overrides.
	home, err := userTemplatesDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user template directory: %w", err)
	}
	if home != "" {
		entries, err := boundedio.ReadDirNoSymlink(home, maxUserTemplateEntries)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("list user templates: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".yml")
			if _, err := safename.Normalize("template", name); err != nil {
				return nil, err
			}
			path := filepath.Join(home, e.Name())
			data, err := readRegularFile(path)
			if err != nil {
				return nil, fmt.Errorf("read user template %s: %w", name, err)
			}
			body, description, err := parseTemplateBytes(data, path)
			if err != nil {
				return nil, err
			}
			out[name] = Template{
				Name: name, Description: description, Source: SourceUser,
				Path: path, Body: body,
			}
		}
	}

	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]Template, 0, len(names))
	for _, n := range names {
		list = append(list, out[n])
	}
	return list, nil
}

// Apply merges the template's Body into the user rule item.
// User-supplied fields always win over template defaults.
//
// The template's literal fields are stripped before return (so
// description etc. don't leak into the rule).
func Apply(tmpl *Template, userItem map[string]interface{}) map[string]interface{} {
	merged := map[string]interface{}{}
	for k, v := range tmpl.Body {
		// Strip template-only metadata that isn't a rule field.
		if k == "description" || k == "template" {
			continue
		}
		merged[k] = v
	}
	for k, v := range userItem {
		if k == "template" {
			continue // the reference itself is consumed by resolution
		}
		merged[k] = v // user wins
	}
	return merged
}

// ErrNotFound is returned when a template name doesn't resolve.
type ErrNotFound struct {
	Name string
}

func (e *ErrNotFound) Error() string {
	return "template '" + e.Name + "' not found (try `reconc template list`)"
}

// -------- helpers ----------------------------------------------------

func userTemplatesDir() (string, error) {
	return presets.UserDirectory("templates")
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file and not a symlink", path)
	}
	return boundedio.ReadRegularFile(path, maxUserTemplateBytes)
}

func parseTemplateBytes(data []byte, contextPath string) (map[string]interface{}, string, error) {
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, "", fmt.Errorf("parse template %s: %w", contextPath, err)
	}
	description := ""
	if d, ok := parsed["description"].(string); ok {
		description = d
	}
	// YAML unmarshal produces map[interface{}]interface{} for nested
	// maps by default; yaml.v3 uses map[string]interface{}. We do a
	// shallow normalisation just in case.
	normalised := normaliseStringKeyedMap(parsed)
	return normalised, description, nil
}

func normaliseStringKeyedMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = normaliseValue(v)
	}
	return out
}

func normaliseValue(v interface{}) interface{} {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		out := map[string]interface{}{}
		for k, val := range x {
			if ks, ok := k.(string); ok {
				out[ks] = normaliseValue(val)
			}
		}
		return out
	case map[string]interface{}:
		return normaliseStringKeyedMap(x)
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, val := range x {
			out[i] = normaliseValue(val)
		}
		return out
	}
	return v
}
