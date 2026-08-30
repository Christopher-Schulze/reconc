package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

// GenerateWrapperTarget returns the one-line, version-independent direct
// target receipt for a supported stable repository binary.
func GenerateWrapperTarget(targetOS, targetArch string) (*Artifact, error) {
	name, ok := stableWrapperTargetName(targetOS, targetArch)
	if !ok {
		return nil, fmt.Errorf("unsupported hook target platform %s/%s", targetOS, targetArch)
	}
	return &Artifact{
		Kind:       "hook-wrapper-target",
		TargetPath: WrapperTargetPath,
		Content:    filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name)) + "\n",
	}, nil
}

func stableWrapperTargetName(targetOS, targetArch string) (string, bool) {
	switch targetOS + "/" + targetArch {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64":
		return "reconc-" + targetOS + "-" + targetArch, true
	case "windows/amd64":
		return "reconc-windows-amd64.exe", true
	default:
		return "", false
	}
}

type wrapperTargetOperations struct {
	lstat         func(string) (os.FileInfo, error)
	readSnapshot  func(string) (managedArtifactSnapshot, error)
	writeArtifact func(string, string, bool, managedArtifactSnapshot) (string, error)
}

func ensureWrapperTarget(root string, force bool) (string, error) {
	return ensureWrapperTargetWithOperations(root, force, wrapperTargetOperations{
		lstat:         os.Lstat,
		readSnapshot:  readManagedArtifactSnapshot,
		writeArtifact: writeGeneratedArtifact,
	})
}

func ensureWrapperTargetWithOperations(root string, force bool, operations wrapperTargetOperations) (string, error) {
	artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", nil
	}
	binaryRelative := strings.TrimSuffix(artifact.Content, "\n")
	binaryPath := filepath.Join(root, filepath.FromSlash(binaryRelative))
	info, err := operations.lstat(binaryPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "inspect wrapper direct target", Cause: err}
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !executableFile(binaryPath) {
		return "", nil
	}

	target := filepath.Join(root, filepath.FromSlash(artifact.TargetPath))
	if err := requireManagedTargetWithin(root, target); err != nil {
		return "", err
	}
	snapshot, err := operations.readSnapshot(target)
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "read " + WrapperTargetPath, Cause: err}
	}
	if snapshot.exists {
		if string(snapshot.body) != artifact.Content && !validWrapperTargetContent(snapshot.body) && !force {
			return "", &rerrors.PolicySourceError{Message: WrapperTargetPath + " exists and is not a valid reconc-managed direct target; pass --force to overwrite"}
		}
	}
	return operations.writeArtifact(target, artifact.Content, false, snapshot)
}

func validWrapperTargetContent(content []byte) bool {
	for _, platform := range [][2]string{
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"linux", "amd64"}, {"linux", "arm64"},
		{"windows", "amd64"},
	} {
		artifact, err := GenerateWrapperTarget(platform[0], platform[1])
		if err == nil && string(content) == artifact.Content {
			return true
		}
	}
	return false
}
