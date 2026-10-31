package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestHookClaimForRecordsTimedOperatorClaimAfterConfirmation(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	var confirmed string
	previous := confirmOperatorPresence
	confirmOperatorPresence = func(action string, _ io.Writer) error {
		confirmed = action
		return nil
	}
	t.Cleanup(func() { confirmOperatorPresence = previous })
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "claim", repo, "operator:edit-protected", "--for", "3h", "--session", "session-1"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("timed operator claim: %v\n%s", err, stderr.String())
	}
	if confirmed != "reconc hook claim operator:edit-protected --for 3h0m0s" {
		t.Fatalf("confirmation action = %q", confirmed)
	}
	if !strings.Contains(stdout.String(), "claim 'operator:edit-protected' recorded for session session-1 until ") {
		t.Fatalf("timed claim output = %q", stdout.String())
	}
}

func TestHookClaimForRejectsInvalidUse(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	previous := confirmOperatorPresence
	confirmations := 0
	confirmOperatorPresence = func(string, io.Writer) error {
		confirmations++
		return nil
	}
	t.Cleanup(func() { confirmOperatorPresence = previous })
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing value", args: []string{"hook", "claim", repo, "operator:edit-protected", "--for"}, want: "--for requires a duration"},
		{name: "invalid duration", args: []string{"hook", "claim", repo, "operator:edit-protected", "--for", "soon"}, want: "invalid --for"},
		{name: "with revoke", args: []string{"hook", "claim", repo, "operator:edit-protected", "--for", "1h", "--revoke"}, want: "cannot be combined with --revoke"},
		{name: "agent claim", args: []string{"hook", "claim", repo, "ci-green", "--for", "1h", "--session", "session-1"}, want: "applies only to operator:* claims"},
		{name: "too long", args: []string{"hook", "claim", repo, "operator:edit-protected", "--for", "9h", "--session", "session-1"}, want: "between 1m0s and 8h0m0s"},
		{name: "zero duration", args: []string{"hook", "claim", repo, "operator:edit-protected", "--for", "0s", "--session", "session-1"}, want: "between 1m0s and 8h0m0s"},
		{name: "empty duration", args: []string{"hook", "claim", repo, "operator:edit-protected", "--for", "", "--session", "session-1"}, want: "invalid --for"},
		{name: "zero duration on agent claim", args: []string{"hook", "claim", repo, "ci-green", "--for", "0s", "--session", "session-1"}, want: "applies only to operator:* claims"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(tc.args, "test", &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	if confirmations != 0 {
		t.Fatalf("an invalid timed claim asked for operator confirmation %d time(s)", confirmations)
	}
}
