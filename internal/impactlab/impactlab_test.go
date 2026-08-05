package impactlab

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

func TestCorpusExportIsPrivateDeterministicAndStrict(t *testing.T) {
	repo := makeImpactRepo(t)
	inputs := runtime.Empty()
	inputs.WritePaths = []string{filepath.Join(repo, "src", "main.go")}
	inputs.Commands = []string{"curl --token sk-supersecretvalue https://example.test"}
	inputs.CommandResults = []runtime.CommandResult{{
		Command: "OPENAI_API_KEY=secretvalue go test ./...", Outcome: runtime.CommandOutcomeSuccess,
	}}
	inputs.Claims = []string{"api_key=claimsecret"}
	corpus, err := NewCorpus(repo, []Case{{ID: "private", Inputs: inputs}}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"supersecretvalue", "secretvalue", "claimsecret", repo} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatalf("corpus leaked %q:\n%s", secret, body)
		}
	}
	if corpus.Completeness.CompleteReplay || corpus.Completeness.RedactionCount != 4 {
		t.Fatalf("redacted completeness = %+v", corpus.Completeness)
	}
	decoded, err := DecodeCorpus(body)
	if err != nil || decoded.CorpusID != corpus.CorpusID {
		t.Fatalf("decode = %+v, %v", decoded, err)
	}
	second, _ := MarshalCorpus(decoded)
	if !bytes.Equal(body, second) {
		t.Fatal("corpus marshal is not deterministic")
	}
}

func TestCorpusRedactsCommonCredentialShapes(t *testing.T) {
	repo := makeImpactRepo(t)
	inputs := runtime.Empty()
	inputs.Commands = []string{
		"AWS_SECRET_ACCESS_KEY=secret-value deploy",
		"curl 'https://example.test/run?access_token=query-secret'",
		"curl -H 'Authorization: Bearer eyJheader123.payload123.signature123'",
		"publish --credential glpat-1234567890abcdef",
	}
	corpus, err := NewCorpus(repo, []Case{{ID: "credentials", Inputs: inputs}}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-value", "query-secret", "eyJheader123", "1234567890abcdef"} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatalf("corpus leaked %q:\n%s", secret, body)
		}
	}
	if corpus.Completeness.CompleteReplay || corpus.Completeness.RedactionCount < len(inputs.Commands) {
		t.Fatalf("credential completeness = %+v", corpus.Completeness)
	}
}

func TestCorpusRejectsMutationDuplicateKeysAndSymlink(t *testing.T) {
	repo := makeImpactRepo(t)
	corpus := simpleCorpus(t, repo)
	body, _ := MarshalCorpus(corpus)
	mutated := bytes.Replace(body, []byte(`"id": "write-src"`), []byte(`"id": "other"`), 1)
	if _, err := DecodeCorpus(mutated); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mutated corpus error = %v", err)
	}
	duplicate := bytes.Replace(body, []byte(`"format_version": "reconc-impact-corpus/v1"`),
		[]byte(`"format_version": "reconc-impact-corpus/v1", "format_version": "other"`), 1)
	if _, err := DecodeCorpus(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate corpus error = %v", err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "corpus.json")
	link := filepath.Join(root, "corpus-link.json")
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCorpusFile(link); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("symlink corpus error = %v", err)
	}
}

func TestCompareFindsBlockingDeltaWithoutRepositoryMutation(t *testing.T) {
	repo := makeImpactRepo(t)
	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	candidateSource := compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: candidate-deny\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n",
	}
	compiled, lockfile, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", candidateSource)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(lockfile)
	if err != nil {
		t.Fatal(err)
	}
	corpus := simpleCorpus(t, repo)
	report, err := Compare(repo, corpus, Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
	}, currentImpactEvaluator(t, repo), evaluator)
	if err != nil {
		t.Fatal(err)
	}
	comparison := report.Cases[0]
	if comparison.CurrentDecision != runtime.DecisionPass ||
		comparison.CandidateDecision != runtime.DecisionBlock ||
		!comparison.DecisionChanged ||
		!slicesEqual(comparison.NewlyBlockingRules, []string{"candidate-deny"}) {
		t.Fatalf("comparison = %+v", comparison)
	}
	if report.Summary.EstimatedUnitsDelta <= 0 || len(report.CorpusUnmatchedRules) != 0 {
		t.Fatalf("summary/rules = %+v / %v", report.Summary, report.CorpusUnmatchedRules)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("impact compile mutated lockfile: %v", err)
	}
	first, _ := MarshalReport(report)
	second, _ := MarshalReport(report)
	if !bytes.Equal(first, second) {
		t.Fatal("impact report is not deterministic")
	}
}

func TestComparePreservesIncompleteCorpusDisclaimerAndUnmatchedRules(t *testing.T) {
	repo := makeImpactRepo(t)
	source := compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: docs-only\n    kind: deny_write\n    paths: [docs/**]\n    mode: warn\n    message: docs\n",
	}
	compiled, lockfile, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(lockfile)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := NewCorpus(repo, []Case{{ID: "src", Inputs: runtime.ExecutionInputs{
		ReadPaths: []string{}, WritePaths: []string{"src/main.go"}, WriteEpochs: map[string]uint64{},
		Commands: []string{}, Claims: []string{}, CommandResults: []runtime.CommandResult{},
	}}}, []EventClass{EventClassWrite})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
	}, currentImpactEvaluator(t, repo), evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if !slicesEqual(report.CorpusUnmatchedRules, []string{"docs-only"}) ||
		!strings.Contains(report.SafetyConclusion, "Incomplete replay") ||
		!strings.Contains(report.SafetyConclusion, "unmatched in this corpus") {
		t.Fatalf("incomplete report = %+v", report)
	}
}

func TestCompareCountsSatisfiedRuleAsCorpusMatched(t *testing.T) {
	repo := makeImpactRepo(t)
	source := compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: read-first\n    kind: require_read\n    paths: [src/**]\n    before_paths: [README.md]\n    mode: block\n    message: read first\n",
	}
	compiled, lockfile, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(lockfile)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.ReadPaths = []string{"README.md"}
	corpus, err := NewCorpus(repo, []Case{{ID: "satisfied", Inputs: inputs}}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
	}, currentImpactEvaluator(t, repo), evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rules) != 1 || report.Rules[0].CandidateMatches != 1 || len(report.CorpusUnmatchedRules) != 0 {
		t.Fatalf("satisfied rule impact = %+v, unmatched=%v", report.Rules, report.CorpusUnmatchedRules)
	}
}

func TestMalformedCandidateUsesProductionParser(t *testing.T) {
	repo := makeImpactRepo(t)
	_, _, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: duplicate\n    kind: deny_write\n    paths: [src/**]\n    message: one\n  - id: duplicate\n    kind: deny_write\n    paths: [docs/**]\n    message: two\n",
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate rule id") {
		t.Fatalf("malformed candidate error = %v", err)
	}
}

func TestCompareScaleRemainsDeterministicAndBounded(t *testing.T) {
	repo := makeImpactRepo(t)
	source := compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: candidate-warn\n    kind: deny_write\n    paths: [src/**]\n    mode: warn\n    message: warn\n",
	}
	compiled, lockfile, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(lockfile)
	if err != nil {
		t.Fatal(err)
	}
	cases := make([]Case, 256)
	for index := range cases {
		inputs := runtime.Empty()
		inputs.WritePaths = []string{"src/file-" + string(rune('a'+index%26)) + ".go"}
		cases[index] = Case{ID: "case-" + fixedDecimal(index, 3), Inputs: inputs}
	}
	corpus, err := NewCorpus(repo, cases, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
	}, currentImpactEvaluator(t, repo), evaluator)
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.CaseCount != len(cases) || len(body) > MaxCorpusBytes {
		t.Fatalf("scale report cases=%d bytes=%d", report.Summary.CaseCount, len(body))
	}
}

func TestCompareRefusesRequireScriptBeforeExecution(t *testing.T) {
	repo := makeImpactRepo(t)
	marker := filepath.Join(repo, "script-ran")
	script := filepath.Join(repo, "impact-script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch script-ran\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: external-check\n    kind: require_script\n    when_paths: [src/**]\n    script: impact-script.sh\n    mode: block\n    message: external\n",
	}
	compiled, lockfile, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(lockfile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compare(repo, simpleCorpus(t, repo), Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
	}, currentImpactEvaluator(t, repo), evaluator)
	if err == nil || !strings.Contains(err.Error(), "side-effect-free replay refuses require_script") {
		t.Fatalf("require_script error = %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("impact executed require_script: %v", statErr)
	}
}

func TestCompareRefusesCompositeRequireScriptBeforeExecution(t *testing.T) {
	repo := makeImpactRepo(t)
	marker := filepath.Join(repo, "composite-script-ran")
	if err := os.WriteFile(filepath.Join(repo, "composite-script.sh"), []byte("#!/bin/sh\ntouch composite-script-ran\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := compiler.CandidateSource{
		Kind: policy.SourcePolicyFile, Name: "candidate",
		Content: "rules:\n  - id: composite-external\n    kind: all_of\n    when_paths: [src/**]\n    checks:\n      - kind: require_script\n        script: composite-script.sh\n    mode: block\n    message: external\n",
	}
	compiled, lockfile, _, err := compiler.RenderRepoPolicyWithCandidate(repo, "test", source)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(lockfile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compare(repo, simpleCorpus(t, repo), Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
	}, currentImpactEvaluator(t, repo), evaluator)
	if err == nil || !strings.Contains(err.Error(), "composite-external") {
		t.Fatalf("composite require_script error = %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("impact executed composite require_script: %v", statErr)
	}
}

func simpleCorpus(t *testing.T, repo string) Corpus {
	t.Helper()
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"src/main.go"}
	corpus, err := NewCorpus(repo, []Case{{ID: "write-src", Inputs: inputs}}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

func makeImpactRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("default_mode: warn\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	return repo
}

func currentImpactEvaluator(t *testing.T, repo string) *runtime.CompiledPolicyEvaluator {
	t.Helper()
	evaluator, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func fixedDecimal(value, width int) string {
	digits := make([]byte, width)
	for index := width - 1; index >= 0; index-- {
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits)
}
