package buildprovenance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

	root = writeSourceFixture(t)
	initial = mustSourceDigest(t, root)
	writeTestFile(t, root, "cmd/reconc/config/.hidden.txt", "embedded-hidden-change\n")
	if got := mustSourceDigest(t, root); got == initial {
		t.Fatal("hidden explicitly matched asset change did not invalidate digest")
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

func TestEmbeddedDiscoveryMatchesGoToolchainHiddenSemantics(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example.test/embed\n\ngo 1.27\n")
	for _, relative := range []string{
		"assets/visible.txt",
		"assets/.wild.txt",
		"assets/_wild.txt",
		"assets/nested/visible.txt",
		"assets/nested/.deep.txt",
		"assets/nested/_deep.txt",
		"assets/.hidden-dir/visible.txt",
		"assets/.hidden-dir/.deep.txt",
	} {
		writeTestFile(t, root, relative, relative+"\n")
	}
	for _, pattern := range []string{"assets/*", "assets", "all:assets"} {
		t.Run(pattern, func(t *testing.T) {
			writeTestFile(t, root, "fixture.go", "package fixture\n\nimport \"embed\"\n\n//go:embed "+pattern+"\nvar files embed.FS\n")
			want := goEmbedFiles(t, root)
			got, err := matchEmbeddedFiles(root, pattern)
			if err != nil {
				t.Fatalf("match %q: %v", pattern, err)
			}
			if !equalStringSlices(got, want) {
				t.Fatalf("match %q = %v, go list = %v", pattern, got, want)
			}
		})
	}
}

func TestBuildMarkerRoundTripAndBinaryInspection(t *testing.T) {
	digest := strings.Repeat("a", 64)
	marker, err := FormatMarker(Provenance{
		Version:      "0.8.5",
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
	if parsed.Version != "0.8.5" || parsed.GOOS != "darwin" || parsed.GOARCH != "arm64" || parsed.SourceDigest != digest {
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
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(7, 0); err != nil {
		t.Fatal(err)
	}
	opened, err := InspectOpenFile(file)
	if err != nil {
		t.Fatalf("inspect open binary: %v", err)
	}
	if opened != parsed {
		t.Fatalf("open-file provenance %#v, want %#v", opened, parsed)
	}
	if offset, err := file.Seek(0, 1); err != nil || offset != 7 {
		t.Fatalf("open-file offset = %d, %v; want 7", offset, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectBinaryStreamsAcrossChunkBoundaryAndRejectsUnsafeInputs(t *testing.T) {
	marker, err := FormatMarker(Provenance{
		Version: "0.9.5", GOOS: "darwin", GOARCH: "arm64",
		SourceDigest: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "boundary")
	prefix := strings.Repeat("x", (64<<10)-len(MarkerPrefix)/2)
	if err := os.WriteFile(binary, []byte(prefix+marker+"suffix"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := InspectBinary(binary); err != nil || got.SourceDigest != strings.Repeat("c", 64) {
		t.Fatalf("boundary marker = %#v, %v", got, err)
	}
	sparse := filepath.Join(root, "oversized")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxBinaryBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectBinary(sparse); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized binary error = %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(binary, link); err == nil {
		if _, err := InspectBinary(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("symlink binary error = %v", err)
		}
	}
}

func TestInspectBinaryFailsClosedOnMissingMalformedAndAmbiguousMarkers(t *testing.T) {
	validA, err := FormatMarker(Provenance{
		Version:      "0.8.5",
		GOOS:         "darwin",
		GOARCH:       "arm64",
		SourceDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	validB, err := FormatMarker(Provenance{
		Version:      "0.8.5",
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
		{name: "malformed", data: MarkerPrefix + "|version=0.8.5|goos=darwin|goarch=arm64|source=bad|end"},
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
		{Version: "0.8.5|bad", GOOS: "darwin", GOARCH: "arm64", SourceDigest: strings.Repeat("a", 64)},
		{Version: "0.8.5", GOOS: "Darwin", GOARCH: "arm64", SourceDigest: strings.Repeat("a", 64)},
		{Version: "0.8.5", GOOS: "darwin", GOARCH: "arm64", SourceDigest: "ABC"},
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

func goEmbedFiles(t *testing.T, root string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-json", ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	body, err := command.Output()
	if err != nil {
		t.Fatalf("go list embed fixture: %v", err)
	}
	var packageInfo struct {
		EmbedFiles []string `json:"EmbedFiles"`
	}
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		t.Fatalf("decode go list output: %v", err)
	}
	sort.Strings(packageInfo.EmbedFiles)
	return packageInfo.EmbedFiles
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
