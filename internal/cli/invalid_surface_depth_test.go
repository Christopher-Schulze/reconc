package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestEveryLeafCommandRejectsUnknownFlagsWithoutSideEffects(t *testing.T) {
	for _, command := range publicLeafCommandPaths() {
		name := strings.Join(command, " ")
		t.Run(name, func(t *testing.T) {
			argv := append(append([]string{}, command...), "--definitely-unknown")
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := Run(argv, "test-version", &stdout, &stderr)
			if err == nil {
				t.Fatalf("%s accepted an unknown flag; stdout=%q stderr=%q", name, stdout.String(), stderr.String())
			}
			if ExitCode(err) == 0 {
				t.Fatalf("%s returned a zero exit code for %v", name, err)
			}
		})
	}
}

func TestEveryCommandGroupRejectsUnknownSubcommands(t *testing.T) {
	for _, group := range publicCommandGroupPaths() {
		name := strings.Join(group, " ")
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			argv := append(append([]string{}, group...), "definitely-unknown")
			err := Run(argv, "test-version", &stdout, &stderr)
			if err == nil || ExitCode(err) == 0 {
				t.Fatalf("%s accepted unknown subcommand: %v", name, err)
			}
		})
	}
}

func TestRemovedCommandsAreNotCallable(t *testing.T) {
	for _, command := range []string{"demo", "verify", "watch", "changelog", "delta", "spec", "coverage"} {
		t.Run(command, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := Run([]string{command}, "test-version", &stdout, &stderr)
			if err == nil || ExitCode(err) == 0 || !strings.Contains(err.Error(), `"`+command+`"`) ||
				!strings.Contains(err.Error(), "unknown") {
				t.Fatalf("removed %s command remained callable: %v", command, err)
			}
		})
	}
}
