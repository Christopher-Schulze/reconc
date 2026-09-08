package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/schema"
)

func TestTemplateChangesInvalidateColdAndWarmPolicy(t *testing.T) {
	const original = "kind: deny_write\nmode: block\npaths: ['generated/**']\nmessage: original\n"
	const changed = "kind: deny_write\nmode: block\npaths: ['protected/**']\nmessage: modified\n"
	cases := []struct {
		name      string
		initial   string
		mutation  string
		remove    bool
		unused    bool
		wantStale bool
	}{
		{name: "changed override", initial: original, mutation: changed, wantStale: true},
		{name: "comment-only content change", initial: original, mutation: original + "# new provenance\n", wantStale: true},
		{name: "removed override selects builtin", initial: original, remove: true, wantStale: true},
		{name: "new override shadows builtin", mutation: changed, wantStale: true},
		{name: "malformed override", initial: original, mutation: "kind: [\n", wantStale: true},
		{name: "unchanged override", initial: original, mutation: original},
		{name: "unused override", mutation: changed, unused: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(presets.HomeEnvVar, home)
			directory := filepath.Join(home, "templates")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "no-generated-writes.yml")
			if test.initial != "" {
				if err := os.WriteFile(path, []byte(test.initial), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			repo := t.TempDir()
			config := "rules:\n  - id: generated\n    template: no-generated-writes\n"
			if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
				t.Fatal(err)
			}
			warm := NewEvaluator()
			if _, err := warm.CheckRepoPolicy(repo, Empty()); err != nil {
				t.Fatalf("initial policy: %v", err)
			}
			if test.unused {
				path = filepath.Join(directory, "unused.yml")
			}
			if test.remove {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(test.mutation), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, evaluator := range []*Evaluator{warm, NewEvaluator()} {
				_, err := evaluator.CheckRepoPolicy(repo, Empty())
				if (err != nil) != test.wantStale {
					t.Errorf("template mutation: error = %v, want stale = %v", err, test.wantStale)
				}
			}
		})
	}
}

func TestPreviousSchemaLockTemplateCompatibility(t *testing.T) {
	for _, test := range []struct {
		name       string
		referenced bool
	}{
		{name: "no template remains compatible"},
		{name: "template requires refresh", referenced: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(presets.HomeEnvVar, t.TempDir())
			repo := t.TempDir()
			config := "rules: []\n"
			if test.referenced {
				config = "rules:\n  - id: generated\n    template: no-generated-writes\n"
			}
			if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
				t.Fatal(err)
			}
			rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
				payload["$schema"] = schema.PolicyLockV6URLV097
				delete(payload, "template_dependencies")
				body, err := json.Marshal(map[string]interface{}{"source_precedence": payload["source_precedence"], "sources": payload["sources"]})
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(body)
				payload["source_digest"] = hex.EncodeToString(digest[:])
			})
			_, err := NewEvaluator().CheckRepoPolicy(repo, Empty())
			if test.referenced && (err == nil || !strings.Contains(err.Error(), "refresh")) {
				t.Fatalf("legacy template lock must require refresh: %v", err)
			}
			if !test.referenced && err != nil {
				t.Fatalf("unchanged legacy lock rejected: %v", err)
			}
		})
	}
}

func TestTemplateProvenanceTamperingRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{"removed", func(p map[string]interface{}) { delete(p, "template_dependencies") }},
		{"null", func(p map[string]interface{}) { p["template_dependencies"] = nil }},
		{"empty", func(p map[string]interface{}) { p["template_dependencies"] = []interface{}{} }},
		{"changed digest", func(p map[string]interface{}) {
			p["template_dependencies"].([]interface{})[0].(map[string]interface{})["content_sha256"] = strings.Repeat("a", 64)
		}},
		{"duplicate", func(p map[string]interface{}) {
			items := p["template_dependencies"].([]interface{})
			p["template_dependencies"] = append(items, items[0])
		}},
		{"unknown field", func(p map[string]interface{}) {
			p["template_dependencies"].([]interface{})[0].(map[string]interface{})["path"] = "private"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(presets.HomeEnvVar, t.TempDir())
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("rules:\n  - id: generated\n    template: no-generated-writes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
				t.Fatal(err)
			}
			warm := NewEvaluator()
			if _, err := warm.CheckRepoPolicy(repo, Empty()); err != nil {
				t.Fatal(err)
			}
			rewriteLockfileWithDigest(t, repo, test.mutate)
			for _, evaluator := range []*Evaluator{warm, NewEvaluator()} {
				if _, err := evaluator.CheckRepoPolicy(repo, Empty()); err == nil {
					t.Error("accepted modified template provenance with recomputed lock digest")
				}
			}
		})
	}
}
