package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedArtifactOwnershipRequiresSignatureAtDefinedLocation(t *testing.T) {
	tests := []struct {
		kind    string
		foreign string
	}{
		{KindGitPreCommit, "#!/bin/sh\necho '# Managed by `reconc hook install git-pre-commit`.'\n"},
		{KindOpenCode, "console.log('Managed by reconc; reconc hook runtime opencode-stop')\n"},
		{KindKilo, "console.log('Managed by reconc; kilo-pre-tool-use')\n"},
		{KindGitHubCopilot, `{"note":"tools/reconc/bin/hook copilot-stop ."}` + "\n"},
		{KindGrok, `{"note":"reconcManaged: true; grok-pre-tool-use"}` + "\n"},
		{KindOMP, "console.log('Managed by reconc. Project-local Oh My Pi policy extension.; omp-pre-tool-use; omp-stop')\n"},
		{KindPi, "console.log('Managed by reconc. Project-local Pi policy extension.; pi-pre-tool-use; pi-stop')\n"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			artifact, err := Generate(test.kind)
			if err != nil {
				t.Fatal(err)
			}
			if !managedPlatformArtifact(test.kind, []byte(artifact.Content)) {
				t.Fatal("current generated artifact lost its ownership signature")
			}
			if managedPlatformArtifact(test.kind, []byte(test.foreign)) {
				t.Fatal("marker mention outside the defined signature location granted ownership")
			}
		})
	}

	wrapper := GenerateWrapper()
	if !wrapperManagedArtifact([]byte(wrapper.Content)) {
		t.Fatal("current generated wrapper lost its ownership signature")
	}
	if wrapperManagedArtifact([]byte("#!/bin/sh\necho '# Managed by Reconc. Repo-local agent hook wrapper.'\n")) {
		t.Fatal("wrapper marker mention outside line two granted ownership")
	}
}

func TestPluginInstallDoesNotOverwriteForeignMarkerMentions(t *testing.T) {
	tests := []struct {
		kind    string
		path    string
		foreign string
	}{
		{KindOpenCode, OpenCodePluginPath, "console.log('Managed by reconc; reconc hook runtime opencode-stop')\n"},
		{KindKilo, KiloPluginPath, "console.log('Managed by reconc; kilo-pre-tool-use')\n"},
		{KindGrok, GrokHooksPath, `{"note":"reconcManaged: true; grok-pre-tool-use"}` + "\n"},
		{KindOMP, OMPExtensionPath, "console.log('Managed by reconc. Project-local Oh My Pi policy extension.; omp-stop')\n"},
		{KindPi, PiExtensionPath, "console.log('Managed by reconc. Project-local Pi policy extension.; pi-stop')\n"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			repo := t.TempDir()
			target := filepath.Join(repo, filepath.FromSlash(test.path))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(test.foreign), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(test.kind, repo, false); err == nil || !strings.Contains(err.Error(), "not reconc-managed") && !strings.Contains(err.Error(), "user-owned") {
				t.Fatalf("foreign marker artifact was not refused: %v", err)
			}
			body, err := os.ReadFile(target)
			if err != nil || string(body) != test.foreign {
				t.Fatalf("refused install changed foreign artifact: %q, %v", body, err)
			}
		})
	}
}

func TestUnparseableMarkerCommandRemainsForeign(t *testing.T) {
	signature := commandSignature(`echo "tools/reconc/bin/hook`, nil)
	if reconcCommandOwned(signature) {
		t.Fatal("an unparseable marker mention granted hook-entry ownership")
	}
	object := map[string]interface{}{
		"PreInvocation": []interface{}{map[string]interface{}{
			"type": "command", "command": `echo "tools/reconc/bin/hook`,
		}},
	}
	if antigravityHookObjectIsReconcManaged(object) {
		t.Fatal("an unparseable marker mention granted Antigravity object ownership")
	}
}
