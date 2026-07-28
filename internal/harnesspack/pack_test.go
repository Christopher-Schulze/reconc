package harnesspack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuildEncodeLoadRoundTrip(t *testing.T) {
	source := fstest.MapFS{
		"README.md":              {Data: []byte("public harness\n"), Mode: 0o644},
		"utils/run-reconc-prune": {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
	}
	options := BuildOptions{
		Name: "advanced", Version: "1.0.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"task-utilities", "advanced-audits"},
		TargetPrefix:         "tools/reconc/harness/template",
	}
	manifest, err := Build(source, options)
	if err != nil {
		t.Fatal(err)
	}
	body, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := Load(body, source, options.TargetPrefix, "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Files) != 2 || pack.Manifest.Digest == "" ||
		pack.Files[1].File.Mode != 0o755 {
		t.Fatalf("loaded pack = %+v", pack)
	}
}

func TestManifestRejectsUntrustedShapes(t *testing.T) {
	source := fstest.MapFS{"file.txt": {Data: []byte("safe")}}
	options := BuildOptions{
		Name: "advanced", Version: "1.0.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"advanced-audits"},
		TargetPrefix:         "tools/reconc/harness/template",
	}
	manifest, err := Build(source, options)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "traversal", mutate: func(value *Manifest) { value.Files[0].Path = "../escape" }},
		{name: "absolute", mutate: func(value *Manifest) { value.Files[0].Path = "/tmp/escape" }},
		{name: "windows separator", mutate: func(value *Manifest) { value.Files[0].Path = `tools\escape` }},
		{name: "duplicate", mutate: func(value *Manifest) { value.Files = append(value.Files, value.Files[0]) }},
		{name: "mode", mutate: func(value *Manifest) { value.Files[0].Mode = 0o777 }},
		{name: "oversized", mutate: func(value *Manifest) { value.Files[0].Size = MaxFileBytes + 1 }},
		{name: "checksum", mutate: func(value *Manifest) { value.Files[0].SHA256 = "bad" }},
		{name: "digest", mutate: func(value *Manifest) { value.Digest = "bad" }},
		{name: "compatibility", mutate: func(value *Manifest) { value.ProductCompatibility.Minimum = "1.0.0" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := *manifest
			copy.Files = append([]File{}, manifest.Files...)
			test.mutate(&copy)
			if err := ValidateManifest(&copy); err == nil {
				t.Fatalf("invalid manifest was accepted: %+v", copy)
			}
		})
	}
}

func TestDecodeRejectsUnknownFieldsAndExtraDocuments(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"unknown":true}`),
		[]byte(`{}` + "\n{}"),
		make([]byte, MaxManifestBytes+1),
	} {
		if _, err := DecodeManifest(body); err == nil {
			t.Fatalf("invalid manifest payload was accepted")
		}
	}
}

func TestLoadRejectsDriftAndIncompatibleProducts(t *testing.T) {
	source := fstest.MapFS{"file.txt": {Data: []byte("safe")}}
	options := BuildOptions{
		Name: "advanced", Version: "1.0.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"advanced-audits"},
		TargetPrefix:         "tools/reconc/harness/template",
	}
	manifest, err := Build(source, options)
	if err != nil {
		t.Fatal(err)
	}
	body, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(body, source, options.TargetPrefix, "1.0.0"); err == nil {
		t.Fatal("incompatible product version was accepted")
	}
	drifted := fstest.MapFS{"file.txt": {Data: []byte("changed")}}
	if _, err := Load(body, drifted, options.TargetPrefix, "0.9.0"); err == nil {
		t.Fatal("drifted pack bytes were accepted")
	}
	extra := fstest.MapFS{
		"file.txt":  {Data: []byte("safe")},
		"extra.txt": {Data: []byte("unmanifested")},
	}
	if _, err := Load(body, extra, options.TargetPrefix, "0.9.0"); err == nil {
		t.Fatal("unmanifested pack file was accepted")
	}
}

func TestDecodeRequiresCanonicalDigest(t *testing.T) {
	manifest := Manifest{
		Schema: ManifestSchema, FormatVersion: FormatVersion, Kind: "harness-pack",
		Name: "advanced", Version: "1.0.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"advanced-audits"},
		Files: []File{{
			Path: "tools/reconc/harness/template/file.txt", Mode: 0o644,
			Size: 4, SHA256: sha256Hex([]byte("safe")), Ownership: "pack-file",
		}},
		TotalBytes: 4,
	}
	digest, err := Digest(&manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(body); err != nil {
		t.Fatalf("canonical manifest rejected: %v", err)
	}
}

func TestManifestIdentityAndCompatibilityBoundaries(t *testing.T) {
	valid := canonicalTestManifest(t)
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "nil", mutate: nil},
		{name: "schema", mutate: func(value *Manifest) { value.Schema = "https://example.invalid/schema" }},
		{name: "kind", mutate: func(value *Manifest) { value.Kind = "policy-pack" }},
		{name: "name", mutate: func(value *Manifest) { value.Name = "Advanced" }},
		{name: "version", mutate: func(value *Manifest) { value.Version = "0.9" }},
		{name: "minimum", mutate: func(value *Manifest) { value.ProductCompatibility.Minimum = "next" }},
		{name: "maximum", mutate: func(value *Manifest) { value.ProductCompatibility.MaximumExclusive = "0.9.0" }},
		{name: "capabilities empty", mutate: func(value *Manifest) { value.Capabilities = nil }},
		{name: "capabilities invalid", mutate: func(value *Manifest) { value.Capabilities = []string{"Advanced"} }},
		{name: "capabilities duplicate", mutate: func(value *Manifest) { value.Capabilities = []string{"advanced", "advanced"} }},
		{name: "files empty", mutate: func(value *Manifest) { value.Files = nil }},
		{name: "total", mutate: func(value *Manifest) { value.TotalBytes++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.mutate == nil {
				if err := ValidateManifest(nil); err == nil {
					t.Fatal("nil manifest was accepted")
				}
				return
			}
			value := *valid
			value.Files = append([]File{}, valid.Files...)
			test.mutate(&value)
			if err := ValidateManifest(&value); err == nil {
				t.Fatalf("invalid manifest was accepted: %+v", value)
			}
		})
	}
	if err := ValidateProductCompatibility(valid, "invalid"); err == nil {
		t.Fatal("invalid product version was accepted")
	}
}

func TestFileAndPathSecurityBoundaries(t *testing.T) {
	body := []byte("safe")
	valid := File{
		Path: "tools/reconc/file.txt", Mode: 0o644, Size: int64(len(body)),
		SHA256: sha256Hex(body), Ownership: "pack-file",
	}
	tests := []struct {
		name   string
		mutate func(*File)
	}{
		{name: "empty path", mutate: func(value *File) { value.Path = "" }},
		{name: "long path", mutate: func(value *File) { value.Path = strings.Repeat("a", 513) }},
		{name: "dot path", mutate: func(value *File) { value.Path = "." }},
		{name: "negative size", mutate: func(value *File) { value.Size = -1 }},
		{name: "ownership", mutate: func(value *File) { value.Ownership = "user" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := VerifyFile(value, body); err == nil {
				t.Fatalf("invalid file was accepted: %+v", value)
			}
		})
	}
	sizeDrift := valid
	sizeDrift.Size++
	if err := VerifyFile(sizeDrift, body); err == nil {
		t.Fatal("size drift was accepted")
	}
	checksumDrift := valid
	checksumDrift.SHA256 = strings.Repeat("0", 64)
	if err := VerifyFile(checksumDrift, body); err == nil {
		t.Fatal("checksum drift was accepted")
	}
	for _, prefix := range []string{"", ".", "../escape", "/absolute", `tools\reconc`} {
		if _, err := validateTargetPrefix(prefix); err == nil {
			t.Fatalf("invalid target prefix %q was accepted", prefix)
		}
	}
}

func TestSourceInventoryRejectsUnsupportedEntries(t *testing.T) {
	tests := []struct {
		name string
		mode fs.FileMode
	}{
		{name: "symbolic link", mode: fs.ModeSymlink},
		{name: "device", mode: fs.ModeDevice},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := fstest.MapFS{"unsafe": {Mode: test.mode}}
			if _, err := sourceInventory(source); err == nil {
				t.Fatalf("%s source entry was accepted", test.name)
			}
		})
	}
	if _, err := Build(fstest.MapFS{}, BuildOptions{
		Name: "advanced", Version: "0.9.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"advanced"},
		TargetPrefix:         "tools/reconc",
	}); err == nil {
		t.Fatal("empty harness pack was accepted")
	}
}

func TestSemanticVersionStrictness(t *testing.T) {
	for _, value := range []string{"", "0.9", "01.0.0", "1..0", "1.x.0"} {
		if _, err := parseVersion(value); err == nil {
			t.Fatalf("invalid version %q was accepted", value)
		}
	}
	for _, value := range []string{"0.9.0", "v0.9.0", "reconc-v0.9.0", "0.9.0-rc.1"} {
		if _, err := parseVersion(value); err != nil {
			t.Fatalf("valid version %q was rejected: %v", value, err)
		}
	}
	if validSHA256(strings.Repeat("A", 64)) {
		t.Fatal("uppercase checksum was accepted")
	}
}

func TestPublicLoadersFailClosed(t *testing.T) {
	if _, err := Encode(nil); err == nil {
		t.Fatal("nil manifest was encoded")
	}
	if _, err := Digest(nil); err == nil {
		t.Fatal("nil manifest was digested")
	}
	if err := ValidateProductCompatibility(nil, "0.9.0"); err == nil {
		t.Fatal("nil manifest passed compatibility validation")
	}
	if _, err := Load([]byte("{}"), fstest.MapFS{}, "tools/reconc", "0.9.0"); err == nil {
		t.Fatal("invalid manifest was loaded")
	}

	manifest := canonicalTestManifest(t)
	body, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(body, fstest.MapFS{}, "../escape", "0.9.0"); err == nil {
		t.Fatal("invalid target prefix was accepted")
	}
	if _, err := Load(
		body,
		fstest.MapFS{"file.txt": {Mode: fs.ModeSymlink}},
		"tools/reconc",
		"0.9.0",
	); err == nil {
		t.Fatal("unsafe source inventory was accepted")
	}
	if _, err := Load(
		body,
		fstest.MapFS{"other.txt": {Data: []byte("safe")}},
		"tools/reconc",
		"0.9.0",
	); err == nil {
		t.Fatal("mismatched source inventory was accepted")
	}
	if _, err := Load(
		body,
		fstest.MapFS{"file.txt": {Data: []byte("evil")}},
		"tools/reconc",
		"0.9.0",
	); err == nil {
		t.Fatal("source checksum drift was accepted")
	}

	for _, archive := range [][]byte{nil, []byte("not a zip"), zipFixture(t, nil)} {
		if _, err := LoadArchive(archive, "0.9.0"); err == nil {
			t.Fatal("invalid archive was accepted")
		}
	}
	if _, err := LoadArchive(zipFixture(t, map[string][]byte{
		"manifest.json": []byte("{}"),
		"file.txt":      []byte("safe"),
	}), "0.9.0"); err == nil {
		t.Fatal("archive with invalid manifest was accepted")
	}
	if _, err := LoadArchive(zipFixture(t, map[string][]byte{
		"manifest.json": body,
		"extra.txt":     []byte("safe"),
		"file.txt":      []byte("safe"),
	}), "0.9.0"); err == nil {
		t.Fatal("archive with extra inventory was accepted")
	}
	if _, err := LoadArchive(zipFixture(t, map[string][]byte{
		"manifest.json": body,
		"wrong.txt":     []byte("safe"),
	}), "0.9.0"); err == nil {
		t.Fatal("archive with mismatched path was accepted")
	}
	if _, err := LoadArchive(zipFixture(t, map[string][]byte{
		"manifest.json":         body,
		"tools/reconc/file.txt": []byte("evil"),
	}), "0.9.0"); err == nil {
		t.Fatal("archive with checksum drift was accepted")
	}
}

func TestBuildAndBoundedReadFailureContracts(t *testing.T) {
	options := BuildOptions{
		Name: "advanced", Version: "0.9.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"advanced"},
		TargetPrefix:         "../escape",
	}
	if _, err := Build(fstest.MapFS{"file.txt": {Data: []byte("safe")}}, options); err == nil {
		t.Fatal("build accepted invalid target prefix")
	}
	options.TargetPrefix = "tools/reconc"
	if _, err := Build(fstest.MapFS{"link": {Mode: fs.ModeSymlink}}, options); err == nil {
		t.Fatal("build accepted symbolic link")
	}
	options.ExcludedPaths = map[string]bool{"file.txt": true}
	if _, err := Build(fstest.MapFS{"file.txt": {Data: []byte("safe")}}, options); err == nil {
		t.Fatal("build accepted an empty post-exclusion pack")
	}

	source := fstest.MapFS{"file.txt": {Data: []byte("safe")}}
	if _, err := readBoundedFile(source, "file.txt", -1); err == nil {
		t.Fatal("bounded read accepted a negative size")
	}
	if _, err := readBoundedFile(source, "missing.txt", 4); err == nil {
		t.Fatal("bounded read accepted a missing file")
	}
	if _, err := readBoundedFile(source, "file.txt", 3); err == nil {
		t.Fatal("bounded read accepted size drift")
	}
}

func TestManifestRejectsAggregateOverflow(t *testing.T) {
	manifest := canonicalTestManifest(t)
	manifest.Files = nil
	manifest.TotalBytes = 5 * MaxFileBytes
	for index := 0; index < 5; index++ {
		manifest.Files = append(manifest.Files, File{
			Path:      "tools/reconc/file-" + string(rune('a'+index)),
			Mode:      0o644,
			Size:      MaxFileBytes,
			SHA256:    strings.Repeat("0", 64),
			Ownership: "pack-file",
		})
	}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("aggregate size overflow was accepted")
	}
}

func canonicalTestManifest(t *testing.T) *Manifest {
	t.Helper()
	body := []byte("safe")
	manifest := &Manifest{
		Schema: ManifestSchema, FormatVersion: FormatVersion, Kind: "harness-pack",
		Name: "advanced", Version: "0.9.0",
		ProductCompatibility: Compatibility{Minimum: "0.9.0", MaximumExclusive: "1.0.0"},
		Capabilities:         []string{"advanced"},
		Files: []File{{
			Path: "tools/reconc/file.txt", Mode: 0o644, Size: int64(len(body)),
			SHA256: sha256Hex(body), Ownership: "pack-file",
		}},
		TotalBytes: int64(len(body)),
	}
	digest, err := Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
	return manifest
}

func zipFixture(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range sortedMapKeys(entries) {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(entries[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func sortedMapKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "manifest.json" {
			continue
		}
		keys = append(keys, key)
	}
	for left := 1; left < len(keys); left++ {
		for right := left; right > 0 && keys[right] < keys[right-1]; right-- {
			keys[right], keys[right-1] = keys[right-1], keys[right]
		}
	}
	if _, ok := values["manifest.json"]; ok {
		keys = append([]string{"manifest.json"}, keys...)
	}
	return keys
}
