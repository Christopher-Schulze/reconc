package assurance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestPackageScriptsRequiresOnlyDeclaredScripts(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "package.json", `{"scripts":{"test":"node --test","lint":"eslint ."}}`)
	writePackageScriptFile(t, root, "package-lock.json", "{}")
	gate := policy.AssuranceGate{ID: "npm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, PackageManager: "npm", Commands: []string{"npm run test", "npm run lint", "npm run build"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Message, `"lint"`) || strings.Contains(findings[0].Message, `"build"`) {
		t.Fatalf("declared-script findings = %+v", findings)
	}
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test", "npm run lint"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("complete declared scripts = %+v, %v", findings, err)
	}
}

func TestPackageScriptsScopesMonorepoCommandsToManifestDirectory(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "package.json", `{"packageManager":"pnpm@10.0.0","private":true}`)
	writePackageScriptFile(t, root, "packages/api service/package.json", `{"scripts":{"test":"vitest run"}}`)
	writePackageScriptFile(t, root, "pnpm-lock.yaml", "lock")
	gate := policy.AssuranceGate{ID: "pnpm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, PackageManager: "pnpm", Commands: []string{"pnpm run test"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"pnpm --dir 'packages/api service' run test"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("monorepo command evidence = %+v, %v", findings, err)
	}
}

func TestPackageScriptsBindsEvidenceToDetectedRunner(t *testing.T) {
	tests := []struct {
		manager string
		lock    string
	}{
		{manager: "bun", lock: "bun.lock"},
		{manager: "npm", lock: "package-lock.json"},
		{manager: "pnpm", lock: "pnpm-lock.yaml"},
		{manager: "yarn", lock: "yarn.lock"},
	}
	for _, test := range tests {
		t.Run(test.manager, func(t *testing.T) {
			root := t.TempDir()
			writePackageScriptFile(t, root, "package.json", fmt.Sprintf(`{"packageManager":%q,"scripts":{"test":"node --test"}}`, test.manager+"@1.0.0"))
			writePackageScriptFile(t, root, test.lock, "lock")
			other := "npm"
			if test.manager == other {
				other = "pnpm"
			}
			gate := policy.AssuranceGate{
				ID: "scripts", Type: policy.AssurancePackageScripts,
				ManifestPaths: []string{"package.json"}, PackageManager: test.manager,
				Commands: []string{test.manager + " run test", other + " run test"},
			}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{other + " run test"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || !strings.Contains(findings[0].Message, "no current successful evidence") {
				t.Fatalf("mismatched runner evidence accepted: %+v", findings)
			}
			findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{test.manager + " run test"}})
			if err != nil || len(findings) != 0 {
				t.Fatalf("detected runner evidence rejected: findings=%+v err=%v", findings, err)
			}
		})
	}
}

func TestPackageScriptsAcceptsExplicitRunnerCaseVariant(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "package.json", `{"packageManager":"npm@11","scripts":{"test":"node --test"}}`)
	writePackageScriptFile(t, root, "package-lock.json", "lock")
	gate := policy.AssuranceGate{
		ID: "scripts", Type: policy.AssurancePackageScripts,
		ManifestPaths: []string{"package.json"}, PackageManager: "npm", Commands: []string{"NPM run test"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("case-equivalent npm runner was rejected: findings=%+v err=%v", findings, err)
	}
}

func TestPackageScriptsHonorsManifestMarkersAndRejectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "plain/package.json", `{"scripts":{"typecheck":"tsc --noEmit"}}`)
	writePackageScriptFile(t, root, "typed/package.json", `{"packageManager":"pnpm@10.0.0","scripts":{"typecheck":"tsc --noEmit"}}`)
	writePackageScriptFile(t, root, "typed/package-lock.json", "{}")
	writePackageScriptFile(t, root, "typed/tsconfig.json", "{}")
	gate := policy.AssuranceGate{ID: "typescript-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, ManifestMarkers: []string{"tsconfig.json", "tsconfig.*.json"}, Commands: []string{"npm run typecheck", "pnpm run typecheck", "yarn run typecheck", "bun run typecheck"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "ambiguous") || findings[0].Paths[0] != "typed/package.json" {
		t.Fatalf("marker/ambiguity findings = %+v", findings)
	}
}

func TestPackageScriptsRejectsMissingOrMismatchedExpectedManager(t *testing.T) {
	for _, test := range []struct {
		name            string
		managerEvidence string
		want            string
	}{
		{name: "missing", want: "expected package manager npm could not be detected"},
		{name: "mismatched", managerEvidence: `,"packageManager":"pnpm@10"`, want: "expected package manager npm but detected pnpm"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePackageScriptFile(t, root, "package.json", `{"scripts":{"test":"node --test"}`+test.managerEvidence+`}`)
			gate := policy.AssuranceGate{ID: "npm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, PackageManager: "npm", Commands: []string{"npm run test"}}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || !strings.Contains(findings[0].Message, test.want) {
				t.Fatalf("manager findings = %+v", findings)
			}
		})
	}
}

func TestPackageScriptsIgnoresSymlinkedManagerEvidence(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "package.json", `{"scripts":{"test":"node --test"}}`)
	writePackageScriptFile(t, root, "package-lock.json", "{}")
	outside := filepath.Join(t.TempDir(), "yarn.lock")
	if err := os.WriteFile(outside, []byte("lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "yarn.lock")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	gate := policy.AssuranceGate{ID: "npm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, PackageManager: "npm", Commands: []string{"npm run test"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("symlinked lockfile must not create manager evidence: findings=%+v err=%v", findings, err)
	}
}

func TestPackageScriptsToleratesUTF8BOM(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "package.json")
	if err := os.WriteFile(path, append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"packageManager":"npm@10","scripts":{"test":"node --test"}}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{ID: "npm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, PackageManager: "npm", Commands: []string{"npm run test"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("BOM-prefixed package manifest: findings=%+v err=%v", findings, err)
	}
}

func TestPackageScriptsReportsMalformedManifestAndContinues(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "broken/package.json", `{"scripts":`)
	writePackageScriptFile(t, root, "valid/package.json", `{"packageManager":"npm@10","scripts":{"test":"node --test"}}`)
	gate := policy.AssuranceGate{ID: "npm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, Commands: []string{"npm run test"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm --prefix valid run test"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Paths[0] != "broken/package.json" || !strings.Contains(findings[0].Message, "not valid JSON") {
		t.Fatalf("malformed manifest findings = %+v", findings)
	}
}

func TestPackageScriptsIgnoresFixtureDirectoriesAndConfiguredExcludes(t *testing.T) {
	root := t.TempDir()
	writePackageScriptFile(t, root, "package.json", `{"packageManager":"npm@10","scripts":{"test":"node --test"}}`)
	writePackageScriptFile(t, root, "fixtures/app/package.json", `{"scripts":{"test":"fixture"}}`)
	writePackageScriptFile(t, root, "testdata/app/package.json", `{"scripts":{"test":"fixture"}}`)
	writePackageScriptFile(t, root, "examples/app/package.json", `{"scripts":{"test":"example"}}`)
	writePackageScriptFile(t, root, "__fixtures__/app/package.json", `{"scripts":{"test":"fixture"}}`)
	writePackageScriptFile(t, root, "samples/app/package.json", `{"scripts":{"test":"sample"}}`)
	gate := policy.AssuranceGate{ID: "npm-scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, ExcludePaths: []string{"samples/**"}, PackageManager: "npm", Commands: []string{"npm run test"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{SuccessfulCommands: []string{"npm run test"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("ignored manifests: findings=%+v err=%v", findings, err)
	}
}

func writePackageScriptFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
