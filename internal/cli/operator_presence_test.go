package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestOperatorAdministrationRequiresAnInteractiveTerminal(t *testing.T) {
	previous := confirmOperatorPresence
	t.Cleanup(func() { confirmOperatorPresence = previous })
	// The real check: `go test` stdin is not a terminal, exactly like an
	// agent tool call or a script run by one.
	confirmOperatorPresence = func(action string, prompt io.Writer) error {
		return confirmOperatorAtTerminal(action, strings.NewReader("000000\n"), false, prompt, "123456")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"hook", "pause", repo, "--for", "30m", "--reason", "agent attempt"},
		{"hook", "resume", repo},
		{"hook", "evidence-resolve", repo, "--token", "token", "--reason", "agent attempt"},
		{"hook", "claim", repo, "operator:agents-md-edit-authorized"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(args, "test", &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
			t.Fatalf("%v ran without operator confirmation: %v", args, err)
		}
	}
	status, err := agentsession.ReadHookPauseStatus(repo, time.Now())
	if err != nil || status.Active {
		t.Fatalf("a refused pause changed state: %+v %v", status, err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "claim", repo, "ci-green"}, "test", &stdout, &stderr); err != nil && strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("an ordinary claim demanded operator confirmation: %v", err)
	}
}

func TestOperatorConfirmationAcceptsOnlyTheShownCode(t *testing.T) {
	var prompt bytes.Buffer
	if err := confirmOperatorAtTerminal("reconc hook pause", strings.NewReader("482913\n"), true, &prompt, "482913"); err != nil {
		t.Fatalf("matching code rejected: %v", err)
	}
	if !strings.Contains(prompt.String(), "type 482913") {
		t.Fatalf("prompt does not show the code: %q", prompt.String())
	}
	for _, answer := range []string{"482914\n", "\n", "yes\n", ""} {
		err := confirmOperatorAtTerminal("reconc hook pause", strings.NewReader(answer), true, io.Discard, "482913")
		if err == nil || !strings.Contains(err.Error(), "did not match") {
			t.Fatalf("answer %q accepted: %v", answer, err)
		}
	}
	code, err := operatorConfirmationCode()
	if err != nil || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		t.Fatalf("confirmation code = %q, %v", code, err)
	}
}
