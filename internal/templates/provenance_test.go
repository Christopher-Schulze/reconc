package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyBindsRawContentAndHidesHome(t *testing.T) {
	for _, suffix := range []string{"", "# provenance-only change\n"} {
		t.Run(suffix, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("RECONC_HOME", home)
			directory := filepath.Join(home, "templates")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			body := "kind: deny_write\npaths: ['generated/**']\n" + suffix
			path := filepath.Join(directory, "no-generated-writes.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			template, err := Resolve("no-generated-writes")
			if err != nil {
				t.Fatal(err)
			}
			dependency := template.Dependency()
			digest := sha256.Sum256([]byte(body))
			if dependency.ContentSHA256 != hex.EncodeToString(digest[:]) || dependency.Source != SourceUser {
				t.Fatalf("incorrect dependency: %+v", dependency)
			}
			encoded, err := json.Marshal(dependency)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), home) || strings.Contains(string(encoded), "generated/**") {
				t.Fatalf("dependency leaks private inputs: %s", encoded)
			}
			if err := ValidateCurrentDependencies([]Dependency{dependency}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body+"# replacement\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateCurrentDependencies([]Dependency{dependency}); err == nil {
				t.Fatal("raw template replacement was accepted")
			}
		})
	}
}
