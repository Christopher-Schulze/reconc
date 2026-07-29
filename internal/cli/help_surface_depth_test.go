package cli

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
)

func TestEveryLeafCommandRendersHelpWithoutRepositorySideEffects(t *testing.T) {
	for _, command := range publicLeafCommandPaths() {
		name := strings.Join(command, " ")
		t.Run(name, func(t *testing.T) {
			argv := append(append([]string{}, command...), "--help")
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := Run(argv, "test-version", &stdout, &stderr); err != nil {
				t.Fatalf("%s --help failed: %v", name, err)
			}
			output := stdout.String() + stderr.String()
			if !strings.Contains(strings.ToLower(output), "usage:") {
				t.Fatalf("%s --help omitted usage text: stdout=%q stderr=%q", name, stdout.String(), stderr.String())
			}
		})
	}
}

func TestPublicHookHelpDoesNotAdvertiseInternalRoutes(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"hook", "--help"}, "test-version", &stdout, &stderr); err != nil {
		t.Fatalf("hook --help failed: %v", err)
	}
	output := stdout.String() + stderr.String()
	for _, internalRoute := range []string{"hook runtime", "grok-pre-tool-guard"} {
		if strings.Contains(output, internalRoute) {
			t.Fatalf("public hook help exposed internal route %q: %s", internalRoute, output)
		}
	}
}

func publicLeafCommandPaths() [][]string {
	paths := [][]string{}
	var walk func([]string, []commandmeta.Subcommand)
	walk = func(prefix []string, commands []commandmeta.Subcommand) {
		for _, command := range commands {
			if command.Stability == commandmeta.StabilityInternal {
				continue
			}
			path := append(append([]string{}, prefix...), command.Name)
			if len(command.Subcommands) == 0 {
				paths = append(paths, path)
				continue
			}
			walk(path, command.Subcommands)
		}
	}
	for _, command := range commandmeta.All() {
		path := []string{command.Name}
		if len(command.Subcommands) == 0 {
			paths = append(paths, path)
			continue
		}
		walk(path, command.Subcommands)
	}
	return paths
}

func publicCommandGroupPaths() [][]string {
	paths := [][]string{}
	var walk func([]string, []commandmeta.Subcommand)
	walk = func(prefix []string, commands []commandmeta.Subcommand) {
		for _, command := range commands {
			if command.Stability == commandmeta.StabilityInternal || len(command.Subcommands) == 0 {
				continue
			}
			path := append(append([]string{}, prefix...), command.Name)
			paths = append(paths, path)
			walk(path, command.Subcommands)
		}
	}
	for _, command := range commandmeta.All() {
		if len(command.Subcommands) == 0 {
			continue
		}
		path := []string{command.Name}
		paths = append(paths, path)
		walk(path, command.Subcommands)
	}
	return paths
}
