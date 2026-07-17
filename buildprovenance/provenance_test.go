package buildprovenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComputeSourceDigestTracksTargetProductionInputs(t *testing.T) {
	root := writeSourceFixture(t)
	initial := mustSourceDigest(t, root)

	for _, relative := range []string{
		"cmd/reconc/main.go",
		"internal/feature/feature.go",
		"internal/feature/feature_test.go",
		"cmd/reconc/config/default.txt",
		"go.mod",
		"go.sum",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.Chtimes(path, time.Unix(100, 0), time.Unix(200, 0)); err != nil {
			t.Fatalf("change %s timestamps: %v", relative, err)
		}
	}
	if got := mustSourceDigest(t, root); got != initial {
		t.Fatalf("timestamps changed digest: got %s, want %s", got, initial)
	}

	writeTestFile(t, root, "internal/feature/feature_test.go", "package feature\n\nfunc testOnly() string { return \"changed\" }\n")
	writeTestFile(t, root, "internal/feature/feature_linux.go", "//go:build linux\n\npackage feature\n\nfunc linuxOnly() string { return \"changed\" }\n")
	if got := mustSourceDigest(t, root); got != initial {
		t.Fatalf("test-only or Linux-only source changed Darwin digest: got %s, want %s", got, initial)
	}

	writeTestFile(t, root, "internal/feature/feature.go", "package feature\n\nfunc Value() string { return \"production-change\" }\n")
	if got := mustSourceDigest(t, root); got == initial {
		t.Fatal("production source change did not invalidate digest")
	}

	root = writeSourceFixture(t)
	initial = mustSourceDigest(t, root)
	writeTestFile(t, root, "cmd/reconc/config/default.txt", "embedded-change\n")
	if got := mustSourceDigest(t, root); got == initial {
		t.Fatal("embedded production asset change did not invalidate digest")
	}
}

func TestComputeSourceDigestIsRepoLocationIndependent(t *testing.T) {
	first := writeSourceFixture(t)
	second := writeSourceFixture(t)
	firstDigest := mustSourceDigest(t, first)
	secondDigest := mustSourceDigest(t, second)
	if firstDigest != secondDigest {
		t.Fatalf("identical source bytes in different roots produced %s and %s", firstDigest, secondDigest)
	}
}

func TestBuildMarkerRoundTripAndBinaryInspection(t *testing.T) {
	digest := strings.Repeat("a", 64)
	marker, err := FormatMarker(Provenance{
		Version:      "0.8.4",
		GOOS:         "darwin",
		GOARCH:       "arm64",
		SourceDigest: digest,
	})
	if err != nil {
		t.Fatalf("format marker: %v", err)
	}
	parsed, err := ParseMarker(marker)
	if err != nil {
		t.Fatalf("parse marker: %v", err)
	}
	if parsed.Version != "0.8.4" || parsed.GOOS != "darwin" || parsed.GOARCH != "arm64" || parsed.SourceDigest != digest {
		t.Fatalf("unexpected parsed provenance: %#v", parsed)
	}

	binary := filepath.Join(t.TempDir(), "reconc")
	if err := os.WriteFile(binary, []byte("binary-prefix\x00"+marker+"\x00binary-suffix"), 0o755); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	inspected, err := InspectBinary(binary)
	if err != nil {
		t.Fatalf("inspect binary: %v", err)
	}
	if inspected != parsed {
		t.Fatalf("inspected provenance %#v, want %#v", inspected, parsed)
	}
}

func TestInspectBinaryFailsClosedOnMissingMalformedAndAmbiguousMarkers(t *testing.T) {
	validA, err := FormatMarker(Provenance{
		Version:      "0.8.4",
		GOOS:         "darwin",
		GOARCH:       "arm64",
		SourceDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	validB, err := FormatMarker(Provenance{
		Version:      "0.8.4",
		GOOS:         "darwin",
		GOARCH:       "arm64",
		SourceDigest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data string
	}{
		{name: "missing", data: "plain binary bytes"},
		{name: "malformed", data: MarkerPrefix + "|version=0.8.4|goos=darwin|goarch=arm64|source=bad|end"},
		{name: "duplicate", data: validA + "\x00" + validA},
		{name: "ambiguous", data: validA + "\x00" + validB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "reconc")
			if err := os.WriteFile(binary, []byte(tt.data), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectBinary(binary); err == nil {
				t.Fatalf("expected %s provenance to fail closed", tt.name)
			}
		})
	}
}

func TestFormatMarkerRejectsInvalidFields(t *testing.T) {
	tests := []Provenance{
		{Version: "", GOOS: "darwin", GOARCH: "arm64", SourceDigest: strings.Repeat("a", 64)},
		{Version: "0.8.4|bad", GOOS: "darwin", GOARCH: "arm64", SourceDigest: strings.Repeat("a", 64)},
		{Version: "0.8.4", GOOS: "Darwin", GOARCH: "arm64", SourceDigest: strings.Repeat("a", 64)},
		{Version: "0.8.4", GOOS: "darwin", GOARCH: "arm64", SourceDigest: "ABC"},
	}
	for _, provenance := range tests {
		if _, err := FormatMarker(provenance); err == nil {
			t.Fatalf("expected invalid provenance to fail: %#v", provenance)
		}
	}
}

func writeSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example.test/reconc\n\ngo 1.23\n")
	writeTestFile(t, root, "go.sum", "fixture-checksum\n")
	writeTestFile(t, root, "cmd/reconc/main.go", `package main

import (
	_ "embed"
	"fmt"

	"example.test/reconc/internal/feature"
)

//go:embed config/*.txt
var config string

func main() {
	fmt.Print(feature.Value(), config)
}
`)
	writeTestFile(t, root, "cmd/reconc/config/default.txt", "embedded-default\n")
	writeTestFile(t, root, "internal/feature/feature.go", "package feature\n\nfunc Value() string { return \"production\" }\n")
	writeTestFile(t, root, "internal/feature/feature_darwin.go", "//go:build darwin\n\npackage feature\n\nfunc darwinOnly() string { return \"darwin\" }\n")
	writeTestFile(t, root, "internal/feature/feature_linux.go", "//go:build linux\n\npackage feature\n\nfunc linuxOnly() string { return \"linux\" }\n")
	writeTestFile(t, root, "internal/feature/feature_test.go", "package feature\n\nfunc testOnly() string { return \"test\" }\n")
	return root
}

func writeTestFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func mustSourceDigest(t *testing.T, root string) string {
	t.Helper()
	digest, err := ComputeSourceDigest(root, "darwin", "arm64")
	if err != nil {
		t.Fatalf("compute source digest: %v", err)
	}
	return digest
}
