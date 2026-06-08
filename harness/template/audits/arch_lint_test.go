package main

import (
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
