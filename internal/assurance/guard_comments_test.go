package assurance

import (
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestGuardCommentOnlyLines(t *testing.T) {
	tests := []struct {
		name      string
		extension string
		lines     []string
		want      []bool
	}{
		{
			name:      "slash and block comments",
			extension: ".go",
			lines: []string{
				"// GuardedClient",
				"/* GuardedClient",
				" * still a comment",
				" */",
				"*ptr = dangerousCall()",
				"* ptr = dangerousCall()",
				"value * dangerousCall()",
				"/* comment */ dangerousCall()",
				"/* comment */ // GuardedClient",
			},
			want: []bool{true, true, true, true, false, false, false, false, true},
		},
		{
			name:      "hash comments are language specific",
			extension: ".py",
			lines:     []string{"# GuardedClient", "dangerousCall()"},
			want:      []bool{true, false},
		},
		{
			name:      "hash is code in Go",
			extension: ".go",
			lines:     []string{"# GuardedClient"},
			want:      []bool{false},
		},
		{
			name:      "PHP attribute is code",
			extension: ".php",
			lines:     []string{"#[GuardedClient]", "# GuardedClient"},
			want:      []bool{false, true},
		},
		{
			name:      "HTML block",
			extension: ".html",
			lines:     []string{"<!--", "dangerousCall()", "--> dangerousCall()"},
			want:      []bool{true, true, false},
		},
		{
			name:      "HEEx block",
			extension: ".heex",
			lines:     []string{"<%!--", "dangerousCall()", "--%>"},
			want:      []bool{true, true, true},
		},
		{
			name:      "PowerShell block",
			extension: ".ps1",
			lines:     []string{"<#", "dangerousCall()", "#>"},
			want:      []bool{true, true, true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := guardCommentOnlyLines(test.extension, test.lines); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("guardCommentOnlyLines() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGuardBoundaryDetectsSiteAtFirstNonWhitespaceCharacter(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "dereference", line: "*ptr = dangerousCall()"},
		{name: "spaced dereference", line: "* ptr = dangerousCall()"},
		{name: "indented dereference", line: "\t*ptr = dangerousCall()"},
		{name: "multiplication", line: "result := left * dangerousCall()"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeAssuranceFile(t, root, "src/x.go", "package x\n"+test.line+"\n")
			gate := policy.AssuranceGate{
				ID: "process", Type: policy.AssuranceProcessBoundary,
				ScanPaths: []string{"src/**"}, SitePatterns: []string{"dangerousCall("},
				GuardMarkers: []string{"GuardedCall"}, MarkerWindowLines: 2,
			}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || !strings.Contains(findings[0].Message, "src/x.go:2") {
				t.Fatalf("leading code site was hidden: %+v", findings)
			}
		})
	}
}

func TestGuardBoundaryRetainsBlockCommentBehavior(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/x.go", "package x\n/*\n * dangerousCall()\n */\ndangerousCall()\n")
	gate := policy.AssuranceGate{
		ID: "process", Type: policy.AssuranceProcessBoundary,
		ScanPaths: []string{"src/**"}, SitePatterns: []string{"dangerousCall("},
		GuardMarkers: []string{"GuardedCall"}, MarkerWindowLines: 2,
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/x.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "src/x.go:5") {
		t.Fatalf("block comment site handling changed: %+v", findings)
	}
}
