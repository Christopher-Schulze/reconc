package runtime

import "testing"

func TestNativeAssurancePreservesCommandEpochs(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: native
    kind: require_assurance
    mode: block
    when_paths: ["**/*.go"]
    message: current verification required
    assurance:
      - id: live
        type: live_verification
        commands: ["go test ./..."]
`)
	writeFile(t, repo, "main.go", "package main\n")
	for _, test := range []struct {
		name  string
		epoch uint64
		want  Decision
	}{
		{"legacy", 0, DecisionBlock}, {"stale", 1, DecisionBlock},
		{"equal", 2, DecisionPass}, {"fresh", 3, DecisionPass},
		{"index-bound", ExplicitEvidenceEpoch, DecisionPass},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputs := Empty()
			inputs.WritePaths = []string{"main.go"}
			inputs.WriteEpochs = map[string]uint64{"main.go": 2}
			inputs.CommandResults = []CommandResult{{Command: "rtk go test ./...", Outcome: CommandOutcomeSuccess, EvidenceEpoch: test.epoch}}
			report, err := CheckRepoPolicy(repo, inputs)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != test.want {
				t.Fatalf("decision = %s, want %s; violations=%+v", report.Decision, test.want, report.Violations)
			}
		})
	}
}
