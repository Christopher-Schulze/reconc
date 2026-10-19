package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestHookPauseLifecycleThroughCLI(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "pause-status", repo}, "test", &stdout, &stderr); err != nil || stdout.String() != "hook pause: none\n" {
		t.Fatalf("initial status err=%v stdout=%q", err, stdout.String())
	}
	stdout.Reset()
	if err := Run([]string{"hook", "pause", repo, "--for", "45m", "--reason", "incident review", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("pause: %v", err)
	}
	var status agentsession.HookPauseStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || !status.Active || status.Reason != "incident review" || status.ExpiresAt == "" {
		t.Fatalf("pause status=%+v err=%v", status, err)
	}
	stdout.Reset()
	if err := Run([]string{"ci", repo, "--staged"}, "test", &stdout, &stderr); err != nil || !strings.Contains(stderr.String(), "reconc ci: skipped; hook pause: active") {
		t.Fatalf("ci did not report the pause: err=%v stderr=%q", err, stderr.String())
	}
	stdout.Reset()
	if err := Run([]string{"hook", "resume", repo}, "test", &stdout, &stderr); err != nil || !strings.HasPrefix(stdout.String(), "hook pause: resumed at ") {
		t.Fatalf("resume err=%v stdout=%q", err, stdout.String())
	}
	if err := Run([]string{"hook", "resume", repo}, "test", &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "no active hook pause") {
		t.Fatalf("second resume = %v", err)
	}
}

func TestHookPauseRejectsIncompleteRequests(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"hook", "pause", repo, "--reason", "x"}, "--for DURATION and --reason TEXT are required"},
		{[]string{"hook", "pause", repo, "--for", "30m"}, "--for DURATION and --reason TEXT are required"},
		{[]string{"hook", "pause", repo, "--for", "soon", "--reason", "x"}, "invalid --for"},
		{[]string{"hook", "pause", repo, "--for", "48h", "--reason", "x"}, "pause duration must be between"},
		{[]string{"hook", "pause", repo, "--for"}, "--for requires a value"},
		{[]string{"hook", "pause", repo, "extra", "--for", "1h", "--reason", "x"}, "accepts at most one repo path"},
		{[]string{"hook", "resume", repo, "--force"}, "unknown flag"},
	}
	for _, testCase := range cases {
		var stdout, stderr bytes.Buffer
		err := Run(testCase.args, "test", &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%v: err=%v, want %q", testCase.args, err, testCase.want)
		}
	}
}
