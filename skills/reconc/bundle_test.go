package reconc

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedSkillArchiveAndReferences(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 || files[0].Path != "SKILL.md" {
		t.Fatalf("embedded inventory = %+v", files)
	}
	byPath := make(map[string]File, len(files))
	for _, file := range files {
		if _, duplicate := byPath[file.Path]; duplicate {
			t.Fatalf("duplicate embedded skill path %q", file.Path)
		}
		sum := sha256.Sum256(file.Data)
		if len(file.Data) != file.Size || hex.EncodeToString(sum[:]) != file.SHA256 {
			t.Fatalf("embedded skill digest mismatch for %s", file.Path)
		}
		byPath[file.Path] = file
	}
	links := regexp.MustCompile(`\]\((references/[^)]+\.md)\)`).FindAllStringSubmatch(string(files[0].Data), -1)
	if len(links) != len(files)-1 {
		t.Fatalf("SKILL.md links to %d references, bundle contains %d", len(links), len(files)-1)
	}
	for _, link := range links {
		if _, ok := byPath[link[1]]; !ok {
			t.Fatalf("SKILL.md reference %q is absent from embedded bundle", link[1])
		}
	}
	manifest, err := BuildManifest()
	if err != nil || manifest.Name != Name || len(manifest.Files) != len(files) || len(manifest.Digest) != 64 {
		t.Fatalf("skill manifest = %+v, %v", manifest, err)
	}
	first, err := Archive()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Archive()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("skill archive is not deterministic: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != len(files) {
		t.Fatalf("skill archive has %d entries, want %d", len(reader.File), len(files))
	}
	for index, entry := range reader.File {
		if entry.Name != Name+"/"+files[index].Path || !entry.Mode().IsRegular() || entry.Mode().Perm() != 0o644 {
			t.Fatalf("skill archive entry %d is not canonical: %s %s", index, entry.Name, entry.Mode())
		}
		opened, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(opened, int64(files[index].Size+1)))
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(body, files[index].Data) {
			t.Fatalf("archive entry %s differs from embedded source: %v %v", entry.Name, readErr, closeErr)
		}
	}
	encoded, err := EncodeManifest()
	if err != nil || !strings.Contains(string(encoded), manifest.Digest) {
		t.Fatalf("encoded skill manifest lost its digest: %v", err)
	}
}
