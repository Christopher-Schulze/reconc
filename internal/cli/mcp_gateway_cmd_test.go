package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/pathidentity"
)

func TestParseMCPGatewayOptionsKeepsDownstreamArgumentsOpaque(t *testing.T) {
	options, err := parseMCPGatewayOptions([]string{
		"repo", "--server", "tools", "--principal", "operator",
		"--allow-repository-managed-policy", "--", "/bin/tool", "--", "--server", "child",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.repository != "repo" || options.command != "/bin/tool" ||
		!reflect.DeepEqual(options.arguments, []string{"--", "--server", "child"}) {
		t.Fatalf("parsed gateway launch = %#v", options)
	}
}

func TestParseMCPGatewayOptionsRejectsAmbiguousAuthorityAndUnknownFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "both policy authorities",
			args: []string{
				"--server", "tools", "--principal", "operator",
				"--expect-lock-digest", strings.Repeat("a", 64),
				"--allow-repository-managed-policy", "--", "/bin/tool",
			},
			want: "exactly one",
		},
		{
			name: "unknown flag",
			args: []string{"--unknown", "--", "/bin/tool"},
			want: "unknown flag",
		},
		{
			name: "missing separator",
			args: []string{
				"--server", "tools", "--principal", "operator",
				"--allow-repository-managed-policy",
			},
			want: "required after --",
		},
		{
			name: "duplicated single-value flag",
			args: []string{
				"--server", "tools", "--server", "other", "--principal", "operator",
				"--allow-repository-managed-policy", "--", "/bin/tool",
			},
			want: "flag --server is duplicated",
		},
		{
			name: "flag consumed as another flag value",
			args: []string{
				"--server", "tools", "--principal", "operator", "--role",
				"--allow-repository-managed-policy", "--expect-lock-digest", strings.Repeat("a", 64),
				"--", "/bin/tool",
			},
			want: "--role requires a value before --allow-repository-managed-policy",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseMCPGatewayOptions(test.args); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMCPGatewayHelpDoesNotStartGateway(t *testing.T) {
	stdout := &bytes.Buffer{}
	if err := runMCP([]string{"gateway", "--help"}, "test", stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "Usage: reconc mcp gateway") {
		t.Fatalf("gateway help = %q", stdout.String())
	}
}

func TestNormalizeGatewayPathsReturnsExactLexicalBindings(t *testing.T) {
	repository, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repository, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repository, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink test is unavailable: %v", err)
	}
	paths, bindings, err := normalizeGatewayPaths(repository, []string{"link/future"})
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := filepath.Join(target, "future")
	if !reflect.DeepEqual(paths, []string{"target/future"}) || len(bindings) != 1 ||
		bindings[0].Lexical != filepath.Join(link, "future") ||
		bindings[0].Identity != wantIdentity {
		t.Fatalf("normalized paths = %#v, bindings = %#v", paths, bindings)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if _, _, err := normalizeGatewayPaths(repository, []string{outside}); err == nil {
		t.Fatal("lexical path outside the repository was accepted")
	}
}
