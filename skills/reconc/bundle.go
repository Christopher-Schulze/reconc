// Package reconc contains the portable Reconc agent skill embedded in the CLI.
package reconc

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed SKILL.md references/install-and-bootstrap.md references/workflow-and-evidence.md references/platform-integration.md references/completion-and-boundaries.md
var source embed.FS

const Name = "reconc"

var paths = [...]string{
	"SKILL.md",
	"references/completion-and-boundaries.md",
	"references/install-and-bootstrap.md",
	"references/platform-integration.md",
	"references/workflow-and-evidence.md",
}

type File struct {
	Path   string `json:"path"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	Data   []byte `json:"-"`
}

type Manifest struct {
	FormatVersion string `json:"format_version"`
	Name          string `json:"name"`
	Files         []File `json:"files"`
	Digest        string `json:"digest"`
}

// Files returns independent copies of the complete allowlisted skill content.
func Files() ([]File, error) {
	files := make([]File, 0, len(paths))
	for _, path := range paths {
		body, err := source.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read embedded skill %s: %w", path, err)
		}
		sum := sha256.Sum256(body)
		files = append(files, File{Path: path, Size: len(body), SHA256: hex.EncodeToString(sum[:]), Data: body})
	}
	return files, nil
}

// BuildManifest describes the embedded payload without exposing source files.
func BuildManifest() (Manifest, error) {
	files, err := Files()
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{FormatVersion: "1", Name: Name, Files: files}
	manifest.Digest, err = DigestFiles(files)
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// DigestFiles binds a sorted file inventory to the canonical skill identity.
func DigestFiles(files []File) (string, error) {
	manifest := Manifest{FormatVersion: "1", Name: Name, Files: files}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode skill manifest identity: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// EncodeManifest emits the same deterministic payload used by release assets.
func EncodeManifest() ([]byte, error) {
	manifest, err := BuildManifest()
	if err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode skill manifest: %w", err)
	}
	return append(body, '\n'), nil
}

// Archive emits a portable ZIP with only the five canonical skill files.
func Archive() ([]byte, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for _, file := range files {
		header := &zip.FileHeader{Name: Name + "/" + file.Path, Method: zip.Deflate}
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("create skill archive entry %s: %w", file.Path, err)
		}
		if _, err := entry.Write(file.Data); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("write skill archive entry %s: %w", file.Path, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close skill archive: %w", err)
	}
	return body.Bytes(), nil
}
