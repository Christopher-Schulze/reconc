package main

import "testing"

func TestLineCommentDistinguishesGoLiteralsFromRealComments(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "interpreted URL", line: `endpoint := "https://example.com/TODO"`},
		{name: "escaped quote", line: `value := "escaped \" // TODO inside"`},
		{name: "raw URL", line: "endpoint := `https://example.com/FIXME`"},
		{name: "rune slash", line: `slash := '/'`},
		{name: "trailing comment", line: `endpoint := "https://example.com" // TODO remove`, want: " TODO remove"},
		{name: "after raw string", line: "value := `// inside` // FIXME outside", want: " FIXME outside"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := lineComment(test.line); got != test.want {
				t.Fatalf("lineComment(%q) = %q, want %q", test.line, got, test.want)
			}
		})
	}
}

func TestAuditAddedLineQualityAllowsURLsAndRejectsRealMarkers(t *testing.T) {
	if failures := auditAddedLineQuality("backend/project/service.go", `endpoint := "https://example.com/TODO"`); len(failures) != 0 {
		t.Fatalf("URL literal was treated as a comment: %v", failures)
	}
	if failures := auditAddedLineQuality("backend/project/service.go", `endpoint := "https://example.com" // TODO remove`); len(failures) != 1 {
		t.Fatalf("real prohibited comment was not rejected exactly once: %v", failures)
	}
}
