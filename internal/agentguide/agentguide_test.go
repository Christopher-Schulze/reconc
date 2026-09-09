package agentguide

import (
	"strings"
	"testing"
)

func TestMarkdownNonEmpty(t *testing.T) {
	if strings.TrimSpace(Markdown()) == "" {
		t.Error("embedded guide is empty")
	}
	if len(Markdown()) > CoreByteBudget {
		t.Fatalf("default guide is %d bytes, budget is %d", len(Markdown()), CoreByteBudget)
	}
	if len(FullMarkdown()) <= len(Markdown()) {
		t.Fatal("full guide should retain lazy reference material")
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
	if Section("") != FullMarkdown() {
		t.Error("empty section name should return full markdown")
	}
}

func TestSectionCatalogUsesStableIDs(t *testing.T) {
	catalog := SectionCatalog()
	if len(catalog) == 0 {
		t.Fatal("lazy section catalog is empty")
	}
	seen := make(map[string]bool, len(catalog))
	for _, section := range catalog {
		if section.ID == "" || section.Title == "" || seen[section.ID] {
			t.Fatalf("invalid or duplicate section descriptor: %+v", section)
		}
		seen[section.ID] = true
		body := Section(section.ID)
		if !strings.HasPrefix(body, "## ") {
			t.Fatalf("stable id %q did not resolve to a top-level section", section.ID)
		}
	}
	if got := Section("platform-integration"); !strings.Contains(got, "hook status") {
		t.Fatal("platform-integration stable id did not resolve its contract")
	}
}

func TestCoreWorkflowSupportsDecisionWalkthroughs(t *testing.T) {
	core := Markdown()
	for _, token := range []string{
		"session-briefing . --json",
		"argv",
		"recommended_action",
		"reconc next .",
		"reconc done .",
		"reconc hook status . --json",
	} {
		if !strings.Contains(core, token) {
			t.Errorf("core workflow omits %q", token)
		}
	}
	ordered := []string{"reconc session-briefing . --json", "reconc check . --write", "reconc next .", "reconc done ."}
	last := -1
	for _, token := range ordered {
		index := strings.Index(core, token)
		if index <= last {
			t.Fatalf("core workflow order invalid at %q", token)
		}
		last = index
	}
}
