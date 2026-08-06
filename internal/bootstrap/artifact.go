package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const maxBinaryBytes int64 = 256 << 20
const maxBootstrapDirectoryEntries = 4096

func CurrentBinarySelection() (*BinarySelection, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve running reconc executable: %w", err)
	}
	resolved, err := pathidentity.ResolveExisting(path)
	if err != nil {
		return nil, fmt.Errorf("resolve running reconc executable identity: %w", err)
	}
	return BinarySelectionFor(resolved, "", runtime.GOOS, runtime.GOARCH)
}

func BinarySelectionFor(path, expectedSHA, targetOS, targetArch string) (*BinarySelection, error) {
	if err := validatePlatform(targetOS, targetArch); err != nil {
		return nil, err
	}
	abs, err := pathidentity.ResolveExisting(path)
	if err != nil {
		return nil, fmt.Errorf("resolve binary artifact identity: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat binary artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("binary artifact is not a regular file: %s", abs)
	}
	if info.Size() > maxBinaryBytes {
		return nil, fmt.Errorf("binary artifact exceeds %d bytes: %s", maxBinaryBytes, abs)
	}
	digest, err := fileSHA256(abs)
	if err != nil {
		return nil, err
	}
	expected := strings.ToLower(strings.TrimSpace(expectedSHA))
	if expected != "" && (len(expected) != 64 || expected != digest) {
		return nil, fmt.Errorf("binary checksum mismatch: expected %s, got %s", expected, digest)
	}
	return &BinarySelection{SourcePath: abs, SHA256: digest, OS: targetOS, Arch: targetArch}, nil
}

func StableBinaryName(targetOS, targetArch string) (string, error) {
	if err := validatePlatform(targetOS, targetArch); err != nil {
		return "", err
	}
	extension := ""
	if targetOS == "windows" {
		extension = ".exe"
	}
	return "reconc-" + targetOS + "-" + targetArch + extension, nil
}

func ResolveRepoBinary(repoRoot, targetOS, targetArch string) ArtifactResolution {
	resolution := ArtifactResolution{OS: targetOS, Arch: targetArch, Candidates: []string{}}
	stable, err := StableBinaryName(targetOS, targetArch)
	if err != nil {
		resolution.Diagnostic = err.Error()
		return resolution
	}
	resolution.StableName = stable
	for _, directory := range []struct {
		path   string
		source string
	}{
		{filepath.Join(repoRoot, "tools", "reconc", "dist"), "tools-reconc-dist"},
		{filepath.Join(repoRoot, "dist"), "root-dist"},
	} {
		path, candidates, diagnostic := resolveBinaryDirectory(directory.path, stable, targetOS, targetArch)
		resolution.Candidates = append(resolution.Candidates, candidates...)
		if diagnostic != "" {
			resolution.Diagnostic = diagnostic
			return resolution
		}
		if path != "" {
			resolution.Path = path
			resolution.Source = directory.source
			return resolution
		}
	}
	if targetOS == runtime.GOOS && targetArch == runtime.GOARCH {
		for _, candidate := range []struct {
			path   string
			source string
		}{
			{filepath.Join(repoRoot, ".build", "bin", "reconc"), "development-build"},
			{filepath.Join(repoRoot, "reconc"), "repository-binary"},
		} {
			if executableRegular(candidate.path, targetOS) {
				resolution.Candidates = append(resolution.Candidates, candidate.path)
				resolution.Path = candidate.path
				resolution.Source = candidate.source
				return resolution
			}
		}
	}
	resolution.Diagnostic = "no compatible repo-local Reconc binary found; install the stable " + stable + " artifact or keep exactly one compatible versioned artifact"
	return resolution
}

func resolveBinaryDirectory(directory, stable, targetOS, targetArch string) (string, []string, string) {
	stablePath := filepath.Join(directory, stable)
	if executableRegular(stablePath, targetOS) {
		return stablePath, []string{stablePath}, ""
	}
	entries, err := boundedio.ReadDirNoSymlink(directory, maxBootstrapDirectoryEntries)
	if os.IsNotExist(err) {
		return "", []string{}, ""
	}
	if err != nil {
		return "", []string{}, "read binary directory " + directory + ": " + err.Error()
	}
	extension := ""
	if targetOS == "windows" {
		extension = ".exe"
	}
	prefix := "reconc-"
	suffix := "-" + targetOS + "-" + targetArch + extension
	matches := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		version := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if strings.TrimSpace(version) == "" {
			continue
		}
		path := filepath.Join(directory, name)
		if executableRegular(path, targetOS) {
			matches = append(matches, path)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", matches, ""
	case 1:
		return matches[0], matches, ""
	default:
		return "", matches, fmt.Sprintf("binary resolution is ambiguous under %s for %s/%s: %s", directory, targetOS, targetArch, strings.Join(matches, ", "))
	}
}

func validatePlatform(targetOS, targetArch string) error {
	supported := map[string]map[string]bool{
		"darwin":  {"amd64": true, "arm64": true},
		"linux":   {"amd64": true, "arm64": true},
		"windows": {"amd64": true},
	}
	architectures, ok := supported[targetOS]
	if !ok || !architectures[targetArch] {
		return fmt.Errorf("unsupported Reconc artifact platform: %s/%s", targetOS, targetArch)
	}
	return nil
}

func executableRegular(path, targetOS string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return targetOS == "windows" || info.Mode()&0o111 != 0
}

func fileSHA256(path string) (string, error) {
	file, err := boundedio.OpenRegularFile(path, maxBinaryBytes)
	if err != nil {
		return "", fmt.Errorf("open %s for checksum: %w", path, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, maxBinaryBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("hash %s: %w", path, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close %s after checksum: %w", path, closeErr)
	}
	if written > maxBinaryBytes {
		return "", fmt.Errorf("artifact exceeds %d-byte checksum limit: %s", maxBinaryBytes, path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func bytesSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
