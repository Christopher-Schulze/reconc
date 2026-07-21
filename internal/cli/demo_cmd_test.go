package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestDemoHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"demo", "--help"}, "test-version", &stdout, &stderr); err != nil {
		t.Fatalf("demo help: %v", err)
	}
	for _, want := range []string{"Usage: reconc demo [--keep] [--json]", "isolated Git repository", "--keep", "--json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestDemoRejectsUnknownArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"demo", "--fictional"}, "test-version", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), `unknown argument "--fictional"`) {
		t.Fatalf("error = %v, exit = %d", err, ExitCode(err))
	}
}

func TestDemoAppearsInGeneratedPublicSurfaces(t *testing.T) {
	var help, stderr bytes.Buffer
	if err := Run([]string{"--help"}, "test-version", &help, &stderr); err != nil {
		t.Fatalf("root help: %v", err)
	}
	if !strings.Contains(help.String(), "demo             run the isolated real-policy product journey") {
		t.Fatalf("root help omitted demo:\n%s", help.String())
	}

	var completion bytes.Buffer
	if err := Run([]string{"completion", "bash"}, "test-version", &completion, &stderr); err != nil {
		t.Fatalf("bash completion: %v", err)
	}
	if !strings.Contains(completion.String(), "demo") || !strings.Contains(completion.String(), "--keep --json") {
		t.Fatalf("completion omitted demo contract")
	}

	var manpage bytes.Buffer
	if err := Run([]string{"manpage"}, "test-version", &manpage, &stderr); err != nil {
		t.Fatalf("manpage: %v", err)
	}
	if !strings.Contains(manpage.String(), ".B demo\n") || !strings.Contains(manpage.String(), "run the isolated real-policy product journey") {
		t.Fatalf("manpage omitted demo contract")
	}
}
