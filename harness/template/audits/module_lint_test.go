package main

import (
	"strings"
	"testing"
)

func TestAuditModuleContractsRejectsMissingManifest(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "codebase/backend/project/internal/modules/email/email.go", "package email\n")

	failures := auditModuleContracts(root)
	if !containsFailure(failures, "codebase/modules/email/MODULE.yaml missing") {
		t.Fatalf("expected missing manifest failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditModuleContractsRejectsDirectTransportImport(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "codebase/modules/email/MODULE.yaml", validModuleManifest("email", "supported"))
	writeFile(t, root, "codebase/backend/project/internal/modules/email/email.go", `package email

import _ "net/http"
`)

	failures := auditModuleContracts(root)
	if !containsFailure(failures, "imports net/http directly") {
		t.Fatalf("expected direct net/http failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditModuleContractsRejectsStubSurface(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "codebase/modules/email/MODULE.yaml", validModuleManifest("email", "stub"))
	writeFile(t, root, "codebase/frontend/modules/email/page.tsx", `export const AppLibrary = {}
`)

	failures := auditModuleContracts(root)
	if !containsFailure(failures, "while MODULE.yaml spec_state=stub") {
		t.Fatalf("expected stub surface failure, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditModuleContractsAcceptsValidStubManifestWithoutSurface(t *testing.T) {
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "codebase/modules/email/MODULE.yaml", validModuleManifest("email", "stub"))

	if failures := auditModuleContracts(root); len(failures) > 0 {
		t.Fatalf("expected valid stub manifest to pass, got:\n%s", strings.Join(failures, "\n"))
	}
}

func validModuleManifest(name string, state string) string {
	return `name: ` + name + `
version: 0.1.0
spec_state: ` + state + `
owner_layer: feature_module
permissions_required: []
requires_connectors: []
runtime_dependencies: []
generated_reference_family: Feature Module Facet Reference
`
}
