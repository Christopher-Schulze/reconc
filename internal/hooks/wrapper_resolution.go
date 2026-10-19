package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/boundedio"
)

// WrapperBinaryStatus describes the current wrapper's actual resolver order.
// It reads build provenance without executing any candidate.
type WrapperBinaryStatus struct {
	Path         string `json:"path,omitempty"`
	Source       string `json:"source,omitempty"`
	Version      string `json:"version,omitempty"`
	SourceDigest string `json:"source_digest,omitempty"`
	Error        string `json:"error,omitempty"`
	// unrunnable marks a wrapper that resolves no binary or one built for
	// another platform; unreadable provenance alone leaves a binary runnable,
	// because builds without release flags carry none.
	unrunnable bool
}

// inspectStatusWrapperResolution resolves the wrapper's binary without
// reading it. The single-platform readiness probe runs on the hook execution
// path, so it reports an unresolvable binary but leaves the binary's target
// platform to full status.
func inspectStatusWrapperResolution(root string) statusWrapperInspection {
	inspection := inspectStatusWrapper(root)
	if inspection.current && inspection.executable {
		path, source, err := resolveWrapperBinary(root)
		binary := WrapperBinaryStatus{Path: path, Source: source}
		if err != nil {
			binary.Error, binary.unrunnable = err.Error(), true
		}
		inspection.binary = &binary
	}
	return inspection
}

// Full status scans the selected binary once.
func inspectStatusWrapperWithBinary(root string) statusWrapperInspection {
	inspection := inspectStatusWrapper(root)
	if inspection.current && inspection.executable {
		binary := inspectWrapperBinary(root)
		inspection.binary = &binary
	}
	return inspection
}

func inspectWrapperBinary(root string) WrapperBinaryStatus {
	path, source, err := resolveWrapperBinary(root)
	status := WrapperBinaryStatus{Path: path, Source: source}
	if err != nil {
		status.Error, status.unrunnable = err.Error(), true
		return status
	}
	provenance, err := buildprovenance.InspectBinary(path)
	if err != nil {
		status.Error = "selected binary provenance is unavailable: " + err.Error()
		return status
	}
	status.Version, status.SourceDigest = provenance.Version, provenance.SourceDigest
	if provenance.GOOS != runtime.GOOS || provenance.GOARCH != runtime.GOARCH {
		status.Error = "selected binary targets " + provenance.GOOS + "/" + provenance.GOARCH + "; current host is " + runtime.GOOS + "/" + runtime.GOARCH
		status.unrunnable = true
	}
	return status
}

func resolveWrapperBinary(root string) (string, string, error) {
	for _, relative := range []string{"tools/reconc/.build/bin/reconc", ".build/bin/reconc", "reconc"} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if wrapperCandidateTrusted(path) {
			return path, "development", nil
		}
	}
	// Like the wrapper, honor only a pin that names this host's binary.
	if host, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH); err == nil {
		if snapshot, err := readManagedArtifactSnapshot(filepath.Join(root, filepath.FromSlash(WrapperTargetPath))); err == nil &&
			snapshot.exists && string(snapshot.body) == host.Content {
			path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(string(snapshot.body), "\n")))
			if wrapperCandidateTrusted(path) {
				return path, "direct-target", nil
			}
		}
	}
	for _, relative := range []string{"tools/reconc/dist", "dist"} {
		path, err := resolveWrapperDirectory(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", "", err
		}
		if path != "" {
			return path, "repository", nil
		}
	}
	if path, err := exec.LookPath("reconc"); err == nil && wrapperCandidateTrusted(path) {
		return path, "PATH", nil
	}
	return "", "", fmt.Errorf("no executable Reconc binary found by the wrapper resolver")
}

func resolveWrapperDirectory(directory string) (string, error) {
	name, supported := stableWrapperTargetName(runtime.GOOS, runtime.GOARCH)
	if !supported {
		return "", nil
	}
	stable := filepath.Join(directory, name)
	if wrapperCandidateTrusted(stable) {
		return stable, nil
	}
	entries, err := boundedio.ReadDir(directory, maxWrapperResolutionEntries)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect wrapper binary directory: %w", err)
	}
	suffix := strings.TrimPrefix(name, "reconc")
	selected := ""
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "reconc-") || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if !wrapperCandidateTrusted(path) {
			continue
		}
		if selected != "" {
			return "", fmt.Errorf("reconc binary resolution is ambiguous under %s for %s/%s", directory, runtime.GOOS, runtime.GOARCH)
		}
		selected = path
	}
	return selected, nil
}

const maxWrapperResolutionEntries = 4096

func wrapperCandidateTrusted(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && executableFile(path)
}
