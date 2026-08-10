package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestPolicyLockRFCReportsCurrentFormat(t *testing.T) {
	root := publicSurfaceRoot(t)
	rfc := readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0001-policy-lockfile.md")
	assertContainsAll(t, "RECONC-0001", rfc,
		"- Format version: `5`",
		"| `format_version` | string | Must equal `5`. |",
		"format-5 lock",
	)
	if strings.Contains(rfc, "| `format_version` | string | Must equal `4`. |") {
		t.Fatal("RECONC-0001 still attributes the current lock contract to format 4")
	}
}

func TestCurrentSchemaPublicationTagMatchesSourceVersion(t *testing.T) {
	root := publicSurfaceRoot(t)
	source := readPublicSurfaceFile(t, root, "cmd/reconc/main.go")
	match := regexp.MustCompile(`(?m)^var Version = "([0-9]+\.[0-9]+\.[0-9]+)"$`).FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatal("cmd/reconc/main.go does not declare one canonical source version")
	}
	if got, want := schema.CurrentSchemaTag, "reconc-v"+match[1]; got != want {
		t.Fatalf("current schema publication tag = %q, want source tag %q", got, want)
	}
}

func TestSchemaRegistryPublicationTagsOwnExactBytesOrAuthorizedPlan(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, contract := range schema.Contracts() {
		local, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(contract.LocalPath)))
		if err != nil {
			t.Fatalf("read %s: %v", contract.LocalPath, err)
		}
		if !gitObjectExists(root, contract.IntroductionTag+"^{commit}") {
			if contract.IntroductionTag != schema.CurrentSchemaTag {
				t.Errorf("publication tag %s is absent for %s", contract.IntroductionTag, contract.LocalPath)
			}
			continue
		}
		object := contract.IntroductionTag + ":" + contract.LocalPath
		command := exec.Command("git", "-C", root, "show", object)
		tagged, err := command.Output()
		if err != nil {
			t.Fatalf("read %s: %v", object, err)
		}
		if !bytes.Equal(local, tagged) {
			t.Errorf("%s differs from introduction object %s", contract.LocalPath, object)
		}
	}
}

func TestInheritedSchemaObservationsOwnExactTagBytes(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, observation := range schema.Observations() {
		object := observation.FirstExactTag + ":" + observation.LocalPath
		tagged, err := exec.Command("git", "-C", root, "show", object).Output()
		if err != nil {
			t.Fatalf("read %s: %v", object, err)
		}
		digest := sha256.Sum256(tagged)
		if got := hex.EncodeToString(digest[:]); got != observation.SHA256 {
			t.Errorf("%s digest = %s, want %s", object, got, observation.SHA256)
		}
		var document struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(tagged, &document); err != nil {
			t.Fatalf("parse %s: %v", object, err)
		}
		if document.ID != observation.ClaimedURL {
			t.Errorf("%s $id = %q, want observed %q", object, document.ID, observation.ClaimedURL)
		}
	}
}

func TestInheritedSchemaClassificationsMatchClaimedTagSemantics(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, observation := range schema.Observations() {
		t.Run(observation.LocalPath, func(t *testing.T) {
			claimedTag, claimedPath, taggedIdentity := rawGitHubSchemaObject(observation.ClaimedURL)
			if observation.Compatibility == schema.CompatibilityUnreachableClaimedHost {
				if taggedIdentity {
					t.Fatalf("unreachable-host observation unexpectedly names a Git tag: %s", observation.ClaimedURL)
				}
				return
			}
			if !taggedIdentity || claimedPath != observation.LocalPath {
				t.Fatalf("claimed URL does not identify the observed Git path: %s", observation.ClaimedURL)
			}
			claimedObject := claimedTag + ":" + claimedPath
			if observation.Compatibility == schema.CompatibilityAbsentAtClaimedTag {
				if gitObjectExists(root, claimedObject) {
					t.Fatalf("schema classified absent but %s exists", claimedObject)
				}
				return
			}

			claimed, err := exec.Command("git", "-C", root, "show", claimedObject).Output()
			if err != nil {
				t.Fatalf("read %s: %v", claimedObject, err)
			}
			firstObject := observation.FirstExactTag + ":" + observation.LocalPath
			first, err := exec.Command("git", "-C", root, "show", firstObject).Output()
			if err != nil {
				t.Fatalf("read %s: %v", firstObject, err)
			}
			sameBytes := bytes.Equal(claimed, first)
			sameSemantics := reflect.DeepEqual(
				schemaSemanticsWithoutPublicationIdentity(t, claimed),
				schemaSemanticsWithoutPublicationIdentity(t, first),
			)
			switch observation.Compatibility {
			case schema.CompatibilityByteIdentical:
				if !sameBytes {
					t.Fatalf("byte-identical classification differs: %s vs %s", claimedObject, firstObject)
				}
			case schema.CompatibilityIDOnlyDrift:
				if sameBytes || !sameSemantics {
					t.Fatalf("ID-only classification has bytes_equal=%t semantics_equal=%t", sameBytes, sameSemantics)
				}
			case schema.CompatibilitySemanticDrift:
				if sameSemantics {
					t.Fatal("semantic-drift classification differs only in publication identity")
				}
			default:
				t.Fatalf("classification %q reached tagged semantic comparison", observation.Compatibility)
			}
		})
	}
}

func rawGitHubSchemaObject(value string) (string, string, bool) {
	const prefix = "https://raw.githubusercontent.com/Christopher-Schulze/reconc/"
	remainder := strings.TrimPrefix(value, prefix)
	parts := strings.SplitN(remainder, "/", 2)
	if remainder == value || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func schemaSemanticsWithoutPublicationIdentity(t *testing.T, body []byte) any {
	t.Helper()
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse schema for semantic comparison: %v", err)
	}
	stripSchemaPublicationIdentity(document)
	return document
}

func stripSchemaPublicationIdentity(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "$id")
		for name, nested := range typed {
			if name == "$schema" {
				if contract, ok := nested.(map[string]any); ok {
					if _, present := contract["const"]; present {
						contract["const"] = "<publication-identity>"
					}
				}
			}
			stripSchemaPublicationIdentity(nested)
		}
	case []any:
		for _, nested := range typed {
			stripSchemaPublicationIdentity(nested)
		}
	}
}

func TestPlannedSchemaPublicationSurvivesPostTagSourceDrift(t *testing.T) {
	root := publicSurfaceRoot(t)
	repository := t.TempDir()
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.name", "Reconc Test")
	runGit(t, repository, "config", "user.email", "reconc-test@example.invalid")
	planned := make([]schema.Contract, 0)
	for _, contract := range schema.Contracts() {
		if contract.IntroductionTag != schema.CurrentSchemaTag {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(contract.LocalPath)))
		if err != nil {
			t.Fatalf("read %s: %v", contract.LocalPath, err)
		}
		target := filepath.Join(repository, filepath.FromSlash(contract.LocalPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			t.Fatal(err)
		}
		planned = append(planned, contract)
	}
	if len(planned) == 0 {
		t.Fatal("registry has no planned schema contracts")
	}
	runGit(t, repository, "add", "schemas")
	runGit(t, repository, "commit", "--quiet", "-m", "planned schemas")
	if gitObjectExists(repository, schema.CurrentSchemaTag+"^{commit}") {
		t.Fatal("temporary publication tag exists before tag creation")
	}
	runGit(t, repository, "tag", "-a", schema.CurrentSchemaTag, "-m", "schema publication")
	for _, contract := range planned {
		local, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(contract.LocalPath)))
		if err != nil {
			t.Fatal(err)
		}
		tagged, err := exec.Command("git", "-C", repository, "show", schema.CurrentSchemaTag+":"+contract.LocalPath).Output()
		if err != nil {
			t.Fatalf("read tagged %s: %v", contract.LocalPath, err)
		}
		if !bytes.Equal(local, tagged) {
			t.Fatalf("tagged schema differs immediately after publication: %s", contract.LocalPath)
		}
	}

	changed := planned[0]
	changedPath := filepath.Join(repository, filepath.FromSlash(changed.LocalPath))
	before, err := os.ReadFile(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changedPath, append(append([]byte(nil), before...), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", changed.LocalPath)
	runGit(t, repository, "commit", "--quiet", "-m", "post-tag drift")
	tagged, err := exec.Command("git", "-C", repository, "show", schema.CurrentSchemaTag+":"+changed.LocalPath).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tagged, before) {
		t.Fatal("post-tag source drift changed immutable tagged schema bytes")
	}
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func gitObjectExists(root string, object string) bool {
	return exec.Command("git", "-C", root, "cat-file", "-e", object).Run() == nil
}

func TestPublicSchemaEmittersAndValidatorsUseRegistryAPIs(t *testing.T) {
	root := publicSurfaceRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.KeyValueExpr:
				if key, ok := value.Key.(*ast.Ident); ok && key.Name == "Schema" && directSchemaURL(value.Value) {
					t.Errorf("%s stamps a schema URL constant instead of registry resolution", path)
				}
			case *ast.BinaryExpr:
				if schemaField(value.X) && directSchemaURL(value.Y) || directSchemaURL(value.X) && schemaField(value.Y) {
					t.Errorf("%s validates a schema URL constant instead of registry compatibility", path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func directSchemaURL(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || !strings.HasSuffix(selector.Sel.Name, "URL") {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "schema"
}

func schemaField(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Schema"
}
