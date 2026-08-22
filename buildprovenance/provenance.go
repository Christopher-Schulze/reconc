// Package buildprovenance computes and inspects deterministic Reconc build
// provenance without executing the binary under inspection.
package buildprovenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/build"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	sourceDigestDomain = "reconc-production-source-v1"
	// MarkerPrefix identifies the stable, machine-readable binary marker.
	MarkerPrefix                = "reconc-build-provenance-v1"
	maxBinaryBytes              = 256 << 20
	maxBuildMarkerBytes         = 1024
	maxModuleFileBytes          = 1 << 20
	maxProductionFileBytes      = 64 << 20
	maxProductionAggregate      = 512 << 20
	maxProductionFiles          = 16_384
	maxEmbeddedDirectoryEntries = 100_000
)

var (
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)
	targetPattern  = regexp.MustCompile(`^[a-z0-9]+$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	markerPattern  = regexp.MustCompile(
		`reconc-build-provenance-v1\|version=([0-9A-Za-z][0-9A-Za-z.+-]*)\|goos=([a-z0-9]+)\|goarch=([a-z0-9]+)\|source=([0-9a-f]{64})\|end`,
	)
)

// BuildMarker is replaced through -ldflags -X for release and host builds.
var BuildMarker = MarkerPrefix + "|unavailable|end"

// Provenance is the source-bound identity embedded in a Reconc binary.
type Provenance struct {
	Version      string
	GOOS         string
	GOARCH       string
	SourceDigest string
}

// ComputeSourceDigest hashes the exact local production inputs selected for
// cmd/reconc on the requested CGO-disabled target.
func ComputeSourceDigest(moduleRoot string, goos string, goarch string) (string, error) {
	if !targetPattern.MatchString(goos) || !targetPattern.MatchString(goarch) {
		return "", fmt.Errorf("invalid build target %q/%q", goos, goarch)
	}
	root, err := filepath.Abs(moduleRoot)
	if err != nil {
		return "", fmt.Errorf("resolve module root: %w", err)
	}
	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	files, err := productionFiles(root, modulePath, goos, goarch)
	if err != nil {
		return "", err
	}
	return digestFiles(root, files, goos, goarch)
}

// FormatMarker validates and serializes provenance for linker injection.
func FormatMarker(provenance Provenance) (string, error) {
	if !versionPattern.MatchString(provenance.Version) {
		return "", fmt.Errorf("invalid version %q", provenance.Version)
	}
	if !targetPattern.MatchString(provenance.GOOS) || !targetPattern.MatchString(provenance.GOARCH) {
		return "", fmt.Errorf("invalid build target %q/%q", provenance.GOOS, provenance.GOARCH)
	}
	if !digestPattern.MatchString(provenance.SourceDigest) {
		return "", fmt.Errorf("invalid source digest %q", provenance.SourceDigest)
	}
	marker := fmt.Sprintf(
		"%s|version=%s|goos=%s|goarch=%s|source=%s|end",
		MarkerPrefix,
		provenance.Version,
		provenance.GOOS,
		provenance.GOARCH,
		provenance.SourceDigest,
	)
	if len(marker) > maxBuildMarkerBytes {
		return "", fmt.Errorf("build provenance marker exceeds %d bytes", maxBuildMarkerBytes)
	}
	return marker, nil
}

// ParseMarker validates a serialized provenance marker.
func ParseMarker(marker string) (Provenance, error) {
	if len(marker) > maxBuildMarkerBytes {
		return Provenance{}, fmt.Errorf("build provenance marker exceeds %d bytes", maxBuildMarkerBytes)
	}
	match := markerPattern.FindStringSubmatch(marker)
	if len(match) != 5 || match[0] != marker {
		return Provenance{}, fmt.Errorf("missing or malformed Reconc build provenance")
	}
	return Provenance{
		Version:      match[1],
		GOOS:         match[2],
		GOARCH:       match[3],
		SourceDigest: match[4],
	}, nil
}

// EmbeddedProvenance returns the provenance linked into the running binary.
func EmbeddedProvenance() (Provenance, error) {
	return ParseMarker(BuildMarker)
}

// InspectBinary reads provenance directly from binary bytes. It never executes
// the inspected file.
func InspectBinary(binaryPath string) (Provenance, error) {
	var provenance Provenance
	err := boundedio.WithRegularFileSnapshot(binaryPath, maxBinaryBytes, func(file *os.File, _ os.FileInfo) error {
		var inspectErr error
		provenance, inspectErr = InspectOpenFile(file)
		return inspectErr
	})
	if err != nil {
		return Provenance{}, fmt.Errorf("read Reconc binary: %w", err)
	}
	return provenance, nil
}

// InspectOpenFile reads provenance from an already-open regular file without
// resolving a mutable path. It restores the caller's file offset before
// returning and rejects metadata changes observed during the scan.
func InspectOpenFile(file *os.File) (provenance Provenance, err error) {
	if file == nil {
		return Provenance{}, fmt.Errorf("open Reconc binary is nil")
	}
	before, err := file.Stat()
	if err != nil {
		return Provenance{}, err
	}
	if !before.Mode().IsRegular() {
		return Provenance{}, fmt.Errorf("open Reconc binary is not a regular file")
	}
	if before.Size() > maxBinaryBytes {
		return Provenance{}, fmt.Errorf("binary exceeds %d bytes", maxBinaryBytes)
	}
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return Provenance{}, fmt.Errorf("read open Reconc binary offset: %w", err)
	}
	defer func() {
		_, restoreErr := file.Seek(offset, io.SeekStart)
		err = errors.Join(err, restoreErr)
	}()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Provenance{}, fmt.Errorf("rewind open Reconc binary: %w", err)
	}
	marker, err := scanBinaryMarker(file)
	if err != nil {
		return Provenance{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return Provenance{}, err
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return Provenance{}, fmt.Errorf("open Reconc binary changed while reading provenance")
	}
	if marker == "" {
		return Provenance{}, fmt.Errorf("missing or malformed embedded build provenance")
	}
	return ParseMarker(marker)
}

func scanBinaryMarker(file *os.File) (string, error) {
	chunk := make([]byte, 64<<10)
	tail := []byte{}
	seen := map[int64]bool{}
	marker := ""
	var consumed int64
	reader := io.LimitReader(file, maxBinaryBytes+1)
	for {
		n, readErr := reader.Read(chunk)
		if n > 0 {
			window := make([]byte, 0, len(tail)+n)
			window = append(window, tail...)
			window = append(window, chunk[:n]...)
			base := consumed - int64(len(tail))
			for _, match := range markerPattern.FindAllIndex(window, -1) {
				position := base + int64(match[0])
				if seen[position] {
					continue
				}
				seen[position] = true
				if marker != "" {
					return "", fmt.Errorf("ambiguous embedded build provenance")
				}
				marker = string(window[match[0]:match[1]])
			}
			keep := maxBuildMarkerBytes - 1
			if len(window) < keep {
				keep = len(window)
			}
			tail = append(tail[:0], window[len(window)-keep:]...)
			consumed += int64(n)
			if consumed > maxBinaryBytes {
				return "", fmt.Errorf("binary exceeds %d bytes", maxBinaryBytes)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return marker, nil
			}
			return "", readErr
		}
	}
}

func productionFiles(root string, modulePath string, goos string, goarch string) ([]string, error) {
	context := build.Default
	context.GOOS = goos
	context.GOARCH = goarch
	context.CgoEnabled = false
	pending := []string{"cmd/reconc"}
	visited := make(map[string]bool)
	files := map[string]struct{}{"go.mod": {}}
	if err := addOptionalFile(root, "go.sum", files); err != nil {
		return nil, err
	}
	for len(pending) > 0 {
		relative := pending[0]
		pending = pending[1:]
		if visited[relative] {
			continue
		}
		visited[relative] = true
		imports, err := addPackageFiles(&context, root, relative, files)
		if err != nil {
			return nil, err
		}
		pending = append(pending, localImports(imports, modulePath)...)
	}
	return sortedKeys(files), nil
}

func addPackageFiles(context *build.Context, root string, relative string, files map[string]struct{}) ([]string, error) {
	directory := filepath.Join(root, filepath.FromSlash(relative))
	pkg, err := context.ImportDir(directory, 0)
	if err != nil {
		return nil, fmt.Errorf("load production package %s: %w", relative, err)
	}
	for _, name := range packageSourceFiles(pkg) {
		files[path.Join(filepath.ToSlash(relative), name)] = struct{}{}
	}
	embedded, err := resolveEmbeddedFiles(directory, pkg.EmbedPatterns)
	if err != nil {
		return nil, fmt.Errorf("resolve embedded inputs for %s: %w", relative, err)
	}
	for _, name := range embedded {
		files[path.Join(filepath.ToSlash(relative), name)] = struct{}{}
	}
	return pkg.Imports, nil
}

func packageSourceFiles(pkg *build.Package) []string {
	var files []string
	for _, group := range [][]string{
		pkg.GoFiles,
		pkg.CgoFiles,
		pkg.CFiles,
		pkg.CXXFiles,
		pkg.MFiles,
		pkg.HFiles,
		pkg.FFiles,
		pkg.SFiles,
		pkg.SwigFiles,
		pkg.SwigCXXFiles,
		pkg.SysoFiles,
	} {
		files = append(files, group...)
	}
	return files
}

func localImports(imports []string, modulePath string) []string {
	var local []string
	prefix := modulePath + "/"
	for _, importPath := range imports {
		if importPath == modulePath {
			local = append(local, ".")
			continue
		}
		if strings.HasPrefix(importPath, prefix) {
			local = append(local, strings.TrimPrefix(importPath, prefix))
		}
	}
	sort.Strings(local)
	return local
}

func resolveEmbeddedFiles(directory string, patterns []string) ([]string, error) {
	files := make(map[string]struct{})
	for _, pattern := range patterns {
		matches, err := matchEmbeddedFiles(directory, pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			files[match] = struct{}{}
		}
	}
	return sortedKeys(files), nil
}

func matchEmbeddedFiles(directory string, pattern string) ([]string, error) {
	originalPattern := pattern
	includeHidden := strings.HasPrefix(pattern, "all:")
	if includeHidden {
		pattern = strings.TrimPrefix(pattern, "all:")
	}
	if !validEmbedPattern(pattern) {
		return nil, fmt.Errorf("invalid go:embed pattern %q", originalPattern)
	}
	var matches []string
	visited := 0
	err := filepath.WalkDir(directory, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxEmbeddedDirectoryEntries {
			return fmt.Errorf("embedded input tree exceeds %d entries", maxEmbeddedDirectoryEntries)
		}
		relative, err := filepath.Rel(directory, filePath)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if !includeHidden && hasHiddenPathElement(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		matched, err := matchesEmbedPattern(pattern, relative)
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("go:embed pattern %q matched irregular file %s", pattern, relative)
		}
		matches = append(matches, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("go:embed pattern %q matched no files", pattern)
	}
	sort.Strings(matches)
	return matches, nil
}

func validEmbedPattern(pattern string) bool {
	if pattern == "" || pattern == "." || strings.HasPrefix(pattern, "/") {
		return false
	}
	for _, element := range strings.Split(pattern, "/") {
		if element == "" || element == "." || element == ".." {
			return false
		}
	}
	return true
}

func matchesEmbedPattern(pattern string, relative string) (bool, error) {
	matched, err := path.Match(pattern, relative)
	if err != nil || matched {
		return matched, err
	}
	parent := path.Dir(relative)
	for parent != "." {
		matched, err = path.Match(pattern, parent)
		if err != nil || matched {
			return matched, err
		}
		parent = path.Dir(parent)
	}
	return false, nil
}

func hasHiddenPathElement(relative string) bool {
	for _, element := range strings.Split(relative, "/") {
		if strings.HasPrefix(element, ".") || strings.HasPrefix(element, "_") {
			return true
		}
	}
	return false
}

func readModulePath(goModPath string) (string, error) {
	content, err := boundedio.ReadRegularFile(goModPath, maxModuleFileBytes)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("go.mod has no module directive")
}

func addOptionalFile(root string, relative string, files map[string]struct{}) error {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", relative, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a non-symlink regular file", relative)
	}
	files[relative] = struct{}{}
	return nil
}

func digestFiles(root string, files []string, goos string, goarch string) (string, error) {
	if len(files) > maxProductionFiles {
		return "", fmt.Errorf("production input set exceeds %d files", maxProductionFiles)
	}
	digest := sha256.New()
	writeDigestRecord(digest, "domain", []byte(sourceDigestDomain))
	writeDigestRecord(digest, "goos", []byte(goos))
	writeDigestRecord(digest, "goarch", []byte(goarch))
	var aggregate int64
	for _, relative := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			if err == nil {
				err = fmt.Errorf("not a non-symlink regular file")
			}
			return "", fmt.Errorf("inspect production input %s: %w", relative, err)
		}
		if info.Size() > maxProductionFileBytes {
			return "", fmt.Errorf("production input %s exceeds %d bytes", relative, maxProductionFileBytes)
		}
		aggregate += info.Size()
		if aggregate > maxProductionAggregate {
			return "", fmt.Errorf("production input set exceeds %d bytes", maxProductionAggregate)
		}
		if err := writeFileDigestRecord(digest, filepath.ToSlash(relative), full, info.Size()); err != nil {
			return "", fmt.Errorf("read production input %s: %w", relative, err)
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeFileDigestRecord(digest hash.Hash, name, filePath string, size int64) error {
	fmt.Fprintf(digest, "%d:%s:%d:", len(name), name, size)
	return boundedio.WithRegularFileSnapshot(filePath, maxProductionFileBytes, func(file *os.File, info os.FileInfo) error {
		if info.Size() != size {
			return fmt.Errorf("file size changed before hashing: expected %d, found %d", size, info.Size())
		}
		written, err := io.Copy(digest, io.LimitReader(file, size+1))
		if err != nil {
			return err
		}
		if written != size {
			return fmt.Errorf("file size changed while hashing: expected %d, read %d", size, written)
		}
		return nil
	})
}

func writeDigestRecord(digest hash.Hash, name string, content []byte) {
	fmt.Fprintf(digest, "%d:%s:%d:", len(name), name, len(content))
	_, _ = digest.Write(content)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
