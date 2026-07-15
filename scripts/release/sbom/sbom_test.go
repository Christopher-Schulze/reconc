package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestRenderDocumentsAreDeterministicAndComplete(t *testing.T) {
	inventory := inventory{
		Version: "9.8.7", Commit: testCommit, Created: time.Unix(1_700_000_000, 0).UTC(), Toolchain: "go1.26.5",
		Modules: []moduleRecord{
			{Path: "example.test/app", Version: "v9.8.7", Root: true},
			{Path: "example.test/harness", Version: "v9.8.7", Root: true},
			{Path: "example.test/dependency", Version: "v1.2.3", Sum: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		},
		Edges: []moduleEdge{{From: "example.test/app@v9.8.7", To: "example.test/dependency@v1.2.3"}},
	}
	firstSPDX, firstCycloneDX, err := renderDocuments(inventory)
	if err != nil {
		t.Fatal(err)
	}
	secondSPDX, secondCycloneDX, err := renderDocuments(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstSPDX) != string(secondSPDX) || string(firstCycloneDX) != string(secondCycloneDX) {
		t.Fatal("identical inventory must render byte-identical SBOMs")
	}
	assertSPDX(t, firstSPDX)
	assertCycloneDX(t, firstCycloneDX)
}

func TestGenerateAndVerifyRealRepositoryInventory(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	options := commandOptions{root: root, outputDir: t.TempDir(), version: "9.8.7", commit: testCommit, epoch: "1700000000"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inventory, err := collectInventory(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	spdx, cyclonedx, err := renderDocuments(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDocuments(options.outputDir, options.version, spdx, cyclonedx); err != nil {
		t.Fatal(err)
	}
	if err := verifyDocuments(options.outputDir, options.version, spdx, cyclonedx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(options.outputDir, "reconc-9.8.7.spdx.json")
	if err := os.WriteFile(path, append(spdx, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDocuments(options.outputDir, options.version, spdx, cyclonedx); err == nil {
		t.Fatal("tampered SBOM must fail deterministic verification")
	}
}

func TestResolveModuleKeyUsesSelectedVersion(t *testing.T) {
	lookup := moduleLookup([]moduleRecord{{Path: "example.test/dependency", Version: "v2.0.0"}})
	key, ok := resolveModuleKey("example.test/dependency@v1.0.0", lookup)
	if !ok || key != "example.test/dependency@v2.0.0" {
		t.Fatalf("graph reference did not resolve to selected module version: %q, %t", key, ok)
	}
}

func assertSPDX(t *testing.T, body []byte) {
	t.Helper()
	var document spdxDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.SPDXVersion != "SPDX-2.3" || len(document.Packages) != 4 {
		t.Fatalf("unexpected SPDX document: %#v", document)
	}
	if !strings.HasSuffix(document.DocumentNamespace, "/"+testCommit) {
		t.Fatalf("SPDX document does not expose source commit %q: %q", testCommit, document.DocumentNamespace)
	}
	if countRelationships(document.Relationships, "DESCRIBES") != 2 || countRelationships(document.Relationships, "DEPENDS_ON") != 1 {
		t.Fatalf("SPDX relationships do not describe both roots and dependencies: %#v", document.Relationships)
	}
}

func assertCycloneDX(t *testing.T, body []byte) {
	t.Helper()
	var document cyclonedxDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.BOMFormat != "CycloneDX" || document.SpecVersion != "1.6" || !strings.HasPrefix(document.SerialNumber, "urn:uuid:") {
		t.Fatalf("unexpected CycloneDX identity: %#v", document)
	}
	if document.Metadata.Component.Name != "app" || len(document.Components) != 3 || len(document.Dependencies) != 4 {
		t.Fatalf("CycloneDX inventory is incomplete: %#v", document)
	}
	if value, ok := cyclonedxPropertyValue(document.Metadata.Component.Properties, "reconc:source-commit"); !ok || value != testCommit {
		t.Fatalf("CycloneDX document does not expose source commit %q: %#v", testCommit, document.Metadata.Component.Properties)
	}
}

func cyclonedxPropertyValue(properties []cyclonedxProperty, name string) (string, bool) {
	for _, property := range properties {
		if property.Name == name {
			return property.Value, true
		}
	}
	return "", false
}

func countRelationships(relationships []spdxRelationship, kind string) int {
	count := 0
	for _, relationship := range relationships {
		if relationship.RelationshipType == kind {
			count++
		}
	}
	return count
}
