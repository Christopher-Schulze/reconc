package runtime

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestPreCommandNotForbidBlocksWhenInnerCheckPasses(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: must-git\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n    mode: block\n    message: must run git\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"echo safe"}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("not { forbid git } must block a non-git command, got %s: %+v", report.Decision, report.Violations)
	}

	inputs.Commands = []string{"git status"}
	report, err = CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("not { forbid git } must pass when git runs, got %s: %+v", report.Decision, report.Violations)
	}
}

func TestPreCommandNotForbidHonorsDispatcherAndPrefixContracts(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: must-git-status\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git status']\n    mode: block\n    message: must run git status\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"taskset -c 0 echo safe"}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("dispatcher-hidden echo must still fail not { forbid git status }: %s %+v", report.Decision, report.Violations)
	}

	inputs.Commands = []string{"taskset -c 0 git status --short"}
	report, err = CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("prefix git status through taskset must pass, got %s: %+v", report.Decision, report.Violations)
	}

	inputs.Commands = []string{"taskset -c 0 git stash"}
	report, err = CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("git stash must not satisfy prefix git status, got %s", report.Decision)
	}
}

func TestPreCommandNotForbidFailsClosedOnIncompleteAnalysis(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: must-git\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n    mode: block\n    message: must run git\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{`taskset --unknown git status`}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("incomplete analysis under not must fail closed, got %s: %+v", report.Decision, report.Violations)
	}
	if len(report.Violations) == 0 || !strings.Contains(report.Violations[0].Explanation, "could not be evaluated") {
		t.Fatalf("incomplete not must explain evaluation failure: %+v", report.Violations)
	}
}

func TestPreCommandNotForbidModes(t *testing.T) {
	tests := []struct {
		mode       string
		want       Decision
		wantMode   policy.Mode
		blocking   bool
		wantRecord bool
	}{
		{mode: "block", want: DecisionBlock, wantMode: policy.ModeBlock, blocking: true, wantRecord: true},
		{mode: "fix", want: DecisionBlock, wantMode: policy.ModeFix, blocking: true, wantRecord: true},
		{mode: "warn", want: DecisionWarn, wantMode: policy.ModeWarn, blocking: false, wantRecord: true},
		{mode: "observe", want: DecisionPass, wantMode: policy.ModeObserve, blocking: false, wantRecord: true},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			repo := makeRepoWithFiles(t,
				"rules:\n  - id: must-git\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n    mode: "+test.mode+"\n    message: must run git\n",
				nil)
			inputs := Empty()
			inputs.WritePaths = []string{"src/main.go"}
			inputs.Commands = []string{"echo safe"}
			report, err := CheckRepoPolicyForPreCommand(repo, inputs)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != test.want {
				t.Fatalf("decision = %s, want %s: %+v", report.Decision, test.want, report.Violations)
			}
			if test.wantRecord {
				if len(report.Violations) != 1 || report.Violations[0].Mode != test.wantMode || report.Violations[0].IsBlocking() != test.blocking {
					t.Fatalf("violation = %+v", report.Violations)
				}
			}
		})
	}
}

func TestPreCommandAllOfMultipleForbidsStillRequiresAHit(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['rm']\n    mode: block\n    message: m\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"echo safe"}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("positive all_of must skip when no forbid hits, got %s: %+v", report.Decision, report.Violations)
	}

	inputs.Commands = []string{"git status"}
	report, err = CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("git must fail all_of { forbid git, forbid rm }, got %s", report.Decision)
	}
}

func TestPreCommandAnyOfMultipleForbidsRequiresEveryInnerFailure(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: any_of\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['rm']\n    mode: block\n    message: m\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"git status"}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("any_of must pass when only one forbid fails, got %s: %+v", report.Decision, report.Violations)
	}

	inputs.Commands = []string{"git status && rm -rf tmp"}
	report, err = CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("any_of of two forbids must block when both run, got %s: %+v", report.Decision, report.Violations)
	}
}

func TestPreCommandMixedAllOfStillSkipsUnrelatedCommands(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n      - kind: require_claim\n        claims: ['approved']\n    mode: block\n    message: m\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"echo safe"}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("mixed all_of must remain a Stop-time claim gate for unrelated commands, got %s: %+v", report.Decision, report.Violations)
	}
}

func TestPreCommandForbidMatchesNewDispatchers(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: no-git\n    kind: forbid_command\n    command_match: prefix\n    commands: ['git']\n    mode: block\n    message: m\n",
		nil)
	for _, command := range []string{
		"taskset -c 0 git status",
		"bwrap --ro-bind / / -- git status",
		"unshare --fork git status",
		"nsenter --target 1 -- git status",
		"pkexec --user root git status",
		"busybox git status",
		"systemd-run --pipe git status",
		"parallel --jobs 2 git status ::: a",
		"sudo taskset -c 0 env git status",
	} {
		inputs := Empty()
		inputs.Commands = []string{command}
		report, err := CheckRepoPolicyForPreCommand(repo, inputs)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if report.Decision != DecisionBlock {
			t.Fatalf("%s must match forbid git, got %s: %+v", command, report.Decision, report.Violations)
		}
	}

	inputs := Empty()
	inputs.Commands = []string{"echo git status", "bwrap --ro-bind /usr/bin/git /git -- echo hi"}
	report, err := CheckRepoPolicyForPreCommand(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("literal and option-operand git text must not match, got %s: %+v", report.Decision, report.Violations)
	}
}

func TestCompletionNotForbidKeepsLogicalNegation(t *testing.T) {
	repo := makeRepoWithFiles(t,
		"rules:\n  - id: must-git\n    kind: not\n    when_paths: ['src/**']\n    checks:\n      - kind: forbid_command\n        command_match: prefix\n        commands: ['git']\n    mode: block\n    message: must run git\n",
		nil)

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.Commands = []string{"echo safe"}
	report := checkRepoPolicyForTest(t, repo, inputs)
	if report.Decision != DecisionBlock {
		t.Fatalf("completion not { forbid git } must still block echo, got %s", report.Decision)
	}
}
