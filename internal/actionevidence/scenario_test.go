package actionevidence

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/runtime"
)

const scenarioTestPolicy = `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      server_fingerprint: hmac-sha256:v1:fixture:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      tool: execute
      effect:
        kind: external
  rules: []
rules: []
`

func TestEvaluateScenariosUsesCurrentProductionCompilerAndEvaluator(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, ".reconc.yml"), []byte(scenarioTestPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repository, "test"); err != nil {
		t.Fatal(err)
	}
	compiled, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repository)
	if err != nil {
		t.Fatal(err)
	}
	actionRuntime, err := compiled.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := impactlab.DecodeCorpusFile(filepath.Join(
		"..", "..", "harness", "template", "audits", "testdata", "action-impact", "corpus.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := EvaluateScenarios(repository, []impactlab.Corpus{corpus}, compiled, actionRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.Evaluated || !evidence.ResultsCurrent || evidence.CaseCount != 1 ||
		evidence.ActionCaseCount != 1 || len(evidence.CorpusIDs) != 1 ||
		evidence.CorpusIDs[0] != corpus.CorpusID {
		t.Fatalf("scenario evidence = %#v", evidence)
	}

	drifted := actionRuntime
	drifted.SourceDigest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := EvaluateScenarios(repository, []impactlab.Corpus{corpus}, compiled, drifted); err == nil {
		t.Fatal("EvaluateScenarios accepted a runtime identity drift")
	}
}

func TestScenarioEvidenceDerivesMissingDimensionsAndPlatforms(t *testing.T) {
	missing := missingActionDimensions(impactlab.ActionDimensions{
		Classes: []impactlab.CaseKind{impactlab.CaseActionPost},
	})
	if len(missing) != 1 || missing[0] != "class:action_post" {
		t.Fatalf("missing dimensions = %#v", missing)
	}
}
