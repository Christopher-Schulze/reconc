package manpage

import (
	"bytes"
	"strings"
	"testing"
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
	for _, sub := range []string{"compile", "check", "bootstrap", "audit", "manpage", "agent-intro"} {
		if !strings.Contains(out, ".B "+sub) {
			t.Errorf("subcommand %q missing from man page", sub)
		}
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
