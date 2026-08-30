package impactlab

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeActionsRedactsEveryAbsolutePathSpan(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		want       string
		redactions int
	}{
		{
			name:       "multiple punctuated POSIX paths",
			action:     `inspect prefix:/private/one,/private/two suffix`,
			want:       `inspect prefix:<path>,<path> suffix`,
			redactions: 2,
		},
		{
			name:       "multiple Windows drive and UNC paths",
			action:     `paths=(C:\Users\Alice\one),[D:/secret/two]; unc=\\server\share\three`,
			want:       `paths=(<path>),[<path>]; unc=<path>`,
			redactions: 3,
		},
		{
			name:       "unicode around punctuated paths",
			action:     `label=über:/private/δ,/var/β sentence./opt/γ uri=file:///private/cache`,
			want:       `label=über:<path>,<path> sentence.<path> uri=<path>`,
			redactions: 4,
		},
		{
			name:       "relative paths URLs and punctuation remain stable",
			action:     `compare docs/a:b https://example.test/private/a ./relative`,
			want:       `compare docs/a:b https://example.test/private/a ./relative`,
			redactions: 0,
		},
		{
			name:       "repository path remains portable",
			action:     `open /work/repo/docs/file`,
			want:       `open ./docs/file`,
			redactions: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, redactions := sanitizeActions("/work/repo", []string{test.action})
			if len(got) != 1 || got[0] != test.want || redactions != test.redactions {
				t.Fatalf("sanitizeActions() = (%q, %d), want (%q, %d)", got, redactions, test.want, test.redactions)
			}
			second, secondRedactions := sanitizeActions("/work/repo", []string{test.action})
			if second[0] != got[0] || secondRedactions != redactions {
				t.Fatalf("sanitizeActions() is not deterministic: (%q, %d) != (%q, %d)", second, secondRedactions, got, redactions)
			}
			if len(got[0]) > maxValueBytes || !utf8.ValidString(got[0]) || strings.TrimSpace(got[0]) != got[0] {
				t.Fatalf("sanitizeActions() returned invalid bounded text %q", got[0])
			}
		})
	}

	bounded, redactions := sanitizeActions("/work/repo", []string{strings.Repeat("/a,", maxValueBytes)})
	if len(bounded) != 1 || len(bounded[0]) > maxValueBytes || !utf8.ValidString(bounded[0]) ||
		strings.Contains(bounded[0], "/a") || redactions == 0 {
		t.Fatalf("bounded sanitizeActions() = (%q, %d)", bounded, redactions)
	}
}
