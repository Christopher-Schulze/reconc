package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestSessionBriefingPreservesShellRemediation(t *testing.T) {
	repo := makeCheckRepo(t, `rules:
  - id: tests-required
    kind: require_command
    when_paths: ['source.go']
    commands: ['go test ./...']
    mode: block
    message: tests required
`)
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "source.go"), []byte("package example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir = repo
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v: %s", err, output)
		}
	}
	payload := []byte(`{"session_id":"briefing-shell"}`)
	if result := agentsession.RunSessionStart(repo, payload); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(repo, "source.go"), []byte("package example\n\nvar Changed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := []byte(`{"session_id":"briefing-shell","tool_name":"Write","tool_input":{"file_path":"source.go"}}`)
	if result := agentsession.RunPostToolUse(repo, write); result.ExitCode != 0 {
		t.Fatalf("write evidence: %+v", result)
	}
	stop := agentsession.RunStop(repo, payload)
	if !strings.Contains(stop.Stdout, "block") {
		t.Fatalf("missing command did not block Stop: %+v", stop)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("briefing: %v, %s", err, stderr.String())
	}
	var briefing struct {
		Status string                    `json:"policy_report_status"`
		Text   string                    `json:"remediation"`
		Action runtime.RemediationAction `json:"remediation_action"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &briefing); err != nil {
		t.Fatal(err)
	}
	if briefing.Status != "current" {
		t.Fatalf("report is not current: %s", stdout.String())
	}
	if strings.Contains(briefing.Text, "'go test ./...'") || !strings.Contains(briefing.Text, "go test ./...") {
		t.Errorf("shell script rendered as one executable: %q", briefing.Text)
	}
	if briefing.Action.Kind != runtime.ActionKindShell || briefing.Action.Shell != "go test ./..." ||
		len(briefing.Action.Argv) != 0 || briefing.Action.Cwd != repo || briefing.Action.Authorization != "operator_approval" {
		t.Errorf("typed action lost: %+v", briefing.Action)
	}
	if !strings.Contains(briefing.Text, "operator_approval") || !strings.Contains(briefing.Text, repo) {
		t.Errorf("text omitted execution context: %q", briefing.Text)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"session-briefing", repo}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("text briefing: %v, %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), briefing.Text) {
		t.Fatalf("text output lost the complete remediation guidance: %s", stdout.String())
	}
}

func TestBriefingActionRenderingPreservesExecutionSemantics(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := filepath.Join(root, "work ' directory")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		action runtime.RemediationAction
		want   string
	}{
		{
			name: "argv",
			action: runtime.RemediationAction{Code: runtime.ActionRunCommand, Kind: runtime.ActionKindArgv,
				Argv: []string{"printf", "%s\\n", "one two", "a'b", "$HOME; false"}},
			want: "one two\na'b\n$HOME; false\n",
		},
		{
			name: "shell",
			action: runtime.RemediationAction{Code: runtime.ActionRunCommand, Kind: runtime.ActionKindShell,
				Shell: "pwd; printf '%s\\n' 'a b' 'quote\"' | tr a-z A-Z"},
			want: workingDirectory + "\nA B\nQUOTE\"\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.action.Cwd = workingDirectory
			test.action.Authorization = "operator_approval"
			test.action.RequiredEvidence = []string{"command_success"}
			plan := &runtime.FixPlan{Remediations: []runtime.Remediation{{Priority: "blocking", Actions: []runtime.RemediationAction{test.action}}}}
			selected, ok := firstExecutableFixPlanAction(plan)
			if !ok || !reflect.DeepEqual(selected, test.action) {
				t.Fatalf("action metadata changed: %+v", selected)
			}
			label := "; Argv: "
			if test.action.Kind == runtime.ActionKindShell {
				label = "; Shell: "
			}
			text := renderBriefingRemediationAction(selected)
			context, command, found := strings.Cut(text, label)
			if !found || !strings.Contains(context, "operator_approval") || !strings.Contains(context, quoteCommandArgument(workingDirectory)) {
				t.Fatalf("execution context missing: %q", text)
			}
			process := exec.Command("sh", "-c", command)
			process.Dir = selected.Cwd
			output, err := process.CombinedOutput()
			if err != nil || string(output) != test.want {
				t.Fatalf("rendered action changed execution: output=%q want=%q error=%v", output, test.want, err)
			}
		})
	}
}

func TestBriefingDoesNotSelectNonExecutableActions(t *testing.T) {
	for _, action := range []runtime.RemediationAction{
		{Kind: runtime.ActionKindInspection, Shell: "echo inert"},
		{Kind: runtime.ActionKindApproval, Argv: []string{"echo", "inert"}},
		{Kind: runtime.ActionKindShell, Shell: " \n\t"},
		{Kind: runtime.ActionKindArgv, Argv: []string{""}},
		{Kind: runtime.ActionKindShell, Shell: "echo inert", Argv: []string{"echo"}},
		{Kind: runtime.ActionKindArgv, Shell: "echo inert", Argv: []string{"echo"}},
	} {
		plan := &runtime.FixPlan{Remediations: []runtime.Remediation{{Priority: "blocking", Actions: []runtime.RemediationAction{action}}}}
		if selected, ok := firstExecutableFixPlanAction(plan); ok {
			t.Fatalf("non-executable action selected: %+v", selected)
		}
	}
}
