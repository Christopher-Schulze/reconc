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

func TestHelpRoutesToExactCommandPath(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "command", argv: []string{"help", "update"}, want: "Usage: reconc update"},
		{name: "nested", argv: []string{"help", "hook", "install"}, want: "Usage: reconc hook install"},
		{name: "flag before command", argv: []string{"--help", "task", "recover"}, want: "reconc task recover"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := Run(test.argv, "test-version", &stdout, &stderr); err != nil {
				t.Fatalf("%v failed: %v", test.argv, err)
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("%v rendered wrong help: %q", test.argv, stdout.String())
			}
			if strings.Contains(stdout.String(), "Repository Control Compiler") {
				t.Fatalf("%v fell back to root help: %q", test.argv, stdout.String())
			}
			if test.name == "flag before command" && strings.Contains(stdout.String(), "reconc task status") {
				t.Fatalf("%v leaked sibling command help: %q", test.argv, stdout.String())
			}
		})
	}
}

func TestHelpRejectsUnknownTarget(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Run([]string{"help", "nonsense"}, "test-version", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), `unknown subcommand "nonsense"`) {
		t.Fatalf("unknown help target = output %q, error %v", stdout.String(), err)
	}
}

func TestHelpRejectsUnknownNestedTarget(t *testing.T) {
	for _, test := range []struct {
		argv []string
		want string
	}{
		{argv: []string{"help", "task", "nonsense"}, want: `unknown target "task nonsense"`},
		{argv: []string{"help", "task", "recover", "nonsense"}, want: `unknown target "task recover nonsense"`},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		err := Run(test.argv, "test-version", &stdout, &stderr)
		if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("unknown nested help target %v = output %q, error %v", test.argv, stdout.String(), err)
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
