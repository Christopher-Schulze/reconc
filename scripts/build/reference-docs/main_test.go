package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/schema"
)

func TestReferencesAreDeterministicAndComplete(t *testing.T) {
	assertStableReference(t, renderCommandReference)
	assertStableReference(t, renderHookReference)
	assertStableReference(t, renderSchemaReference)

	rows, err := publicCommandRows()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(rows), publicCommandCount(commandmeta.Public()); got != want {
		t.Fatalf("command rows = %d, want %d", got, want)
	}
	publicPaths := make(map[string]bool, len(rows))
	for _, row := range rows {
		publicPaths[row.path] = true
	}
	assertInternalCommandsHidden(t, publicPaths, commandmeta.All())

	hookBody, err := renderHookReference()
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range hooks.VerificationSurfaces() {
		needle := "| `" + surface.Kind + "` | `" + surface.Surface + "` |"
		if got := bytes.Count(hookBody, []byte(needle)); got != 1 {
			t.Fatalf("hook row %s:%s appears %d times", surface.Kind, surface.Surface, got)
		}
	}

	schemaBody, err := renderSchemaReference()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range schema.Contracts() {
		needle := "| `" + string(contract.Artifact) + "` | `v" + contract.SchemaVersion + "` |"
		if got := bytes.Count(schemaBody, []byte(needle)); got != 1 {
			t.Fatalf("schema row %s/v%s appears %d times", contract.Artifact, contract.SchemaVersion, got)
		}
	}
}

func TestRunGeneratesChecksAndRefusesStaleReferences(t *testing.T) {
	root := t.TempDir()
	writeFixtureDocument(t, root, "docs/commands.md", commandBegin, commandEnd)
	writeFixtureDocument(t, root, "docs/architecture.md", hookBegin, hookEnd, schemaBegin, schemaEnd)

	if err := run(root, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true, io.Discard); err != nil {
		t.Fatalf("fresh check: %v", err)
	}

	path := filepath.Join(root, "docs", "commands.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte("## Canonical command catalog"), []byte("## stale command catalog"), 1)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true, io.Discard); err == nil || !strings.Contains(err.Error(), "docs/commands.md") {
		t.Fatalf("stale check error = %v", err)
	}
	stale, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stale, []byte("## stale command catalog")) {
		t.Fatal("check mode rewrote stale documentation")
	}
}

func TestMarkedSectionRequiresOneOrderedMarkerPair(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: "plain\n"},
		{name: "duplicate", body: commandBegin + "\n" + commandBegin + "\n" + commandEnd + "\n"},
		{name: "reversed", body: commandEnd + "\n" + commandBegin + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := replaceMarkedSection([]byte(test.body), markedSection{commandBegin, commandEnd, []byte("generated\n")})
			if err == nil {
				t.Fatal("malformed markers were accepted")
			}
		})
	}
}

func assertStableReference(t *testing.T, render func() ([]byte, error)) {
	t.Helper()
	first, err := render()
	if err != nil {
		t.Fatal(err)
	}
	second, err := render()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("reference rendering is not deterministic")
	}
}

func publicCommandCount(commands []commandmeta.Command) int {
	count := len(commands)
	var countSubcommands func([]commandmeta.Subcommand)
	countSubcommands = func(subcommands []commandmeta.Subcommand) {
		count += len(subcommands)
		for _, subcommand := range subcommands {
			countSubcommands(subcommand.Subcommands)
		}
	}
	for _, command := range commands {
		countSubcommands(command.Subcommands)
	}
	return count
}

func assertInternalCommandsHidden(t *testing.T, publicPaths map[string]bool, commands []commandmeta.Command) {
	t.Helper()
	var checkSubcommands func(string, []commandmeta.Subcommand)
	checkSubcommands = func(parent string, subcommands []commandmeta.Subcommand) {
		for _, subcommand := range subcommands {
			path := parent + " " + subcommand.Name
			if subcommand.Stability == commandmeta.StabilityInternal && publicPaths[path] {
				t.Fatalf("internal command leaked into reference: %s", path)
			}
			checkSubcommands(path, subcommand.Subcommands)
		}
	}
	for _, command := range commands {
		path := "reconc " + command.Name
		if command.Stability == commandmeta.StabilityInternal && publicPaths[path] {
			t.Fatalf("internal command leaked into reference: %s", path)
		}
		checkSubcommands(path, command.Subcommands)
	}
}

func writeFixtureDocument(t *testing.T, root, relative string, markers ...string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# fixture\n\n" + strings.Join(markers, "\nstale\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
