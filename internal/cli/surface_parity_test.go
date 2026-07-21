package cli

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
)

func TestMetadataFlagsMatchParserSurfaces(t *testing.T) {
	candidates := parserFlagCandidates(t)
	probeRoot := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Chdir(probeRoot)
	valueFlags := metadataValueFlags()
	unsupported := map[string]bool{
		"bootstrap::--force":               true,
		"bootstrap:apply:--output":         true,
		"bootstrap:apply:--replace-output": true,
		"run:off:--force":                  true,
	}
	for _, command := range commandmeta.All() {
		t.Run(command.Name, func(t *testing.T) {
			assertParserFlagSet(t, command.Name, "", command.Flags, candidates, valueFlags, unsupported)
			for _, nested := range command.Subcommands {
				if nested.Stability == commandmeta.StabilityInternal {
					continue
				}
				t.Run(nested.Name, func(t *testing.T) {
					assertParserFlagSet(t, command.Name, nested.Name, nested.Flags, candidates, valueFlags, unsupported)
				})
			}
		})
	}
}

func TestMetadataFlagsAppearInHelp(t *testing.T) {
	for _, command := range commandmeta.All() {
		t.Run(command.Name, func(t *testing.T) {
			assertHelpContainsFlags(t, []string{command.Name, "--help"}, command.Name, command.Flags)
			for _, nested := range command.Subcommands {
				if nested.Stability == commandmeta.StabilityInternal {
					continue
				}
				t.Run(nested.Name, func(t *testing.T) {
					assertHelpContainsFlags(t, []string{command.Name, nested.Name, "--help"}, command.Name+" "+nested.Name, nested.Flags)
				})
			}
		})
	}
}

func assertParserFlagSet(t *testing.T, command, nested string, expected []commandmeta.Flag, candidates []string, valueFlags map[string]bool, unsupported map[string]bool) {
	t.Helper()
	want := map[string]bool{}
	for _, flag := range expected {
		want[flag.Name] = true
	}
	for _, candidate := range candidates {
		recognized := parserRecognizesFlag(t, command, nested, candidate, valueFlags[candidate])
		key := command + ":" + nested + ":" + candidate
		if unsupported[key] {
			if !recognized {
				t.Fatalf("documented unsupported parser flag %s is no longer recognized", candidate)
			}
			continue
		}
		if recognized != want[candidate] {
			t.Fatalf("parser/metadata drift for %s %s flag %s: parser=%t metadata=%t", command, nested, candidate, recognized, want[candidate])
		}
	}
}

func parserRecognizesFlag(t *testing.T, command, nested, candidate string, takesValue bool) bool {
	t.Helper()
	args := parserProbeBase(command, nested)
	args = append(args, candidate)
	if takesValue {
		args = append(args, "surface-probe-value")
	}
	const sentinel = "--surface-probe-invalid"
	args = append(args, sentinel)
	var stdout, stderr bytes.Buffer
	err := Run(args, "surface-probe", &stdout, &stderr)
	if err == nil {
		return true
	}
	message := err.Error()
	quoted := strconv.Quote(candidate)
	for _, rejection := range []string{
		"unknown flag " + quoted,
		"unknown argument " + quoted,
		"unknown subcommand " + quoted,
		"unknown shell " + quoted,
		"unexpected argument " + quoted,
		"unsupported argument " + quoted,
	} {
		if strings.Contains(message, rejection) {
			return false
		}
	}
	return true
}

func parserProbeBase(command, nested string) []string {
	args := []string{command}
	if nested != "" {
		args = append(args, nested)
	}
	key := command + ":" + nested
	switch key {
	case "assert:", "why:":
		args = append(args, "surface-probe-rule")
	case "can:":
		args = append(args, "write", "surface-probe-path")
	case "diff:":
		args = append(args, "surface-probe-a", "surface-probe-b")
	case "hook:generate", "hook:install", "hook:uninstall":
		args = append(args, "codex")
	case "hook:sync-scaffold":
		args = append(args, "surface-probe-scaffold")
	case "hook:claim":
		args = append(args, ".", "surface-probe-claim")
	case "preset:show", "template:show":
		args = append(args, "default")
	case "task:claim", "task:resume":
		args = append(args, "999")
	case "completion:":
		args = append(args, "bash")
	}
	return args
}

func parserFlagCandidates(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read CLI source directory: %v", err)
	}
	candidates := map[string]bool{}
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name != "cli.go" && !strings.HasSuffix(name, "_cmd.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse CLI source %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil || !isFlagCandidate(value) {
				return true
			}
			candidates[value] = true
			return true
		})
	}
	for _, excluded := range []string{"-h", "--help", "-V", "--version"} {
		delete(candidates, excluded)
	}
	out := make([]string, 0, len(candidates))
	for candidate := range candidates {
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

func isFlagCandidate(value string) bool {
	if !strings.HasPrefix(value, "-") || len(value) < 2 || strings.ContainsAny(value, " \t\n=/") {
		return false
	}
	trimmed := strings.TrimLeft(value, "-")
	if trimmed == "" {
		return false
	}
	for _, char := range trimmed {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func metadataValueFlags() map[string]bool {
	values := map[string]bool{}
	for _, command := range commandmeta.All() {
		for _, flag := range command.Flags {
			values[flag.Name] = values[flag.Name] || flag.Value != ""
		}
		for _, nested := range command.Subcommands {
			for _, flag := range nested.Flags {
				values[flag.Name] = values[flag.Name] || flag.Value != ""
			}
		}
	}
	return values
}

func assertHelpContainsFlags(t *testing.T, args []string, surface string, flags []commandmeta.Flag) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(args, "surface-probe", &stdout, &stderr)
	if err != nil && stdout.Len() == 0 && stderr.Len() == 0 {
		t.Fatalf("%s help failed without output: %v", surface, err)
	}
	output := stdout.String() + stderr.String()
	for _, flag := range flags {
		if !strings.Contains(output, flag.Name) {
			t.Errorf("%s help omits metadata flag %s\n%s", surface, flag.Name, output)
		}
	}
}

func TestParserProbeCoversEveryMetadataFlag(t *testing.T) {
	candidates := parserFlagCandidates(t)
	joined := " " + strings.Join(candidates, " ") + " "
	for _, command := range commandmeta.All() {
		for _, flag := range command.Flags {
			if !strings.Contains(joined, " "+flag.Name+" ") {
				t.Fatalf("metadata flag %s for %s was not found in parser source", flag.Name, command.Name)
			}
		}
		for _, nested := range command.Subcommands {
			for _, flag := range nested.Flags {
				if !strings.Contains(joined, " "+flag.Name+" ") {
					t.Fatalf("metadata flag %s for %s %s was not found in parser source", flag.Name, command.Name, nested.Name)
				}
			}
		}
	}
}

func TestSurfaceProbeBaseKeysStayUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, command := range commandmeta.All() {
		for _, nested := range append([]commandmeta.Subcommand{{}}, command.Subcommands...) {
			key := fmt.Sprintf("%s:%s", command.Name, nested.Name)
			if seen[key] {
				t.Fatalf("duplicate surface key %s", key)
			}
			seen[key] = true
		}
	}
}
