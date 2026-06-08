package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type goPackageNode struct {
	path    string
	imports []string
}

func auditArchitectureBoundaries(root string) []string {
	cfg, cfgFailures := loadStackConfig(root)
	if len(cfgFailures) > 0 {
		return cfgFailures
	}
	archConfigRel := projectRel(root, "config/arch/arch-rules.yaml")
	archConfig := filepath.Join(root, filepath.FromSlash(archConfigRel))
	if !exists(archConfig) {
		if cfg.ArchitectureBoundaries.Required {
			return []string{fmt.Sprintf("%s missing", archConfigRel)}
		}
		return nil
	}
	var failures []string
	failures = append(failures, auditArchitectureConfig(root, cfg)...)
	packages, packageFailures := collectGoPackages(root, filepath.Join(projectRoot(root), "backend"))
	failures = append(failures, packageFailures...)
	for _, pkg := range packages {
		relPkg := filepath.ToSlash(rel(root, pkg.path))
		for _, imp := range pkg.imports {
			if failure := auditImportBoundary(relPkg, imp, cfg); failure != "" {
				failures = append(failures, failure)
			}
		}
	}
	failures = append(failures, auditPackageCycles(root, packages)...)
	return failures
}

func auditArchitectureConfig(root string, cfg stackConfig) []string {
	archRel := projectRel(root, "config/arch/arch-rules.yaml")
	path := filepath.Join(root, filepath.FromSlash(archRel))
	content, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s missing or unreadable: %v", archRel, err)}
	}
	required := []string{
		"core-services",
		"feature-modules",
		"connectors",
		"public-shared",
		projectRel(root, filepath.ToSlash(filepath.Join("backend", cfg.Project, "internal/core/**"))),
		projectRel(root, filepath.ToSlash(filepath.Join("backend", cfg.Project, "internal/modules/{name}/**"))),
		projectRel(root, filepath.ToSlash(filepath.Join("backend", cfg.Project, "internal/connectors/**"))),
		projectRel(root, filepath.ToSlash(filepath.Join("backend", cfg.Project, "pkg/**"))),
		projectRel(root, "backend/shared/**"),
		projectRel(root, "config/arch/arch-rules-whitelist.yaml"),
	}
	var failures []string
	text := string(content)
	for _, token := range required {
		if !strings.Contains(text, token) {
			failures = append(failures, fmt.Sprintf("%s missing required token %q", archRel, token))
		}
	}
	whitelistRel := projectRel(root, "config/arch/arch-rules-whitelist.yaml")
	if !exists(filepath.Join(root, filepath.FromSlash(whitelistRel))) {
		failures = append(failures, fmt.Sprintf("%s missing", whitelistRel))
	}
	return failures
}

func collectGoPackages(root string, base string) ([]goPackageNode, []string) {
	if !exists(base) {
		return nil, nil
	}
	byDir := map[string]map[string]bool{}
	var failures []string
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			failures = append(failures, fmt.Sprintf("walk %s: %v", rel(root, path), err))
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			failures = append(failures, fmt.Sprintf("%s: parse imports: %v", rel(root, path), parseErr))
			return nil
		}
		dir := filepath.Dir(path)
		if byDir[dir] == nil {
			byDir[dir] = map[string]bool{}
		}
		for _, spec := range file.Imports {
			byDir[dir][strings.Trim(spec.Path.Value, `"`)] = true
		}
		return nil
	})
	if err != nil {
		failures = append(failures, fmt.Sprintf("walk %s: %v", rel(root, base), err))
	}
	var packages []goPackageNode
	for dir, imports := range byDir {
		var sorted []string
		for imp := range imports {
			sorted = append(sorted, imp)
		}
		sort.Strings(sorted)
		packages = append(packages, goPackageNode{path: dir, imports: sorted})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].path < packages[j].path })
	return packages, failures
}

func auditImportBoundary(relPkg string, imp string, cfg stackConfig) string {
	imp = filepath.ToSlash(imp)
	relPkg = trimCodebasePrefix(relPkg)
	project := cfg.Project
	switch {
	case strings.HasPrefix(relPkg, "backend/"+project+"/internal/core/"):
		if strings.Contains(imp, "/backend/"+project+"/internal/modules/") || strings.Contains(imp, project+"/internal/modules/") {
			return fmt.Sprintf("%s imports feature module %s; core services must not depend on modules", relPkg, imp)
		}
	case strings.HasPrefix(relPkg, "backend/"+project+"/internal/modules/"):
		module := moduleNameFromBackendPath(relPkg, cfg)
		if module != "" {
			prefix := project + "/internal/modules/"
			if index := strings.Index(imp, prefix); index >= 0 {
				imported := strings.TrimPrefix(imp[index:], prefix)
				importedName := strings.Split(imported, "/")[0]
				if importedName != "" && importedName != module {
					return fmt.Sprintf("%s imports sibling module %s; modules communicate through EventBus/UCS only", relPkg, imp)
				}
			}
		}
	case strings.HasPrefix(relPkg, "backend/"+project+"/internal/connectors/"):
		if strings.Contains(imp, project+"/internal/") && !strings.Contains(imp, project+"/pkg/") {
			imported := filepath.ToSlash(imp)
			if idx := strings.Index(imported, project+"/internal/connectors"); idx >= 0 {
				return ""
			}
			return fmt.Sprintf("%s imports internal Project package %s; connectors may use stdlib, project/pkg and backend/shared only", relPkg, imp)
		}
	case strings.HasPrefix(relPkg, "backend/"+project+"/pkg/") || strings.HasPrefix(relPkg, "backend/shared/"):
		if strings.Contains(imp, project+"/internal/") {
			return fmt.Sprintf("%s imports internal package %s; public/shared packages must not depend on internals", relPkg, imp)
		}
	}
	return ""
}

func auditPackageCycles(root string, packages []goPackageNode) []string {
	known := map[string]bool{}
	graph := map[string][]string{}
	for _, pkg := range packages {
		relPkg := trimCodebasePrefix(rel(root, pkg.path))
		known[relPkg] = true
	}
	for _, pkg := range packages {
		relPkg := trimCodebasePrefix(rel(root, pkg.path))
		for _, imp := range pkg.imports {
			target := trimCodebasePrefix(importToRepoPath(imp))
			if known[target] {
				graph[relPkg] = append(graph[relPkg], target)
			}
		}
	}
	var failures []string
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var stack []string
	var visit func(string)
	visit = func(node string) {
		if visiting[node] {
			failures = append(failures, fmt.Sprintf("architecture import cycle detected: %s -> %s", strings.Join(stack, " -> "), node))
			return
		}
		if visited[node] {
			return
		}
		visiting[node] = true
		stack = append(stack, node)
		for _, next := range graph[node] {
			visit(next)
		}
		stack = stack[:len(stack)-1]
		visiting[node] = false
		visited[node] = true
	}
	for node := range known {
		visit(node)
	}
	sort.Strings(failures)
	return failures
}

func importToRepoPath(imp string) string {
	imp = filepath.ToSlash(imp)
	for _, marker := range []string{"codebase/backend/", "backend/"} {
		if index := strings.Index(imp, marker); index >= 0 {
			path := imp[index:]
			if strings.HasPrefix(path, "backend/") {
				return "codebase/" + path
			}
			return path
		}
	}
	return ""
}

func moduleNameFromBackendPath(path string, cfg stackConfig) string {
	path = trimCodebasePrefix(path)
	prefix := "backend/" + cfg.Project + "/internal/modules/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	return strings.Split(rest, "/")[0]
}

func trimCodebasePrefix(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "codebase/")
}
