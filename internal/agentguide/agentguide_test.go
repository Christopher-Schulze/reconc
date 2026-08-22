package agentguide

import (
	"strings"
	"testing"
)

func TestMarkdownNonEmpty(t *testing.T) {
	if strings.TrimSpace(Markdown()) == "" {
		t.Error("embedded guide is empty")
	}
}

func TestSectionsAndSectionRoundTrip(t *testing.T) {
	sections := Sections()
	if len(sections) == 0 {
		t.Fatal("embedded guide has no top-level sections")
	}
	for _, section := range sections {
		if strings.TrimSpace(section) == "" {
			t.Fatal("section inventory contains an empty heading")
		}
		body := Section(section)
		if !strings.HasPrefix(body, "## "+section+"\n") {
			t.Fatalf("section %q did not round-trip through the parser", section)
		}
	}
	if len(sections) > 1 && strings.Contains(Section(sections[0]), "\n## "+sections[1]+"\n") {
		t.Error("first section body bled into the next top-level section")
	}
}

func TestSectionCaseInsensitive(t *testing.T) {
	sections := Sections()
	if len(sections) == 0 {
		t.Fatal("embedded guide has no top-level sections")
	}
	want := Section(sections[0])
	if got := Section(strings.ToLower(sections[0])); got != want {
		t.Error("case-insensitive section lookup changed the selected body")
	}
}

func TestLeafSectionStopsAtHigherLevelHeading(t *testing.T) {
	body := Section("On Block: Get a Fix Plan")
	if !strings.HasPrefix(body, "### On Block: Get a Fix Plan\n") {
		t.Fatalf("unexpected leaf section: %q", body)
	}
	if strings.Contains(body, "\n## Inspecting Rules\n") || strings.Contains(body, "\n### Render Human-Readable Explanation\n") {
		t.Fatalf("leaf section crossed its next equal-or-higher heading:\n%s", body)
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
