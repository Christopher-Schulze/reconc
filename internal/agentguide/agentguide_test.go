package agentguide

import (
	"strings"
	"testing"
)

func TestMarkdownNonEmpty(t *testing.T) {
	md := Markdown()
	if len(md) < 500 {
		t.Errorf("guide suspiciously short: %d bytes", len(md))
	}
	if !strings.Contains(md, "# reconc") {
		t.Errorf("guide missing top-level heading")
	}
}

func TestSectionsReturnsTopLevel(t *testing.T) {
	sections := Sections()
	if len(sections) < 5 {
		t.Errorf("expected at least 5 sections, got %d: %v", len(sections), sections)
	}
	for _, want := range []string{"Rule Kinds", "Exit Codes (Stable Contract)", "Golden Rules"} {
		found := false
		for _, s := range sections {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected section %q in list; got %v", want, sections)
		}
	}
}

func TestSectionByExactHeading(t *testing.T) {
	body := Section("Exit Codes")
	if body == "" {
		t.Fatal("expected section body, got empty string")
	}
	if !strings.HasPrefix(body, "## Exit Codes") {
		t.Errorf("expected body to start with the heading line, got: %s", body[:80])
	}
	if !strings.Contains(body, "`0`") {
		t.Errorf("expected exit-code body to mention exit 0, got: %s", body)
	}
	// Must not bleed into next section.
	if strings.Contains(body, "## Rule Kinds") {
		t.Errorf("section body bled into next section")
	}
}

func TestSectionCaseInsensitive(t *testing.T) {
	body := Section("golden rules")
	if body == "" {
		t.Fatal("expected case-insensitive match")
	}
	if !strings.Contains(body, "Never paraphrase policy") {
		t.Errorf("expected golden-rules content")
	}
}

func TestSectionNotFound(t *testing.T) {
	body := Section("this section definitely does not exist")
	if body != "" {
		t.Errorf("expected empty string for missing section, got: %s", body)
	}
}

func TestSectionEmptyNameReturnsFullDoc(t *testing.T) {
	if Section("") != Markdown() {
		t.Error("empty section name should return full markdown")
	}
}
