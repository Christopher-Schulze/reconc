package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplayCells(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "ASCII", text: "reconc", want: 6},
		{name: "CJK", text: "界面", want: 4},
		{name: "combining", text: "e\u0301", want: 1},
		{name: "text variation", text: "\u2764\ufe0e", want: 1},
		{name: "emoji variation", text: "\u2764\ufe0f", want: 2},
		{name: "emoji modifier", text: "👍🏽", want: 2},
		{name: "keycap", text: "1\ufe0f\u20e3", want: 2},
		{name: "regional indicator pair", text: "🇩🇪", want: 2},
		{name: "ZWJ sequence", text: "👩‍💻", want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := displayCells(test.text); got != test.want {
				t.Fatalf("displayCells(%q) = %d, want %d", test.text, got, test.want)
			}
		})
	}
}

func TestTruncateTextCells(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{name: "ASCII ellipsis", text: "abcdefg", limit: 6, want: "abc..."},
		{name: "CJK ellipsis", text: "界界界", limit: 5, want: "界..."},
		{name: "combining cluster", text: "e\u0301e\u0301e\u0301e\u0301e\u0301", limit: 4, want: "e\u0301..."},
		{name: "variation cluster", text: "\u2764\ufe0f\u2764\ufe0f\u2764\ufe0f", limit: 5, want: "\u2764\ufe0f..."},
		{name: "emoji modifier cluster", text: "👍🏽x", limit: 2, want: "👍🏽"},
		{name: "ZWJ cluster", text: "👩‍💻x", limit: 2, want: "👩‍💻"},
		{name: "width one ASCII", text: "abcd", limit: 1, want: "a"},
		{name: "width one rejects wide cluster", text: "界a", limit: 1, want: ""},
		{name: "width two", text: "界a", limit: 2, want: "界"},
		{name: "width three", text: "界ab", limit: 3, want: "界a"},
		{name: "invalid UTF-8 replacement", text: string([]byte{'a', 0xff, 'b'}), limit: 3, want: "a\ufffdb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := truncateTextCells(test.text, test.limit)
			if got != test.want {
				t.Fatalf("truncateTextCells(%q, %d) = %q, want %q", test.text, test.limit, got, test.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncateTextCells returned invalid UTF-8: %q", got)
			}
			if cells := displayCells(got); cells > test.limit {
				t.Fatalf("output occupies %d cells, limit is %d: %q", cells, test.limit, got)
			}
		})
	}
}

func TestRenderTextWidthUsesTerminalCells(t *testing.T) {
	view := &View{
		RepoRoot:       "/tmp/repo",
		LockfileStatus: "界界界界界界界界界界",
		Sources:        []SourceSummary{},
		Rules:          []RuleSummary{},
		Errors:         []string{"e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301 👩‍💻"},
	}
	const limit = 20
	rendered := RenderTextWidth(view, limit)
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		if !utf8.ValidString(line) {
			t.Fatalf("rendered line is invalid UTF-8: %q", line)
		}
		if cells := displayCells(line); cells > limit {
			t.Fatalf("rendered line occupies %d cells, limit is %d: %q", cells, limit, line)
		}
	}
}
