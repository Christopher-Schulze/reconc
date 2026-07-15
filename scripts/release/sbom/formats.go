package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

type spdxDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      spdxCreationInfo   `json:"creationInfo"`
	Packages          []spdxPackage      `json:"packages"`
	Relationships     []spdxRelationship `json:"relationships"`
}

type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	Name             string            `json:"name"`
	SPDXID           string            `json:"SPDXID"`
	VersionInfo      string            `json:"versionInfo"`
	DownloadLocation string            `json:"downloadLocation"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	LicenseConcluded string            `json:"licenseConcluded"`
	LicenseDeclared  string            `json:"licenseDeclared"`
	CopyrightText    string            `json:"copyrightText"`
	Checksums        []spdxChecksum    `json:"checksums,omitempty"`
	ExternalRefs     []spdxExternalRef `json:"externalRefs"`
}

type spdxChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

type spdxExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type spdxRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

type cyclonedxDocument struct {
	BOMFormat    string                `json:"bomFormat"`
	SpecVersion  string                `json:"specVersion"`
	SerialNumber string                `json:"serialNumber"`
	Version      int                   `json:"version"`
	Metadata     cyclonedxMetadata     `json:"metadata"`
	Components   []cyclonedxComponent  `json:"components"`
	Dependencies []cyclonedxDependency `json:"dependencies"`
}

type cyclonedxMetadata struct {
	Timestamp string             `json:"timestamp"`
	Tools     cyclonedxTools     `json:"tools"`
	Component cyclonedxComponent `json:"component"`
}

type cyclonedxTools struct {
	Components []cyclonedxComponent `json:"components"`
}

type cyclonedxComponent struct {
	Type       string              `json:"type"`
	BOMRef     string              `json:"bom-ref"`
	Group      string              `json:"group,omitempty"`
	Name       string              `json:"name"`
	Version    string              `json:"version"`
	PURL       string              `json:"purl,omitempty"`
	Hashes     []cyclonedxHash     `json:"hashes,omitempty"`
	Properties []cyclonedxProperty `json:"properties,omitempty"`
}

type cyclonedxHash struct {
	Algorithm string `json:"alg"`
	Content   string `json:"content"`
}

type cyclonedxProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cyclonedxDependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn"`
}

func renderDocuments(inventory inventory) ([]byte, []byte, error) {
	spdx, err := json.MarshalIndent(buildSPDX(inventory), "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal SPDX SBOM: %w", err)
	}
	cyclonedx, err := json.MarshalIndent(buildCycloneDX(inventory), "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal CycloneDX SBOM: %w", err)
	}
	return append(spdx, '\n'), append(cyclonedx, '\n'), nil
}

func buildSPDX(inventory inventory) spdxDocument {
	packages := make([]spdxPackage, 0, len(inventory.Modules)+1)
	for _, module := range inventory.Modules {
		packages = append(packages, spdxModulePackage(module))
	}
	packages = append(packages, spdxToolchainPackage(inventory.Toolchain))
	relationships := spdxRelationships(inventory)
	return spdxDocument{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name:              "reconc-" + inventory.Version,
		DocumentNamespace: fmt.Sprintf("https://github.com/Christopher-Schulze/reconc/sbom/reconc-v%s/%s", inventory.Version, inventory.Commit),
		CreationInfo:      spdxCreationInfo{Created: inventory.Created.Format("2006-01-02T15:04:05Z"), Creators: []string{"Tool: reconc-sbom-generator-" + inventory.Version}},
		Packages:          packages, Relationships: relationships,
	}
}

func spdxModulePackage(module moduleRecord) spdxPackage {
	purl := modulePURL(module.Path, module.Version)
	pack := spdxPackage{
		Name: path.Base(module.Path), SPDXID: spdxID(moduleKey(module.Path, module.Version)), VersionInfo: module.Version,
		DownloadLocation: "NOASSERTION", FilesAnalyzed: false, LicenseConcluded: "NOASSERTION", LicenseDeclared: "NOASSERTION", CopyrightText: "NOASSERTION",
		ExternalRefs: []spdxExternalRef{{ReferenceCategory: "PACKAGE-MANAGER", ReferenceType: "purl", ReferenceLocator: purl}},
	}
	if digest := goSumDigest(module.Sum); digest != "" {
		pack.Checksums = []spdxChecksum{{Algorithm: "SHA256", ChecksumValue: digest}}
	}
	return pack
}

func spdxToolchainPackage(version string) spdxPackage {
	return spdxPackage{
		Name: "go", SPDXID: spdxID("go-toolchain@" + version), VersionInfo: version,
		DownloadLocation: "https://go.dev/dl/", FilesAnalyzed: false, LicenseConcluded: "NOASSERTION", LicenseDeclared: "BSD-3-Clause", CopyrightText: "Copyright The Go Authors",
		ExternalRefs: []spdxExternalRef{{ReferenceCategory: "PACKAGE-MANAGER", ReferenceType: "purl", ReferenceLocator: "pkg:generic/go@" + version}},
	}
}

func spdxRelationships(inventory inventory) []spdxRelationship {
	relationships := []spdxRelationship{}
	for _, module := range inventory.Modules {
		if module.Root {
			relationships = append(relationships, spdxRelationship{SPDXElementID: "SPDXRef-DOCUMENT", RelationshipType: "DESCRIBES", RelatedSPDXElement: spdxID(moduleKey(module.Path, module.Version))})
		}
	}
	for _, edge := range inventory.Edges {
		relationships = append(relationships, spdxRelationship{SPDXElementID: spdxID(edge.From), RelationshipType: "DEPENDS_ON", RelatedSPDXElement: spdxID(edge.To)})
	}
	sort.Slice(relationships, func(i, j int) bool { return relationshipKey(relationships[i]) < relationshipKey(relationships[j]) })
	return relationships
}

func buildCycloneDX(inventory inventory) cyclonedxDocument {
	root := primaryRootModule(inventory.Modules)
	components := make([]cyclonedxComponent, 0, len(inventory.Modules))
	for _, module := range inventory.Modules {
		if module.Path != root.Path {
			components = append(components, cyclonedxModuleComponent(module))
		}
	}
	components = append(components, cyclonedxToolchainComponent(inventory.Toolchain))
	return cyclonedxDocument{
		BOMFormat: "CycloneDX", SpecVersion: "1.6", SerialNumber: deterministicSerial(inventory), Version: 1,
		Metadata: cyclonedxMetadata{
			Timestamp: inventory.Created.Format("2006-01-02T15:04:05Z"), Component: cyclonedxModuleComponent(root),
			Tools: cyclonedxTools{Components: []cyclonedxComponent{{Type: "application", BOMRef: "pkg:generic/reconc-sbom-generator@" + inventory.Version, Name: "reconc-sbom-generator", Version: inventory.Version}}},
		},
		Components: components, Dependencies: cyclonedxDependencies(inventory),
	}
}

func cyclonedxModuleComponent(module moduleRecord) cyclonedxComponent {
	component := cyclonedxComponent{
		Type: "library", BOMRef: modulePURL(module.Path, module.Version), Group: path.Dir(module.Path), Name: path.Base(module.Path), Version: module.Version, PURL: modulePURL(module.Path, module.Version),
		Properties: []cyclonedxProperty{{Name: "reconc:go-module-root", Value: fmt.Sprintf("%t", module.Root)}},
	}
	if module.Root {
		component.Type = "application"
	}
	if digest := goSumDigest(module.Sum); digest != "" {
		component.Hashes = []cyclonedxHash{{Algorithm: "SHA-256", Content: digest}}
	}
	return component
}

func cyclonedxToolchainComponent(version string) cyclonedxComponent {
	return cyclonedxComponent{Type: "application", BOMRef: "pkg:generic/go@" + version, Name: "go", Version: version, PURL: "pkg:generic/go@" + version}
}

func cyclonedxDependencies(inventory inventory) []cyclonedxDependency {
	dependsOn := make(map[string][]string)
	for _, module := range inventory.Modules {
		dependsOn[modulePURL(module.Path, module.Version)] = []string{}
	}
	for _, edge := range inventory.Edges {
		from := moduleKeyToPURL(edge.From)
		dependsOn[from] = append(dependsOn[from], moduleKeyToPURL(edge.To))
	}
	dependencies := make([]cyclonedxDependency, 0, len(dependsOn)+1)
	for ref, targets := range dependsOn {
		sort.Strings(targets)
		dependencies = append(dependencies, cyclonedxDependency{Ref: ref, DependsOn: targets})
	}
	dependencies = append(dependencies, cyclonedxDependency{Ref: "pkg:generic/go@" + inventory.Toolchain, DependsOn: []string{}})
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Ref < dependencies[j].Ref })
	return dependencies
}

func modulePURL(modulePath, version string) string {
	return "pkg:golang/" + modulePath + "@" + version
}

func moduleKeyToPURL(key string) string {
	index := strings.LastIndexByte(key, '@')
	if index < 0 {
		return "pkg:golang/" + key
	}
	return modulePURL(key[:index], key[index+1:])
}

func spdxID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "SPDXRef-Package-" + hex.EncodeToString(digest[:12])
}

func relationshipKey(relationship spdxRelationship) string {
	return relationship.SPDXElementID + "\x00" + relationship.RelationshipType + "\x00" + relationship.RelatedSPDXElement
}

func primaryRootModule(modules []moduleRecord) moduleRecord {
	for _, module := range modules {
		if module.Path == "reconc.dev/reconc" && module.Root {
			return module
		}
	}
	for _, module := range modules {
		if module.Root {
			return module
		}
	}
	return moduleRecord{}
}

func deterministicSerial(inventory inventory) string {
	seed := inventory.Version + "\x00" + inventory.Commit + "\x00" + inventory.Created.Format("2006-01-02T15:04:05Z")
	digest := sha256.Sum256([]byte(seed))
	id := digest[:16]
	id[6] = (id[6] & 0x0f) | 0x80
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func goSumDigest(sum string) string {
	if !strings.HasPrefix(sum, "h1:") {
		return ""
	}
	digest, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sum, "h1:"))
	if err != nil || len(digest) != sha256.Size {
		return ""
	}
	return hex.EncodeToString(digest)
}
