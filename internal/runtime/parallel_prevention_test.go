package runtime

import "testing"

func TestParallelPreventionPreservesInnerShellBoundary(t *testing.T) {
	repo := makeRepoWithFiles(t, "rules:\n  - id: no-git\n    kind: forbid_command\n    command_match: prefix\n    commands: ['git']\n    mode: block\n    message: blocked\n", nil)
	tests := []struct {
		command string
		want    Decision
	}{
		{"parallel 'git status' ::: .", DecisionBlock},
		{"parallel {} ::: 'git status'", DecisionBlock},
		{"parallel '{1}' ::: git ::: status", DecisionBlock},
		{"parallel -I run run ::: 'git status'", DecisionBlock},
		{"parallel --replace=run run ::: 'git status'", DecisionBlock},
		{"parallel 'echo ready; git status' ::: .", DecisionBlock},
		{"parallel 'echo $(git status)' ::: .", DecisionBlock},
		{"parallel git status ::: .", DecisionBlock},
		{"sudo parallel 'git status' ::: .", DecisionBlock},
		{"parallel 'sh -c \"git status\"' ::: .", DecisionBlock},
		{"parallel 'parallel git status ::: .' ::: .", DecisionBlock},
		{"parallel echo 'git status' ::: .", DecisionPass},
		{"parallel 'echo git status' ::: .", DecisionPass},
		{"parallel -q echo 'git status; git status' ::: .", DecisionPass},
		{"echo parallel 'git status' ::: .", DecisionPass},
		{`parallel --tagstring '{= system("git status"); =}' echo ::: .`, DecisionBlock},
		{`parallel --tagstring='{= system("git status"); =}' echo ::: .`, DecisionBlock},
		{`sudo parallel -q --tag-string '{= system("git status"); =}' echo ::: .`, DecisionBlock},
		{`parallel --workdir '{= system("git status"); $_="."; =}' echo ::: .`, DecisionBlock},
		{`parallel --results='{= system("git status"); =}' echo ::: .`, DecisionBlock},
		{`parallel --retries '{= system("git status"); $_=1; =}' echo ::: .`, DecisionBlock},
		{`parallel --tagstring literal --workdir=. echo ::: '{= system("git status"); =}'`, DecisionPass},
		{`echo parallel --tagstring '{= system("git status"); =}' echo ::: .`, DecisionPass},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			inputs := Empty()
			inputs.Commands = []string{test.command}
			report, err := CheckRepoPolicyForPreCommand(repo, inputs)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != test.want {
				t.Fatalf("decision=%s, want %s: %+v", report.Decision, test.want, report.Violations)
			}
		})
	}
}
