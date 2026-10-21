package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCheckedCatalogOffline(t *testing.T) {
	root := repositoryRoot(t)
	catalog, err := loadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	result := compareCatalog(context.Background(), root, catalog, false)
	if !result.Complete || result.ReviewRequired || result.SourcesChecked != 0 || result.Mode != "offline" || len(result.Results) != 21 {
		t.Fatalf("offline fixture comparison: %+v", result)
	}
	var first, second, stderr bytes.Buffer
	for _, output := range []*bytes.Buffer{&first, &second} {
		if code := run(context.Background(), []string{"--root", root}, output, &stderr); code != 0 {
			t.Fatalf("exit %d: %s", code, &stderr)
		}
	}
	if first.String() != second.String() {
		t.Fatal("unchanged inputs produced different reports")
	}
	catalog.Hosts[0].Fixtures[0].SHA256 = strings.Repeat("0", 64)
	changed := compareCatalog(context.Background(), root, catalog, false)
	if !changed.Complete || !changed.ReviewRequired || changed.Results[0].Status != "changed" {
		t.Fatalf("fixture drift lost: %+v", changed)
	}
	catalog.Hosts[0].Fixtures[0].Path = "missing-contract.json"
	unavailable := compareCatalog(context.Background(), root, catalog, false)
	if unavailable.Complete || unavailable.Results[0].Status != "unavailable" {
		t.Fatalf("missing fixture considered healthy: %+v", unavailable)
	}
}

func TestCatalogRejectsInvalidCoverageAndProvenance(t *testing.T) {
	root := repositoryRoot(t)
	for _, test := range []struct {
		name   string
		change func(*sourceCatalog)
	}{
		{"missing host", func(c *sourceCatalog) { c.Hosts = c.Hosts[1:] }},
		{"duplicate host", func(c *sourceCatalog) { c.Hosts[1].Host = c.Hosts[0].Host }},
		{"missing fixture", func(c *sourceCatalog) { c.Hosts[0].Fixtures = nil }},
		{"unexpected fixture", func(c *sourceCatalog) { c.Hosts[0].Fixtures[0].Path = "../outside" }},
		{"invalid digest", func(c *sourceCatalog) { c.Hosts[0].Fixtures[0].SHA256 = "abc" }},
		{"missing sources", func(c *sourceCatalog) { c.Hosts[0].Sources = nil }},
		{"unofficial URL", func(c *sourceCatalog) { c.Hosts[0].Sources[0].URL = "https://example.com/hooks" }},
		{"missing markers", func(c *sourceCatalog) { c.Hosts[0].Sources[0].RequiredMarkers = nil }},
		{"invalid mode", func(c *sourceCatalog) { c.Hosts[0].Sources[0].Normalization = "execute" }},
		{"missing sections", func(c *sourceCatalog) { c.Hosts[0].Sources[0].Sections = nil }},
		{"empty section start", func(c *sourceCatalog) { c.Hosts[0].Sources[0].Sections[0].Start = "" }},
		{"section ends at its start", func(c *sourceCatalog) {
			c.Hosts[0].Sources[0].Sections[0].End = c.Hosts[0].Sources[0].Sections[0].Start
		}},
		{"previous catalog format", func(c *sourceCatalog) { c.FormatVersion = 1 }},
		{"undeclared branch", func(c *sourceCatalog) {
			for h := range c.Hosts {
				for s := range c.Hosts[h].Sources {
					p := &c.Hosts[h].Sources[s]
					if strings.Contains(p.URL, "/HEAD/") {
						p.URL = strings.Replace(p.URL, "/HEAD/", "/v1/", 1)
						return
					}
				}
			}
		}},
		{"tracked ref with HEAD URL", func(c *sourceCatalog) {
			for h := range c.Hosts {
				for s := range c.Hosts[h].Sources {
					p := &c.Hosts[h].Sources[s]
					if strings.Contains(p.URL, "/HEAD/") {
						p.TrackedRef = "v1"
						return
					}
				}
			}
		}},
		{"tracked ref on documentation", func(c *sourceCatalog) {
			for h := range c.Hosts {
				for s := range c.Hosts[h].Sources {
					p := &c.Hosts[h].Sources[s]
					if !strings.Contains(p.URL, "raw.githubusercontent.com") {
						p.TrackedRef = "v1"
						return
					}
				}
			}
		}},
		{"mutable baseline", func(c *sourceCatalog) {
			for h := range c.Hosts {
				for s := range c.Hosts[h].Sources {
					p := &c.Hosts[h].Sources[s]
					if strings.Contains(p.URL, "raw.githubusercontent.com") {
						p.BaselineURL = p.URL
						return
					}
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := loadCatalog(root)
			if err != nil {
				t.Fatal(err)
			}
			test.change(&catalog)
			if err := validateCatalog(root, catalog); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}

func TestNormalizeSource(t *testing.T) {
	contract := strings.Repeat("PreToolUse SessionStart contract ", 4)
	for _, test := range []struct {
		name, input, mode, want string
		invalid                 bool
	}{
		{"markdown whitespace", " \r\n" + contract + "\t", "markdown", strings.TrimSpace(contract), false},
		{"markdown code block", contract + "\r\n  ```json theme={null}  \n{\n  \"a  b\": [1,  2]\n}\t\n  ```\nNext   step.", "markdown",
			strings.TrimSpace(contract) + "\n```json theme={null}\n{\n  \"a  b\": [1,  2]\n}\n```\nNext step.", false},
		{"markdown unclosed code block", contract + "\n~~~\nif x:\n    y()", "markdown", strings.TrimSpace(contract) + "\n~~~\nif x:\n    y()", false},
		{"source whitespace", " \n" + contract + " \r\n  next();\t\n", "source", strings.TrimRight(contract, " ") + "\n  next();", false},
		{"HTML main", "<nav>navigation changes</nav><main><script>dynamic()</script><style>body{}</style><!-- generated --><p>" + contract + "</p>&amp;</main>", "html", strings.TrimSpace(contract) + " &", false},
		{"error HTML", "<!doctype html><html>" + contract + "</html>", "source", "", true},
		{"short", "error", "markdown", "", true},
		{"empty", "", "source", "", true},
		{"invalid UTF8", "\xff" + contract, "source", "", true},
		{"NUL", "\x00" + contract, "source", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := normalizeSource([]byte(test.input), test.mode)
			if test.invalid {
				if err == nil {
					t.Fatal("invalid source accepted")
				}
				return
			}
			if err != nil || string(actual) != test.want {
				t.Fatalf("got %q, %v; want %q", actual, err, test.want)
			}
		})
	}
}

func TestSourceHTTPBoundary(t *testing.T) {
	body := strings.Repeat("PreToolUse SessionStart contract ", 4)
	expected := digest([]byte(strings.TrimSpace(body)))
	for _, test := range []struct {
		name, body string
		code       int
		want       string
		missing    bool
	}{
		{"unchanged", body, 200, "unchanged", false},
		{"changed", body + "new signature", 200, "changed", false},
		{"missing marker", strings.ReplaceAll(body, "PreToolUse", "OtherEvent"), 200, "changed", true},
		{"upstream failure", body, 503, "unavailable", false},
		{"oversize", strings.Repeat("x", maxSourceBytes+1), 200, "unavailable", false},
		{"malformed", "<html>" + body + "</html>", 200, "unavailable", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("User-Agent") != "Reconc-host-contract-review/1" {
					t.Error("unexpected source request")
				}
				w.WriteHeader(test.code)
				if _, err := io.WriteString(w, test.body); err != nil && test.name != "oversize" {
					t.Error(err)
				}
			}))
			defer server.Close()
			probe := sourceProbe{ID: "hooks", URL: server.URL, Normalization: "markdown", SHA256: expected,
				RequiredMarkers: []string{"PreToolUse", "SessionStart"}, Sections: []sourceSection{{Start: "PreToolUse"}}}
			if test.missing {
				probe.SHA256 = digest([]byte(strings.TrimSpace(test.body)))
			}
			result := compareSource(context.Background(), sourceClient(), "test-host", probe)
			if result.Status != test.want || (len(result.MissingMarkers) > 0) != test.missing {
				t.Fatalf("comparison: %+v", result)
			}
			if strings.Contains(result.Error, body) {
				t.Fatal("report contains source body")
			}
		})
	}
}

func TestSourceTimeoutAndRedirect(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		result := compareSource(ctx, sourceClient(), "test-host", sourceProbe{URL: server.URL, Normalization: "source"})
		if result.Status != "unavailable" || !strings.Contains(result.Error, "deadline") {
			t.Fatalf("timeout lost: %+v", result)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://localhost/forbidden", http.StatusFound)
		}))
		defer server.Close()
		result := compareSource(context.Background(), sourceClient(), "test-host", sourceProbe{URL: server.URL, Normalization: "source"})
		if result.Status != "unavailable" || !strings.Contains(result.Error, "HTTPS") {
			t.Fatalf("unsafe redirect admitted: %+v", result)
		}
	})
}

func TestSourceURLRestrictions(t *testing.T) {
	credentialURL := url.URL{Scheme: "https", Host: "cursor.com", Path: "/docs/hooks", User: url.UserPassword("user", "pass")}
	for _, raw := range []string{"http://cursor.com/docs/hooks", credentialURL.String(), "https://cursor.com:443/docs/hooks", "https://cursor.com/docs/hooks?token=x", "https://cursor.com/docs/hooks#anchor", "https://cursor.com.example.org/hooks", "://invalid"} {
		t.Run(raw, func(t *testing.T) {
			if validateSourceURL(raw) == nil {
				t.Fatal("invalid source URL accepted")
			}
		})
	}
}

func TestCatalogStrictJSON(t *testing.T) {
	for _, content := range []string{`{"unknown":true}`, `{} {}`, `{"schema":`} {
		root := t.TempDir()
		path := filepath.Join(root, "scripts/audits/host-contracts")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "sources.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCatalog(root); err == nil {
			t.Fatalf("invalid JSON accepted: %s", content)
		}
	}
}

func TestReportDistinguishesIncompleteAndChanged(t *testing.T) {
	result := report{Complete: true}
	result.add(comparison{Status: "changed"})
	result.add(comparison{Status: "unavailable"})
	if result.Complete || !result.ReviewRequired {
		t.Fatalf("incomplete result hid drift: %+v", result)
	}
	var stdout, stderr bytes.Buffer
	if run(context.Background(), []string{"unexpected"}, &stdout, &stderr) != 1 {
		t.Fatal("unexpected argument accepted")
	}
	var decoded report
	stdout.Reset()
	if run(context.Background(), []string{"--root", repositoryRoot(t)}, &stdout, &stderr) != 0 {
		t.Fatal(stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != "reconc-host-contract-report" || decoded.FormatVersion != 1 {
		t.Fatalf("invalid report identity: %+v", decoded)
	}
}

// The digest covers only the contract sections: an edit elsewhere on the page
// stays green, an edit inside a section or a lost section boundary turns red.
func TestSectionDigestIgnoresUnrelatedEdits(t *testing.T) {
	page := func(intro, decision, footer string) string {
		return intro + " ## Events PreToolUse runs before a tool; SessionStart runs once. The decision field accepts " +
			decision + ". ## Footer " + footer
	}
	original := page("Hooks let you automate work.", `"allow" or "deny"`, "About us. Privacy.")
	sections := []sourceSection{{Start: "## Events", End: "## Footer"}}
	normalized, err := normalizeSource([]byte(original), "markdown")
	if err != nil {
		t.Fatal(err)
	}
	contract, missing := contractSections(normalized, sections)
	if len(missing) != 0 {
		t.Fatalf("missing sections: %v", missing)
	}
	expected := contractDigest(contract, "markdown")
	for _, test := range []struct {
		name, body, want string
		missing          []string
	}{
		{"unchanged", original, "unchanged", nil},
		{"unrelated edit", page("Hooks automate repetitive work in many teams.", `"allow" or "deny"`, "Careers. Terms. Status."), "unchanged", nil},
		{"contract edit", page("Hooks let you automate work.", `"allow", "deny" or "defer"`, "About us. Privacy."), "changed", nil},
		// A lost boundary drops its section, and with it the markers inside.
		{"lost section end", strings.ReplaceAll(original, "## Footer", "## Legal"), "changed", []string{"## Footer", "PreToolUse", "SessionStart"}},
		{"marker left the section", strings.Replace(original, "SessionStart", "Start", 1), "changed", []string{"SessionStart"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.WriteString(w, test.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			probe := sourceProbe{ID: "hooks", URL: server.URL, Normalization: "markdown", SHA256: expected,
				RequiredMarkers: []string{"PreToolUse", "SessionStart"}, Sections: sections}
			result := compareSource(context.Background(), sourceClient(), "test-host", probe)
			if result.Status != test.want || strings.Join(result.MissingMarkers, ",") != strings.Join(test.missing, ",") {
				t.Fatalf("comparison: %+v", result)
			}
		})
	}
}

// A documentation contract ignores only presentation the markdown
// specifications define as meaningless: a fenced code block's info string and
// the number of hyphens in a table delimiter cell. Every other character,
// including escapes, operators, type syntax, quoting, and alignment colons,
// still turns the source red. Source code stays pinned by its exact text.
func TestDocumentationContractDigestIgnoresOnlyMeaninglessPresentation(t *testing.T) {
	page := func(output string) string { return "Intro text. ## Output " + output + " ## Footer About us." }
	original := page("> \\[!NOTE]\nReturn `decision` when `status != 0`.\n| Field | Type |\n| --- | :--- |\n| `decision` | `string[]` |\n|---|---|\n```json\n{\"decision\": \"block\", \"note\": \"say \\\"hi\\\"\"}\n|-|\n```yaml\n```\nExit code 2 blocks the call; use `a\\|b` and \\*literal\\*.")
	sections := []sourceSection{{Start: "## Output", End: "## Footer"}}
	normalized, err := normalizeSource([]byte(original), "markdown")
	if err != nil {
		t.Fatal(err)
	}
	contract, missing := contractSections(normalized, sections)
	if len(missing) != 0 {
		t.Fatalf("missing sections: %v", missing)
	}
	expected := contractDigest(contract, "markdown")
	for _, test := range []struct{ name, body, want string }{
		{"fence without language", strings.Replace(original, "```json", "```", 1), "unchanged"},
		{"fence with another language", strings.Replace(original, "```json", "```jsonc", 1), "unchanged"},
		{"shorter delimiter cells", strings.Replace(original, "| --- | :--- |", "| - | :- |", 1), "unchanged"},
		{"compact delimiter row", strings.Replace(original, "|---|---|", "|-|-----|", 1), "unchanged"},
		{"hyphens inside a code block", strings.Replace(original, "\n|-|\n", "\n|--|\n", 1), "changed"},
		{"fence-like text inside a code block", strings.Replace(original, "```yaml", "```", 1), "changed"},
		{"comparison operator", strings.Replace(original, "!= 0", "== 0", 1), "changed"},
		{"array type", strings.Replace(original, "`string[]`", "`string`", 1), "changed"},
		{"quoted value", strings.Replace(original, `"block"`, "block", 1), "changed"},
		{"escaped quote in code", strings.Replace(original, `\"hi\"`, `"hi"`, 1), "changed"},
		{"escaped pipe in code", strings.Replace(original, "`a\\|b`", "`a|b`", 1), "changed"},
		{"escaped emphasis", strings.Replace(original, "\\*literal\\*", "*literal*", 1), "changed"},
		{"escaped alert marker", strings.Replace(original, "\\[!NOTE]", "[!NOTE]", 1), "changed"},
		{"alignment colon", strings.Replace(original, ":---", "---", 1), "changed"},
		{"space before punctuation", strings.Replace(original, "`status != 0`.", "`status != 0` .", 1), "changed"},
		{"changed word", strings.Replace(original, "Exit code 2", "Exit code 1", 1), "changed"},
		{"fence info attributes", strings.Replace(original, "```json\n", "```json theme={null}\n", 1), "unchanged"},
		{"reflowed prose", strings.Replace(original, "Intro text. ## Output > ", "Intro text.\n\n## Output\n> ", 1), "unchanged"},
		{"space inside a code string", strings.Replace(original, `"say \"hi\""`, `"say  \"hi\""`, 1), "changed"},
		{"code indentation", strings.Replace(original, "\n{\"decision\"", "\n    {\"decision\"", 1), "changed"},
		{"code line break", strings.Replace(original, `"block", "note"`, "\"block\",\n\"note\"", 1), "changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.body == original {
				t.Fatal("the variant did not change the page")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.WriteString(w, test.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			probe := sourceProbe{ID: "hooks", URL: server.URL, Normalization: "markdown", SHA256: expected,
				RequiredMarkers: []string{"decision"}, Sections: sections}
			if result := compareSource(context.Background(), sourceClient(), "test-host", probe); result.Status != test.want {
				t.Fatalf("comparison: %+v, want %s", result, test.want)
			}
		})
	}
	for _, mode := range []string{"source", "html"} {
		bare := []byte(strings.Replace(string(contract), "```json", "```", 1))
		if contractDigest(contract, mode) == contractDigest(bare, mode) {
			t.Fatalf("a %s contract ignored a changed fence tag", mode)
		}
	}
}
