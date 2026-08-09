package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/boundedio"
)

type documentFile struct {
	path string
	body []byte
}

func writeDocuments(outputDir, version string, spdx, cyclonedx []byte) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create SBOM output directory: %w", err)
	}
	for _, document := range documentFiles(outputDir, version, spdx, cyclonedx) {
		if err := writeAtomic(document.path, document.body); err != nil {
			return err
		}
	}
	return nil
}

func verifyDocuments(outputDir, version string, spdx, cyclonedx []byte) error {
	for _, document := range documentFiles(outputDir, version, spdx, cyclonedx) {
		if len(document.body) == 0 {
			return fmt.Errorf("generated SBOM is empty: %s", document.path)
		}
		actual, err := boundedio.ReadRegularFile(document.path, int64(len(document.body)))
		if err != nil {
			return fmt.Errorf("read SBOM %s: %w", document.path, err)
		}
		if !bytes.Equal(actual, document.body) {
			return fmt.Errorf("SBOM is malformed, stale, or non-deterministic: %s", document.path)
		}
	}
	return nil
}

func documentFiles(outputDir, version string, spdx, cyclonedx []byte) []documentFile {
	return []documentFile{
		{path: filepath.Join(outputDir, "reconc-"+version+".spdx.json"), body: spdx},
		{path: filepath.Join(outputDir, "reconc-"+version+".cdx.json"), body: cyclonedx},
	}
}

func writeAtomic(path string, body []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".sbom-*")
	if err != nil {
		return fmt.Errorf("create temporary SBOM: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary SBOM: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary SBOM: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary SBOM: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish SBOM %s: %w", path, err)
	}
	return nil
}
