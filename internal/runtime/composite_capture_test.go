package runtime

import "testing"

func TestCompositeDenyWriteBindsParentCaptures(t *testing.T) {
	repo := makeRepoWithFiles(t, "rules:\n  - id: generated-pair\n    kind: all_of\n    when_paths: ['src/{module}/**']\n    checks:\n      - kind: deny_write\n        paths: ['generated/{module}/**']\n    mode: block\n    message: m\n", nil)
	cases := []struct {
		writes []string
		block  bool
	}{
		{writes: []string{"src/foo/a.go", "generated/foo/x.go"}, block: true},
		{writes: []string{"src/foo/a.go", "generated/bar/x.go"}, block: false},
		{writes: []string{"src/foo/a.go", "generated/module/x.go"}, block: false},
	}
	for _, testCase := range cases {
		inputs := Empty()
		inputs.WritePaths = testCase.writes
		report, err := CheckRepoPolicy(repo, inputs)
		if err != nil {
			t.Fatal(err)
		}
		if blocked := report.Decision == DecisionBlock; blocked != testCase.block {
			t.Fatalf("writes %v: blocked=%t, want %t (%+v)", testCase.writes, blocked, testCase.block, report.Violations)
		}
	}
}

func TestCompositeDenyWriteFailsClosedOnUncapturedVariable(t *testing.T) {
	repo := makeRepoWithFiles(t, "rules:\n  - id: uncaptured\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: deny_write\n        paths: ['generated/{module}/**']\n    mode: block\n    message: m\n", nil)
	inputs := Empty()
	inputs.WritePaths = []string{"src/a.go"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock {
		t.Fatalf("an uncaptured sub-check variable did not fail closed: %+v", report)
	}
}
