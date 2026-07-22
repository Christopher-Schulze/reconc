package manpage

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/schema"
)

func TestRenderContainsSuppliedVersion(t *testing.T) {
	var buf bytes.Buffer
	version := "version-sentinel-91"
	if err := Render(&buf, version); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		t.Fatal("rendered man page is empty")
	}
	if !strings.Contains(out, version) {
		t.Error("rendered man page lost the supplied version")
	}
	if !strings.HasPrefix(out, ".TH RECONC 1 ") {
		t.Error("rendered man page has no valid RECONC section-1 header")
	}
}

func TestRenderUsesCanonicalSchemaURLs(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	for _, schemaURL := range []string{schema.DefaultBaseURL, schema.PolicyLockBaseURL} {
		if !strings.Contains(buf.String(), schemaURL) {
			t.Errorf("man page omitted canonical schema URL %q", schemaURL)
		}
	}
}

func TestRenderIncludesStandardSections(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{
		".SH NAME", ".SH SYNOPSIS", ".SH DESCRIPTION", ".SH EXIT STATUS",
		".SH SUBCOMMANDS", ".SH ENVIRONMENT", ".SH FILES", ".SH SEE ALSO", ".SH BUGS",
	} {
		if !strings.Contains(buf.String(), section) {
			t.Errorf("man page omitted standard section %q", section)
		}
	}
}

func TestRenderIncludesCanonicalCommandAndNestedInventory(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "0")
	var buf bytes.Buffer
	if err := Render(&buf, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, command := range commandmeta.All() {
		if !strings.Contains(out, ".B "+command.Name+"\n") || !strings.Contains(out, command.Summary) {
			t.Errorf("man page omitted canonical command %s", command.Name)
		}
		for _, nested := range command.Subcommands {
			if !strings.Contains(out, ".B "+command.Name+" "+nested.Name+"\n") || !strings.Contains(out, nested.Summary) {
				t.Errorf("man page omitted canonical nested command %s %s", command.Name, nested.Name)
			}
		}
	}
}

func TestRenderUsesSourceDateEpochDeterministically(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "946684800")
	var first, second bytes.Buffer
	if err := Render(&first, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := Render(&second, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("man page output changed with a fixed SOURCE_DATE_EPOCH")
	}
	if !strings.Contains(first.String(), "2000-01-01") {
		t.Fatalf("man page did not use SOURCE_DATE_EPOCH: %s", first.String()[:80])
	}
}

func TestRenderRejectsInvalidSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "not-an-epoch")
	if err := Render(&bytes.Buffer{}, "0.2.0"); err == nil || !strings.Contains(err.Error(), "SOURCE_DATE_EPOCH") {
		t.Fatalf("expected invalid SOURCE_DATE_EPOCH error, got %v", err)
	}
}

func TestEscapeRoffLeadingHyphen(t *testing.T) {
	// Descriptions starting with hyphen would trip strict groff
	// parsers if not escaped.
	got := escapeRoff("-flag")
	if !strings.HasPrefix(got, `\-`) {
		t.Errorf("expected leading hyphen escaped; got %q", got)
	}
}

func TestEscapeRoffBackslash(t *testing.T) {
	got := escapeRoff(`a\b`)
	if got != `a\\b` {
		t.Errorf("expected backslash doubled; got %q", got)
	}
}
