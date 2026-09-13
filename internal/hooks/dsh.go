package hooks

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

//go:embed dsh_extension.mjs
var dshExtensionSource string

func generateDSH() (*Artifact, error) {
	budgets, err := bunRouteBudgets(KindDSH)
	if err != nil {
		return nil, err
	}
	content := strings.Replace(dshExtensionSource, "__ROUTE_BUDGETS__", budgets, 1)
	return &Artifact{Kind: KindDSH, TargetPath: DSHExtensionPath, Content: content}, nil
}

// GenerateDSHPatch returns the repository-owned DSH profile overlay used by
// both direct hook installation and transactional bootstrap.
func GenerateDSHPatch() *Artifact {
	return &Artifact{
		Kind:       KindDSH,
		TargetPath: DSHPatchPath,
		Content: `# Managed by reconc. Load with dsh --patch .dsh/reconc.patch.yml.
- insert:
    - id: reconc-native
      name: ./reconc.mjs
      inject: [tools, systemPrompt]

- id: agent-loop
  inject: [reconcGuard]
`,
	}
}

func preflightDSHPatch(root string) (managedArtifactSnapshot, error) {
	target := filepath.Join(root, filepath.FromSlash(DSHPatchPath))
	if err := requireManagedTargetWithin(root, target); err != nil {
		return managedArtifactSnapshot{}, err
	}
	snapshot, err := readManagedArtifactSnapshot(target)
	if err != nil {
		return managedArtifactSnapshot{}, &rerrors.PolicySourceError{Message: "read " + DSHPatchPath, Cause: err}
	}
	if snapshot.exists && string(snapshot.body) != GenerateDSHPatch().Content &&
		!strings.HasPrefix(string(snapshot.body), "# Managed by reconc. Load with dsh --patch .dsh/reconc.patch.yml.\n") {
		return managedArtifactSnapshot{}, &rerrors.PolicySourceError{Message: DSHPatchPath + " is not Reconc-managed; refusing to overwrite the user's DSH patch"}
	}
	return snapshot, nil
}

func installDSHPatch(root string, snapshot managedArtifactSnapshot) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(DSHPatchPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", &rerrors.PolicySourceError{Message: "create parent of " + DSHPatchPath, Cause: err}
	}
	return writeGeneratedArtifact(target, GenerateDSHPatch().Content, false, snapshot)
}
