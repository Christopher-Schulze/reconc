package assurance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestAssuranceScopesNestedModulesAndBindsCommandDirectory(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\n")
	writeAssuranceFile(t, root, "services/worker/go.mod", "module example/worker\n")
	writeAssuranceFile(t, root, "services/worker/main.go", "package worker\n")
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"services/api/main.go", "services/worker/main.go"},
		SuccessfulCommandEvidence: []CommandEvidence{{
			Command:          "go test ./...",
			WorkingDirectory: filepath.Join(root, "services/api"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one unsatisfied module", findings)
	}
	finding := findings[0]
	if finding.ModuleRoot != "services/worker" || finding.Manifest != "services/worker/go.mod" {
		t.Fatalf("finding scope = %+v", finding)
	}
	if len(finding.EffectiveScope) != 1 || finding.EffectiveScope[0] != "services/worker/main.go" {
		t.Fatalf("effective scope = %v", finding.EffectiveScope)
	}
}

func TestAssuranceAcceptsExplicitModuleCommandPrefix(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\n")
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths:       []string{"services/api/main.go"},
		SuccessfulCommands: []string{"cd services/api && go test ./..."},
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("explicit module command = %+v, %v", findings, err)
	}
}

func TestAssuranceChoosesDeepestOverlappingModuleRoot(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "go.mod", "module example/root\n")
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\n")
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths:       []string{"services/api/main.go"},
		SuccessfulCommands: []string{"go test ./..."},
	})
	if err != nil || len(findings) != 1 || findings[0].ModuleRoot != "services/api" {
		t.Fatalf("root evidence must not satisfy deepest module: findings=%+v err=%v", findings, err)
	}
}

func TestAssuranceDoesNotUseNestedCommandForOverlappingRootModule(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "go.mod", "module example/root\n")
	writeAssuranceFile(t, root, "main.go", "package root\n")
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"main.go"},
		SuccessfulCommandEvidence: []CommandEvidence{{
			Command:          "go test ./...",
			WorkingDirectory: filepath.Join(root, "services/api"),
		}},
	})
	if err != nil || len(findings) != 1 || findings[0].ModuleRoot != "." {
		t.Fatalf("nested command must not satisfy overlapping root: findings=%+v err=%v", findings, err)
	}
}

func TestAssuranceAllowsWorkspaceCommandForOwnedModule(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/api\"]\n")
	writeAssuranceFile(t, root, "crates/api/Cargo.toml", "[package]\nname = \"api\"\nversion = \"0.1.0\"\n")
	writeAssuranceFile(t, root, "crates/api/src/lib.rs", "pub fn answer() -> u8 { 42 }\n")
	gate := policy.AssuranceGate{
		ID: "rust-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"Cargo.toml"}, Commands: []string{"cargo test --workspace"}, CommandPolicy: "all",
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"crates/api/src/lib.rs"},
		SuccessfulCommandEvidence: []CommandEvidence{{
			Command:          "cargo test --workspace",
			WorkingDirectory: root,
		}},
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("workspace command = %+v, %v", findings, err)
	}
}

func TestAssuranceReportsMissingModuleEvidenceForChangedSource(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/main.go", "package api\n")
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	_, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"services/api/main.go"}})
	if err == nil || !strings.Contains(err.Error(), "no detected module root covers changed paths") {
		t.Fatalf("missing module evidence error = %v", err)
	}
}

func TestAssuranceReportsDeletedApplicableModuleManifest(t *testing.T) {
	for _, test := range []struct {
		name      string
		replace   func(string) error
		wantError string
	}{
		{name: "deleted", replace: os.Remove, wantError: "unavailable"},
		{name: "non-regular", replace: func(path string) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.Mkdir(path, 0o755)
		}, wantError: "not a regular file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			manifest := filepath.Join(root, "services", "api", "go.mod")
			writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
			if err := test.replace(manifest); err != nil {
				t.Fatal(err)
			}
			gate := policy.AssuranceGate{
				ID: "go-live", Type: policy.AssuranceLiveVerification,
				ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
			}
			_, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"services/api/go.mod"}})
			if err == nil || !strings.Contains(err.Error(), "services/api/go.mod") || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("missing applicable module manifest error = %v", err)
			}
		})
	}
}

func TestAssuranceIgnoresUndetectableChangedModuleManifests(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\n")
	writeAssuranceFile(t, root, "vendor/go.mod", "module example/vendor\n")
	writeAssuranceFile(t, root, "a/b/c/d/e/f/go.mod", "module example/deep\n")
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}

	if _, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"vendor/go.mod", `VENDOR\pkg\go.mod`, "a/b/c/d/e/f/go.mod"},
	}); err != nil {
		t.Fatalf("ignored changed manifests blocked assurance: %v", err)
	}

	if err := os.Remove(filepath.Join(root, "vendor", "go.mod")); err != nil {
		t.Fatal(err)
	}
	if _, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"vendor/go.mod"}}); err != nil {
		t.Fatalf("deleted ignored manifest blocked assurance: %v", err)
	}

	if _, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths:              []string{"services/api/main.go", "vendor/go.mod"},
		SuccessfulCommandEvidence: []CommandEvidence{{Command: "go test ./...", WorkingDirectory: filepath.Join(root, "services/api")}},
	}); err != nil {
		t.Fatalf("ignored vendor manifest impersonated the nested module: %v", err)
	}
}

func TestAssuranceStillRejectsSymlinkedAdmissibleManifest(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/main.go", "package api\n")
	outside := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(outside, []byte("module example/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "services", "api", "go.mod")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, manifest); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	gate := policy.AssuranceGate{
		ID: "go-live", Type: policy.AssuranceLiveVerification,
		ApplicableIf: []string{"go.mod"}, Commands: []string{"go test ./..."}, CommandPolicy: "all",
	}
	_, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"services/api/go.mod"}})
	if err == nil || !strings.Contains(err.Error(), "services/api/go.mod") || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked admissible manifest error = %v", err)
	}
}

func TestAssurancePackageScriptEvidenceBindsQuotedModuleDirectory(t *testing.T) {
	root := t.TempDir()
	module := "packages/api service"
	writeAssuranceFile(t, root, filepath.Join(module, "package.json"), `{"scripts":{"test":"vitest"}}`)
	writeAssuranceFile(t, root, filepath.Join(module, "pnpm-lock.yaml"), "lock\n")
	writeAssuranceFile(t, root, filepath.Join(module, "index.ts"), "export {}\n")
	gate := policy.AssuranceGate{
		ID: "scripts", Type: policy.AssurancePackageScripts,
		ApplicableIf: []string{"package.json"}, ManifestPaths: []string{"**/package.json"},
		PackageManager: "pnpm", Commands: []string{"pnpm run test"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths:       []string{filepath.ToSlash(filepath.Join(module, "index.ts"))},
		SuccessfulCommands: []string{"pnpm --dir 'packages/api service' run test"},
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("quoted module package command = %+v, %v", findings, err)
	}
}

func TestAssuranceInputIdentityIncludesScopedModuleFacts(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "services/api/go.mod", "module example/api\n")
	writeAssuranceFile(t, root, "services/api/main.go", "package api\nfunc answer() {\n}\n")
	gate := policy.AssuranceGate{
		ID: "format", Type: policy.AssuranceGoFormat,
		ApplicableIf: []string{"go.mod"}, ScanPaths: []string{"**/*.go"},
	}
	inputs := Inputs{ChangedPaths: []string{"services/api/main.go"}}
	_, first, err := EvaluateWithInputIdentity(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "services/api/main.go", "package api\nfunc answer() { }\n")
	_, second, err := EvaluateWithInputIdentity(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("scoped module fact changes must alter assurance input identity")
	}
}
