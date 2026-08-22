package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditArchitectureBoundariesRejectsCoreImportingModule(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeArchConfig(t, root)
	writeFile(t, root, "codebase/backend/project/internal/core/scheduler/scheduler.go", `package scheduler

import _ "example.com/project/codebase/backend/project/internal/modules/email"
`)

	failures := auditArchitectureBoundaries(root)
	if !containsFailure(failures, "core services must not depend on modules") {
		t.Fatalf("expected core-module boundary failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditArchitectureBoundariesRejectsSiblingModuleImport(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeArchConfig(t, root)
	writeFile(t, root, "codebase/backend/project/internal/modules/email/email.go", `package email

import _ "example.com/project/codebase/backend/project/internal/modules/calendar"
`)

	failures := auditArchitectureBoundaries(root)
	if !containsFailure(failures, "modules communicate through EventBus/UCS only") {
		t.Fatalf("expected sibling module import failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditArchitectureBoundariesAllowsEmptyBackendWithConfig(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeArchConfig(t, root)

	if failures := auditArchitectureBoundaries(root); len(failures) > 0 {
		t.Fatalf("expected empty backend to pass when config exists, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditImportBoundaryIgnoresCoincidentalProjectSubstring(t *testing.T) {
	cfg := stackConfig{Project: "project"}
	tests := []struct {
		name string
		pkg  string
		imp  string
	}{
		{
			name: "core external package",
			pkg:  "backend/project/internal/core/scheduler",
			imp:  "example.com/notproject/internal/modules/calendar",
		},
		{
			name: "public external package",
			pkg:  "backend/project/pkg/api",
			imp:  "example.com/notproject/internal/private",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if failure := auditImportBoundary(test.pkg, test.imp, cfg); failure != "" {
				t.Fatalf("coincidental substring created an internal import: %s", failure)
			}
		})
	}
}

func TestImportToRepoPathRequiresConfiguredProjectBoundaries(t *testing.T) {
	if got := importToRepoPath("example.com/project/codebase/backend/project/internal/core", "project"); got != "codebase/backend/project/internal/core" {
		t.Fatalf("internal import mapped to %q", got)
	}
	for _, external := range []string{
		"example.com/notproject/codebase/backend/project/internal/core",
		"example.com/projectile/codebase/backend/project/internal/core",
		"example.com/project/notcodebase/backend/project/internal/core",
	} {
		if got := importToRepoPath(external, "project"); got != "" {
			t.Fatalf("external import %q mapped to internal node %q", external, got)
		}
	}
}

func TestAuditPackageCyclesUsesConfiguredProjectForSharedPackages(t *testing.T) {
	root := t.TempDir()
	sharedPath := filepath.Join(root, "codebase/backend/shared/bridge")
	projectPath := filepath.Join(root, "codebase/backend/project/pkg/api")
	packages := []goPackageNode{
		{path: sharedPath, imports: []string{"example.com/project/codebase/backend/project/pkg/api"}},
		{path: projectPath, imports: []string{"example.com/project/codebase/backend/shared/bridge"}},
	}
	failures := auditPackageCycles(root, packages, stackConfig{Project: "project"})
	if !containsFailure(failures, "architecture import cycle detected") {
		t.Fatalf("shared/project cycle was not detected: %v", failures)
	}
}

func writeArchConfig(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, "codebase/config/arch/arch-rules.yaml", `version: 1
whitelist: codebase/config/arch/arch-rules-whitelist.yaml
rules:
  - id: core-services
    path_glob: codebase/backend/project/internal/core/**
  - id: feature-modules
    path_glob: codebase/backend/project/internal/modules/{name}/**
  - id: connectors
    path_glob: codebase/backend/project/internal/connectors/**
  - id: public-shared
    path_glob: [codebase/backend/project/pkg/**, codebase/backend/shared/**]
`)
	writeFile(t, root, "codebase/config/arch/arch-rules-whitelist.yaml", "version: 1\ntemporary_exceptions: []\n")
}
