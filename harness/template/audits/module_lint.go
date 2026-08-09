package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func auditModuleContracts(root string) []string {
	var failures []string
	modulesBase := moduleManifestBase(root)
	modules := discoverModuleNames(root)
	for _, name := range modules {
		manifestRel := filepath.ToSlash(rel(root, filepath.Join(modulesBase, name, "MODULE.yaml")))
		manifestPath := filepath.Join(modulesBase, name, "MODULE.yaml")
		if !exists(manifestPath) {
			failures = append(failures, fmt.Sprintf("%s missing for module %s", manifestRel, name))
			continue
		}
		manifest, err := readAuditFile(manifestPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("read %s: %v", manifestRel, err))
			continue
		}
		failures = append(failures, auditModuleManifest(name, manifestRel, string(manifest))...)
		failures = append(failures, auditModuleDirectImports(root, name)...)
		if moduleManifestValue(string(manifest), "spec_state") == "stub" {
			failures = append(failures, auditStubModuleSurface(root, name)...)
		}
	}
	return failures
}

// moduleManifestBase returns the directory that holds module manifest dirs.
// Codebase-style repos use codebase/modules; flat-root repos use ./modules.
// The choice tracks projectRoot so audit failure messages and the on-disk
// path stay in sync even when codebase/modules/ has not been created yet.
func moduleManifestBase(root string) string {
	return filepath.Join(projectRoot(root), "modules")
}

// moduleBackendDir returns the backend module dir for the given name, looking
// in both codebase- and flat-root layouts. Returns "" if no candidate exists.
func moduleBackendDir(root string, name string) string {
	for _, candidate := range []string{
		filepath.Join(root, "codebase/backend/project/internal/modules", name),
		filepath.Join(root, "backend/project/internal/modules", name),
	} {
		if exists(candidate) {
			return candidate
		}
	}
	return ""
}

// moduleSurfaceDirs returns existing backend + frontend module dirs for the
// given module name across both supported layouts.
func moduleSurfaceDirs(root string, name string) []string {
	var dirs []string
	for _, candidate := range []string{
		filepath.Join(root, "codebase/backend/project/internal/modules", name),
		filepath.Join(root, "codebase/frontend/modules", name),
		filepath.Join(root, "backend/project/internal/modules", name),
		filepath.Join(root, "frontend/modules", name),
	} {
		if exists(candidate) {
			dirs = append(dirs, candidate)
		}
	}
	return dirs
}

func discoverModuleNames(root string) []string {
	seen := map[string]bool{}
	bases := []string{
		filepath.Join(root, "codebase/modules"),
		filepath.Join(root, "codebase/backend/project/internal/modules"),
		filepath.Join(root, "codebase/frontend/modules"),
		filepath.Join(root, "modules"),
		filepath.Join(root, "backend/project/internal/modules"),
		filepath.Join(root, "frontend/modules"),
	}
	for _, base := range bases {
		entries, err := readAuditDirectory(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				seen[entry.Name()] = true
			}
		}
	}
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

func auditModuleManifest(name string, manifestRel string, manifest string) []string {
	var failures []string
	required := []string{"name", "version", "spec_state", "owner_layer", "permissions_required", "requires_connectors", "runtime_dependencies", "generated_reference_family"}
	for _, field := range required {
		if moduleManifestValue(manifest, field) == "" && !strings.Contains(manifest, field+":") {
			failures = append(failures, fmt.Sprintf("%s missing field %s", manifestRel, field))
		}
	}
	if got := moduleManifestValue(manifest, "name"); got != "" && got != name {
		failures = append(failures, fmt.Sprintf("%s name=%s does not match directory", manifestRel, got))
	}
	if owner := moduleManifestValue(manifest, "owner_layer"); owner != "" && owner != "feature_module" {
		failures = append(failures, fmt.Sprintf("%s owner_layer=%s invalid; use feature_module", manifestRel, owner))
	}
	if state := moduleManifestValue(manifest, "spec_state"); state != "" && state != "stub" && state != "preview" && state != "supported" {
		failures = append(failures, fmt.Sprintf("%s spec_state=%s invalid; use stub, preview, or supported", manifestRel, state))
	}
	return failures
}

func auditModuleDirectImports(root string, name string) []string {
	base := moduleBackendDir(root, name)
	if base == "" {
		return nil
	}
	var failures []string
	err := walkAuditTree(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			failures = append(failures, fmt.Sprintf("walk %s: %v", rel(root, path), err))
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			failures = append(failures, fmt.Sprintf("%s: parse imports: %v", rel(root, path), parseErr))
			return nil
		}
		for _, spec := range file.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			if imp == "net/http" || imp == "os/exec" {
				failures = append(failures, fmt.Sprintf("%s imports %s directly; feature modules must use UCS/Tool runtime boundaries", rel(root, path), imp))
			}
		}
		return nil
	})
	if err != nil {
		failures = append(failures, fmt.Sprintf("walk module %s: %v", name, err))
	}
	return failures
}

func auditStubModuleSurface(root string, name string) []string {
	var failures []string
	for _, base := range moduleSurfaceDirs(root, name) {
		err := walkAuditTree(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				failures = append(failures, fmt.Sprintf("walk %s: %v", rel(root, path), err))
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".tsx") || strings.HasSuffix(entry.Name(), ".yaml") {
				content, readErr := readAuditFile(path)
				if readErr != nil {
					failures = append(failures, fmt.Sprintf("read %s: %v", rel(root, path), readErr))
					return nil
				}
				text := string(content)
				for _, token := range []string{"Route(", "RegisterRoute", "AppLibrary", "settingsPanel", "SettingsPanel", "provides_ui_mode: true"} {
					if strings.Contains(text, token) {
						failures = append(failures, fmt.Sprintf("%s exposes %s while MODULE.yaml spec_state=stub", rel(root, path), token))
					}
				}
			}
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Sprintf("walk stub module %s: %v", name, err))
		}
	}
	return failures
}

func moduleManifestValue(manifest string, key string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*"?([^"\n#]+)"?\s*(?:#.*)?$`)
	match := re.FindStringSubmatch(manifest)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
