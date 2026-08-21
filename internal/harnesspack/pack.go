// Package harnesspack defines and verifies immutable Reconc harness packs.
package harnesspack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/schema"
)

const (
	FormatVersion    = "reconc.harness-pack/v1"
	ManifestSchema   = schema.HarnessPackManifestURL
	MaxManifestBytes = 1 << 20
	MaxFileBytes     = 4 << 20
	MaxTotalBytes    = 16 << 20
	MaxFiles         = 1024
	MaxArchiveBytes  = 32 << 20
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type Compatibility struct {
	Minimum          string `json:"minimum"`
	MaximumExclusive string `json:"maximum_exclusive"`
}

type File struct {
	Path      string `json:"path"`
	Mode      uint32 `json:"mode"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Ownership string `json:"ownership"`
}

type Manifest struct {
	Schema               string        `json:"$schema"`
	FormatVersion        string        `json:"format_version"`
	Kind                 string        `json:"kind"`
	Name                 string        `json:"name"`
	Version              string        `json:"version"`
	ProductCompatibility Compatibility `json:"product_compatibility"`
	Capabilities         []string      `json:"capabilities"`
	Files                []File        `json:"files"`
	TotalBytes           int64         `json:"total_bytes"`
	Digest               string        `json:"digest"`
}

type Data struct {
	File File
	Body []byte
}

type Pack struct {
	Manifest Manifest
	Files    []Data
}

type BuildOptions struct {
	Name                 string
	Version              string
	ProductCompatibility Compatibility
	Capabilities         []string
	TargetPrefix         string
	ExcludedPaths        map[string]bool
}

func DecodeManifest(body []byte) (*Manifest, error) {
	if len(body) == 0 || len(body) > MaxManifestBytes {
		return nil, fmt.Errorf("harness pack manifest must contain 1..%d bytes", MaxManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode harness pack manifest: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("harness pack manifest must contain exactly one JSON document")
	}
	if err := ValidateManifest(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func ValidateManifest(manifest *Manifest) error {
	if manifest == nil {
		return errors.New("harness pack manifest is nil")
	}
	if !schema.AcceptsFormat(schema.HarnessPackManifest, manifest.Schema, manifest.FormatVersion) {
		return fmt.Errorf("unsupported harness pack schema or format")
	}
	if manifest.Kind != "harness-pack" || !identifierPattern.MatchString(manifest.Name) {
		return fmt.Errorf("invalid harness pack identity")
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return fmt.Errorf("invalid harness pack version: %w", err)
	}
	minimum, err := parseVersion(manifest.ProductCompatibility.Minimum)
	if err != nil {
		return fmt.Errorf("invalid minimum product version: %w", err)
	}
	maximum, err := parseVersion(manifest.ProductCompatibility.MaximumExclusive)
	if err != nil || compareVersion(minimum, maximum) >= 0 {
		return fmt.Errorf("invalid maximum product version")
	}
	if err := validateSortedIdentifiers(manifest.Capabilities, "capabilities"); err != nil {
		return err
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > MaxFiles {
		return fmt.Errorf("harness pack must contain 1..%d files", MaxFiles)
	}
	var total int64
	for index, file := range manifest.Files {
		if err := validateFile(file); err != nil {
			return fmt.Errorf("harness pack file %d: %w", index, err)
		}
		if index > 0 && manifest.Files[index-1].Path >= file.Path {
			return errors.New("harness pack file paths must be uniquely sorted")
		}
		if total > MaxTotalBytes-file.Size {
			return fmt.Errorf("harness pack exceeds %d bytes", MaxTotalBytes)
		}
		total += file.Size
	}
	if manifest.TotalBytes != total {
		return fmt.Errorf("harness pack total_bytes is %d, want %d", manifest.TotalBytes, total)
	}
	digest, err := Digest(manifest)
	if err != nil {
		return err
	}
	if manifest.Digest != digest {
		return fmt.Errorf("harness pack digest mismatch")
	}
	return nil
}

func ValidateProductCompatibility(manifest *Manifest, productVersion string) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	current, err := parseVersion(productVersion)
	if err != nil {
		return fmt.Errorf("invalid Reconc product version %q: %w", productVersion, err)
	}
	minimum, _ := parseVersion(manifest.ProductCompatibility.Minimum)
	maximum, _ := parseVersion(manifest.ProductCompatibility.MaximumExclusive)
	if compareVersion(current, minimum) < 0 || compareVersion(current, maximum) >= 0 {
		return fmt.Errorf(
			"harness pack %s@%s supports Reconc >=%s and <%s, not %s",
			manifest.Name,
			manifest.Version,
			manifest.ProductCompatibility.Minimum,
			manifest.ProductCompatibility.MaximumExclusive,
			productVersion,
		)
	}
	return nil
}

func Load(manifestBody []byte, source fs.FS, targetPrefix, productVersion string) (*Pack, error) {
	manifest, err := DecodeManifest(manifestBody)
	if err != nil {
		return nil, err
	}
	if err := ValidateProductCompatibility(manifest, productVersion); err != nil {
		return nil, err
	}
	prefix, err := validateTargetPrefix(targetPrefix)
	if err != nil {
		return nil, err
	}
	inventory, err := sourceInventory(source)
	if err != nil {
		return nil, err
	}
	if len(inventory) != len(manifest.Files) {
		return nil, fmt.Errorf("harness pack source contains %d files, manifest contains %d", len(inventory), len(manifest.Files))
	}
	files := make([]Data, 0, len(manifest.Files))
	var total int64
	for index, file := range manifest.Files {
		relative := strings.TrimPrefix(file.Path, prefix+"/")
		if relative == file.Path || inventory[index] != relative {
			return nil, fmt.Errorf("harness pack source inventory mismatch at %s", file.Path)
		}
		if file.Size > MaxTotalBytes-total {
			return nil, fmt.Errorf("harness pack exceeds %d bytes before reading %s", MaxTotalBytes, file.Path)
		}
		body, err := readBoundedFileWithLimit(source, relative, file.Size, MaxTotalBytes-total)
		if err != nil {
			return nil, fmt.Errorf("read harness pack file %s: %w", file.Path, err)
		}
		if err := VerifyFile(file, body); err != nil {
			return nil, err
		}
		total += int64(len(body))
		files = append(files, Data{File: file, Body: body})
	}
	if total != manifest.TotalBytes {
		return nil, fmt.Errorf("harness pack actual bytes are %d, manifest contains %d", total, manifest.TotalBytes)
	}
	return &Pack{Manifest: *manifest, Files: files}, nil
}

func LoadArchive(body []byte, productVersion string) (*Pack, error) {
	if len(body) == 0 || len(body) > MaxArchiveBytes {
		return nil, fmt.Errorf("harness pack archive must contain 1..%d bytes", MaxArchiveBytes)
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("open harness pack archive: %w", err)
	}
	if len(reader.File) < 2 || len(reader.File) > MaxFiles+1 ||
		reader.File[0].Name != "manifest.json" {
		return nil, errors.New("harness pack archive inventory is invalid")
	}
	if err := validateArchiveInventory(reader.File); err != nil {
		return nil, err
	}
	manifestBody, err := readArchiveFile(reader.File[0], MaxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read harness pack manifest: %w", err)
	}
	manifest, err := DecodeManifest(manifestBody)
	if err != nil {
		return nil, err
	}
	if err := ValidateProductCompatibility(manifest, productVersion); err != nil {
		return nil, err
	}
	if len(reader.File) != len(manifest.Files)+1 {
		return nil, fmt.Errorf("harness pack archive contains %d files, manifest contains %d", len(reader.File)-1, len(manifest.Files))
	}
	files := make([]Data, 0, len(manifest.Files))
	var total int64
	for index, file := range manifest.Files {
		entry := reader.File[index+1]
		if entry.Name != file.Path || entry.FileInfo().Mode().Perm() != fs.FileMode(file.Mode) ||
			entry.FileInfo().Mode()&fs.ModeType != 0 {
			return nil, fmt.Errorf("harness pack archive entry mismatch at %s", file.Path)
		}
		remaining := MaxTotalBytes - total
		if file.Size > remaining {
			return nil, fmt.Errorf("harness pack exceeds %d bytes before reading %s", MaxTotalBytes, file.Path)
		}
		content, err := readArchiveFile(entry, minInt64(MaxFileBytes, remaining))
		if err != nil {
			return nil, fmt.Errorf("read harness pack archive entry %s: %w", file.Path, err)
		}
		if err := VerifyFile(file, content); err != nil {
			return nil, err
		}
		total += int64(len(content))
		files = append(files, Data{File: file, Body: content})
	}
	if total != manifest.TotalBytes {
		return nil, fmt.Errorf("harness pack archive actual bytes are %d, manifest contains %d", total, manifest.TotalBytes)
	}
	return &Pack{Manifest: *manifest, Files: files}, nil
}

func Build(source fs.FS, options BuildOptions) (*Manifest, error) {
	prefix, err := validateTargetPrefix(options.TargetPrefix)
	if err != nil {
		return nil, err
	}
	inventory, err := sourceInventory(source)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(inventory))
	for _, relative := range inventory {
		if !options.ExcludedPaths[relative] {
			selected = append(selected, relative)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("harness pack must contain at least one non-excluded file")
	}
	if len(selected) > MaxFiles {
		return nil, fmt.Errorf("harness pack contains %d files; maximum is %d", len(selected), MaxFiles)
	}
	files := make([]File, 0, len(selected))
	var total int64
	for _, relative := range selected {
		info, err := fs.Stat(source, relative)
		if err != nil {
			return nil, fmt.Errorf("stat harness pack source %s: %w", relative, err)
		}
		mode := uint32(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if info.Size() < 0 || info.Size() > MaxFileBytes || info.Size() > MaxTotalBytes-total {
			return nil, fmt.Errorf("harness pack source file %s does not fit the remaining byte budget", relative)
		}
		body, err := readBoundedFileWithLimit(source, relative, info.Size(), MaxTotalBytes-total)
		if err != nil {
			return nil, fmt.Errorf("read harness pack source %s: %w", relative, err)
		}
		if int64(len(body)) > MaxTotalBytes-total {
			return nil, fmt.Errorf("harness pack exceeds %d bytes after reading %s", MaxTotalBytes, relative)
		}
		total += int64(len(body))
		files = append(files, File{
			Path: prefix + "/" + relative, Mode: mode, Size: int64(len(body)),
			SHA256: sha256Hex(body), Ownership: "pack-file",
		})
	}
	capabilities := append([]string{}, options.Capabilities...)
	sort.Strings(capabilities)
	manifest := &Manifest{
		Schema: schema.Resolve(schema.HarnessPackManifest), FormatVersion: FormatVersion, Kind: "harness-pack",
		Name: options.Name, Version: options.Version,
		ProductCompatibility: options.ProductCompatibility,
		Capabilities:         capabilities, Files: files, TotalBytes: total,
	}
	digest, err := Digest(manifest)
	if err != nil {
		return nil, err
	}
	manifest.Digest = digest
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func Encode(manifest *Manifest) ([]byte, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode harness pack manifest: %w", err)
	}
	return append(body, '\n'), nil
}

func Digest(manifest *Manifest) (string, error) {
	if manifest == nil {
		return "", errors.New("harness pack manifest is nil")
	}
	copy := *manifest
	copy.Digest = ""
	body, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("encode harness pack digest: %w", err)
	}
	return sha256Hex(body), nil
}

func VerifyFile(file File, body []byte) error {
	if err := validateFile(file); err != nil {
		return err
	}
	if int64(len(body)) != file.Size {
		return fmt.Errorf("harness pack file size mismatch: %s", file.Path)
	}
	if sha256Hex(body) != file.SHA256 {
		return fmt.Errorf("harness pack file checksum mismatch: %s", file.Path)
	}
	return nil
}

func validateFile(file File) error {
	if file.Path == "" || len(file.Path) > 512 || strings.Contains(file.Path, `\`) ||
		path.IsAbs(file.Path) || path.Clean(file.Path) != file.Path ||
		file.Path == "." || file.Path == ".." || strings.HasPrefix(file.Path, "../") {
		return fmt.Errorf("path is not canonical repository-relative: %q", file.Path)
	}
	if file.Mode != 0o644 && file.Mode != 0o755 {
		return fmt.Errorf("unsupported mode %04o", file.Mode)
	}
	if file.Size < 0 || file.Size > MaxFileBytes {
		return fmt.Errorf("size must be 0..%d bytes", MaxFileBytes)
	}
	if !validSHA256(file.SHA256) || file.Ownership != "pack-file" {
		return errors.New("checksum or ownership is invalid")
	}
	return nil
}

func validateSortedIdentifiers(values []string, field string) error {
	if len(values) == 0 {
		return fmt.Errorf("harness pack %s must not be empty", field)
	}
	for index, value := range values {
		if !identifierPattern.MatchString(value) {
			return fmt.Errorf("invalid harness pack %s value %q", field, value)
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("harness pack %s must be uniquely sorted", field)
		}
	}
	return nil
}

func validateTargetPrefix(value string) (string, error) {
	prefix := strings.TrimSuffix(strings.TrimSpace(value), "/")
	if prefix == "" || strings.Contains(prefix, `\`) || path.IsAbs(prefix) ||
		path.Clean(prefix) != prefix || prefix == "." || prefix == ".." || strings.HasPrefix(prefix, "../") {
		return "", fmt.Errorf("invalid harness pack target prefix %q", value)
	}
	return prefix, nil
}

func sourceInventory(source fs.FS) ([]string, error) {
	paths := []string{}
	err := fs.WalkDir(source, ".", func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == "." {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("harness pack source contains symbolic link %s", current)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("harness pack source contains irregular file %s", current)
		}
		if path.Clean(current) != current || strings.Contains(current, `\`) {
			return fmt.Errorf("harness pack source path is not canonical: %s", current)
		}
		if len(paths) >= MaxFiles {
			return fmt.Errorf("harness pack source exceeds %d files", MaxFiles)
		}
		paths = append(paths, current)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func readBoundedFile(source fs.FS, name string, expectedSize int64) ([]byte, error) {
	return readBoundedFileWithLimit(source, name, expectedSize, MaxFileBytes)
}

func readBoundedFileWithLimit(source fs.FS, name string, expectedSize, maximum int64) ([]byte, error) {
	if expectedSize < 0 || expectedSize > MaxFileBytes {
		return nil, fmt.Errorf("file size must be 0..%d bytes", MaxFileBytes)
	}
	if maximum < 0 || expectedSize > maximum {
		return nil, fmt.Errorf("file size exceeds remaining %d-byte budget", maximum)
	}
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum || int64(len(body)) != expectedSize {
		return nil, fmt.Errorf("file size changed: got %d, want %d", len(body), expectedSize)
	}
	return body, nil
}

func readArchiveFile(file *zip.File, maximum int64) ([]byte, error) {
	if file == nil || maximum < 0 || file.UncompressedSize64 > uint64(maximum) {
		return nil, fmt.Errorf("archive entry exceeds %d bytes", maximum)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maximum+1))
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum || uint64(len(body)) != file.UncompressedSize64 {
		return nil, errors.New("archive entry size is invalid")
	}
	return body, nil
}

func validateArchiveInventory(entries []*zip.File) error {
	seen := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		if entry == nil || entry.Name == "" {
			return fmt.Errorf("harness pack archive entry %d is invalid", index)
		}
		if _, duplicate := seen[entry.Name]; duplicate {
			return fmt.Errorf("harness pack archive contains duplicate entry %s", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		if entry.Name == "manifest.json" && index != 0 {
			return errors.New("harness pack archive manifest must be the first entry")
		}
		if index > 0 {
			if err := validateArchivePath(entry.Name); err != nil {
				return err
			}
		}
		mode := entry.FileInfo().Mode()
		if !mode.IsRegular() || mode&fs.ModeType != 0 {
			return fmt.Errorf("harness pack archive entry %s is not a regular file", entry.Name)
		}
	}
	return nil
}

func validateArchivePath(value string) error {
	if value == "manifest.json" || value == "" || strings.Contains(value, `\`) ||
		path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." ||
		strings.HasPrefix(value, "../") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("harness pack archive entry path is not canonical: %q", value)
	}
	return nil
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

type semanticVersion struct {
	major int
	minor int
	patch int
}

func parseVersion(value string) (semanticVersion, error) {
	clean := strings.TrimSpace(value)
	clean = strings.TrimPrefix(clean, "reconc-v")
	clean = strings.TrimPrefix(clean, "v")
	if prerelease := strings.IndexAny(clean, "-+"); prerelease >= 0 {
		clean = clean[:prerelease]
	}
	parts := strings.Split(clean, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("version must be MAJOR.MINOR.PATCH")
	}
	numbers := [3]int{}
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, fmt.Errorf("version component %q is invalid", part)
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return semanticVersion{}, fmt.Errorf("version component %q is invalid", part)
		}
		numbers[index] = number
	}
	return semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}, nil
}

func compareVersion(left, right semanticVersion) int {
	leftParts := [3]int{left.major, left.minor, left.patch}
	rightParts := [3]int{right.major, right.minor, right.patch}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1
		}
		if leftParts[index] > rightParts[index] {
			return 1
		}
	}
	return 0
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
