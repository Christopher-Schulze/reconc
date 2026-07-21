package manpage

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/schema"
)

func TestRenderContainsHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "0.9.9"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, ".TH RECONC 1") {
		t.Error("missing .TH header")
	}
	if !strings.Contains(out, `"reconc 0.9.9"`) {
		t.Error("version not embedded in .TH line")
	}
}

func TestRenderUsesCanonicalSchemaBase(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "0.9.9"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), schema.DefaultBaseURL) {
		t.Fatalf("man page omitted canonical schema base %q", schema.DefaultBaseURL)
	}
	if !strings.Contains(buf.String(), schema.PolicyLockBaseURL) {
		t.Fatalf("man page omitted canonical policy-lock schema base %q", schema.PolicyLockBaseURL)
	}
	if !strings.Contains(buf.String(), "RECONC_GROK_STEER") {
		t.Fatal("man page omitted Grok steering control")
	}
	if !strings.Contains(buf.String(), "NO_COLOR") {
		t.Fatal("man page omitted ANSI opt-out")
	}
}

func TestRenderIncludesStandardSections(t *testing.T) {
	var buf bytes.Buffer
	_ = Render(&buf, "0.2.0")
	out := buf.String()
	for _, section := range []string{
		".SH NAME", ".SH SYNOPSIS", ".SH DESCRIPTION",
		".SH EXIT STATUS", ".SH SUBCOMMANDS", ".SH ENVIRONMENT",
		".SH FILES", ".SH SEE ALSO", ".SH BUGS",
	} {
		if !strings.Contains(out, section) {
			t.Errorf("missing section %q", section)
		}
	}
}

func TestRenderIncludesEverySubcommand(t *testing.T) {
	var buf bytes.Buffer
	_ = Render(&buf, "0.2.0")
	out := buf.String()
	// Spot-check a representative selection.
	for _, sub := range []string{"compile", "check", "bootstrap", "audit", "run", "manpage", "agent-intro"} {
		if !strings.Contains(out, ".B "+sub) {
			t.Errorf("subcommand %q missing from man page", sub)
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
	if !strings.Contains(first.String(), `.TH RECONC 1 "2000-01-01"`) {
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
