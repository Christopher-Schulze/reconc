package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Each fixture is an artifact a published release generated, stored as
// testdata/legacy-generators/<release>/<current kind>.artifact: 0.10.3
// (identical from 0.10.0), 0.9.8 Codex, and 0.7.3, whose GitHub Copilot kind
// was still named "copilot". The registry must recognize each so an upgrade is
// not reported as user edits.
type legacyFixture struct {
	release string
	kind    string
	body    []byte
}

func legacyFixtures(t *testing.T) []legacyFixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "legacy-generators", "*", "*.artifact"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 13 {
		t.Fatalf("legacy generator fixtures = %d, want at least 13", len(paths))
	}
	fixtures := make([]legacyFixture, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, legacyFixture{
			release: filepath.Base(filepath.Dir(path)),
			kind:    strings.TrimSuffix(filepath.Base(path), ".artifact"),
			body:    body,
		})
	}
	return fixtures
}

func TestLegacyGeneratorRegistryRecognizesPublishedArtifacts(t *testing.T) {
	for _, fixture := range legacyFixtures(t) {
		kind := fixture.kind
		if !legacyGeneratedArtifact(kind, fixture.body) {
			t.Errorf("the registry does not recognize the %s %s artifact", fixture.release, kind)
		}
		edited := append(slices.Clone(fixture.body), []byte("// edited\n")...)
		var document map[string]interface{}
		if json.Unmarshal(fixture.body, &document) == nil {
			document["edited"] = true
			if edited, _ = json.Marshal(document); edited == nil {
				t.Fatal("marshal edited document")
			}
		}
		if legacyGeneratedArtifact(kind, edited) {
			t.Errorf("an edited %s %s artifact was recognized as release output", fixture.release, kind)
		}
	}
}

func TestUpgradeFromAPublishedReleaseIsNotAUserEdit(t *testing.T) {
	for _, fixture := range legacyFixtures(t) {
		kind := fixture.kind
		t.Run(fixture.release+"/"+kind, func(t *testing.T) {
			definition, ok := lookupPlatformDefinition(kind)
			if !ok {
				t.Fatalf("unknown kind %s", kind)
			}
			write := func(t *testing.T, body []byte) (string, string) {
				t.Helper()
				repo := t.TempDir()
				target := filepath.Join(repo, filepath.FromSlash(definition.TargetPath))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, body, 0o644); err != nil {
					t.Fatal(err)
				}
				return repo, target
			}
			legacy := fixture.body
			repo, _ := write(t, legacy)
			report, err := Install(kind, repo, false)
			if err != nil {
				t.Fatalf("install over the %s artifact: %v", fixture.release, err)
			}
			if len(report.DroppedUserEdits) != 0 {
				t.Fatalf("upgrading a %s install reported user edits: %v", fixture.release, report.DroppedUserEdits)
			}
			repo, target := write(t, legacy)
			if _, err := Uninstall(kind, repo); err != nil {
				t.Fatalf("uninstalling a %s install was refused: %v", fixture.release, err)
			}
			if remaining, err := os.ReadFile(target); err == nil && strings.Contains(string(remaining), "tools/reconc/bin/hook") {
				t.Fatalf("uninstall left %s hooks in place:\n%s", fixture.release, remaining)
			} else if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if !json.Valid(legacy) {
				return
			}
			// A real edit of a release entry is still a user edit: install
			// either reports replacing it or refuses and keeps the file.
			edited := strings.Replace(string(legacy), `"timeout": 10`, `"timeout": 11`, 1)
			if edited == string(legacy) {
				edited = strings.Replace(string(legacy), `"timeoutSec": 10`, `"timeoutSec": 11`, 1)
			}
			if edited == string(legacy) {
				t.Skip("no timeout to edit in this artifact")
			}
			repo, target = write(t, []byte(edited))
			report, err = Install(kind, repo, false)
			if err == nil && len(report.DroppedUserEdits) == 0 {
				t.Fatalf("an edited %s entry was replaced silently", fixture.release)
			}
			if err != nil {
				if kept, readErr := os.ReadFile(target); readErr != nil || string(kept) != edited {
					t.Fatalf("a refused install changed the edited %s file: %v", fixture.release, err)
				}
			}
		})
	}
}
