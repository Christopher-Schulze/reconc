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
	corpus, err := NewCorpus(repo, []Case{NewRepositoryCase("private", inputs)}, AllEventClasses())
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
	second, err := MarshalCorpus(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, second) {
		t.Fatal("corpus marshal is not deterministic")
	}
}

func TestCorpusRedactsCommonCredentialShapes(t *testing.T) {
	repo := makeImpactRepo(t)
	inputs := runtime.Empty()
	inputs.Commands = []string{
		"AWS_SECRET_ACCESS_" + "KEY=secret-value deploy",
		"curl 'https://example.test/run?access_token=query-secret'",
		"curl -H 'Authorization: Bearer eyJheader123.payload123.signature123'",
		"publish --credential glpat-1234567890abcdef",
	}
	corpus, err := NewCorpus(repo, []Case{NewRepositoryCase("credentials", inputs)}, AllEventClasses())
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

func TestSanitizeSensitiveTextRedactsQuotedValuesThroughBoundary(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		forbidden []string
	}{
		{name: "double quoted flag", command: `deploy --password "secret extra words" --mode fast`, forbidden: []string{"secret", "extra", "words"}},
		{name: "single quoted flag", command: `deploy --token 'secret extra words' --mode fast`, forbidden: []string{"secret", "extra", "words"}},
		{name: "unterminated quote", command: `deploy --credential "secret extra words`, forbidden: []string{"secret", "extra", "words"}},
		{name: "escaped quote", command: `deploy --password "secret \"quoted\" extra" tail`, forbidden: []string{"secret", "quoted", "extra"}},
		{name: "quoted assignment", command: `deploy --password="secret extra words" tail`, forbidden: []string{"secret", "extra", "words"}},
		{name: "quoted bearer header", command: `curl -H 'Authorization: Bearer secret extra words' https://example.test`, forbidden: []string{"secret", "extra", "words"}},
		{name: "separately quoted bearer", command: `curl -H Authorization: "Bearer" "secret extra words"`, forbidden: []string{"secret", "extra", "words"}},
		{name: "bare bearer header", command: `curl -H Authorization: Bearer secret-value`, forbidden: []string{"secret-value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cleaned, redactions := sanitizeSensitiveText(test.command)
			if redactions == 0 {
				t.Fatalf("redactions = 0; cleaned = %q", cleaned)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(cleaned, forbidden) {
					t.Fatalf("cleaned command retained %q: %q", forbidden, cleaned)
				}
			}
			second, secondRedactions := sanitizeSensitiveText(test.command)
			if second != cleaned || secondRedactions != redactions {
				t.Fatalf("sanitization is not deterministic: (%q, %d) != (%q, %d)", cleaned, redactions, second, secondRedactions)
			}
		})
	}
}

func TestSanitizeSensitiveTextPreservesOrdinaryQuotesAndBoundsWords(t *testing.T) {
	command := `echo "ordinary value" 'single value' escaped\ value tail`
	cleaned, redactions := sanitizeSensitiveText(command)
	if cleaned != command || redactions != 0 {
		t.Fatalf("ordinary command = (%q, %d)", cleaned, redactions)
	}

	words, truncated := splitShellTextWords(strings.Repeat("x ", maxValueBytes+2))
	if !truncated || len(words) != maxValueBytes+1 {
		t.Fatalf("bounded words = (%d, %t)", len(words), truncated)
	}
	cleaned, redactions = sanitizeSensitiveText(strings.Repeat("x ", maxValueBytes+2))
	if len(cleaned) > maxValueBytes || redactions != 1 {
		t.Fatalf("bounded sanitization = (%d bytes, %d redactions)", len(cleaned), redactions)
	}
}

func TestCorpusRoundTripDoesNotRetainQuotedCredentialSuffixes(t *testing.T) {
	repo := makeImpactRepo(t)
	inputs := runtime.Empty()
	inputs.Commands = []string{
		`deploy --password "secret extra words" --mode fast`,
		`curl -H 'Authorization: Bearer header secret suffix' https://example.test`,
	}
	corpus, err := NewCorpus(repo, []Case{NewRepositoryCase("quoted-values", inputs)}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret", "extra", "words", "header", "suffix"} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("corpus retained quoted credential suffix %q:\n%s", forbidden, body)
		}
	}
	decoded, err := DecodeCorpus(body)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := MarshalCorpus(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, roundTrip) {
		t.Fatal("sanitized corpus round-trip changed bytes")
	}
}

func TestCorpusRejectsMutationDuplicateKeysAndSymlink(t *testing.T) {
	repo := makeImpactRepo(t)
	corpus := simpleCorpus(t, repo)
	body, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	mutated := bytes.Replace(body, []byte(`"id": "write-src"`), []byte(`"id": "other"`), 1)
	if _, err := DecodeCorpus(mutated); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mutated corpus error = %v", err)
	}
	duplicate := bytes.Replace(body, []byte(`"format_version": "reconc-impact-corpus/v2"`),
		[]byte(`"format_version": "reconc-impact-corpus/v2", "format_version": "other"`), 1)
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
	report, err := Compare(repo, corpus, impactCandidateMetadata(t, compiled, evaluator), currentImpactEvaluator(t, repo), evaluator)
	if err != nil {
		t.Fatal(err)
	}
	comparison := report.Cases[0]
	if comparison.Repository == nil || comparison.Repository.CurrentDecision != runtime.DecisionPass ||
		comparison.Repository.CandidateDecision != runtime.DecisionBlock ||
		!comparison.Repository.DecisionChanged ||
		!slicesEqual(comparison.Repository.NewlyBlockingRules, []string{"candidate-deny"}) {
		t.Fatalf("comparison = %+v", comparison)
	}
	if report.Summary.EstimatedUnitsDelta <= 0 || len(report.CorpusUnmatchedRules) != 0 {
		t.Fatalf("summary/rules = %+v / %v", report.Summary, report.CorpusUnmatchedRules)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("impact compile mutated lockfile: %v", err)
	}
	first, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
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
	corpus, err := NewCorpus(repo, []Case{NewRepositoryCase("src", runtime.ExecutionInputs{
		ReadPaths: []string{}, WritePaths: []string{"src/main.go"}, WriteEpochs: map[string]uint64{},
		Commands: []string{}, Claims: []string{}, CommandResults: []runtime.CommandResult{},
	})}, []EventClass{EventClassWrite})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, impactCandidateMetadata(t, compiled, evaluator), currentImpactEvaluator(t, repo), evaluator)
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
	corpus, err := NewCorpus(repo, []Case{NewRepositoryCase("satisfied", inputs)}, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, impactCandidateMetadata(t, compiled, evaluator), currentImpactEvaluator(t, repo), evaluator)
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
		cases[index] = NewRepositoryCase("case-"+fixedDecimal(index, 3), inputs)
	}
	corpus, err := NewCorpus(repo, cases, AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(repo, corpus, impactCandidateMetadata(t, compiled, evaluator), currentImpactEvaluator(t, repo), evaluator)
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
	_, err = Compare(repo, simpleCorpus(t, repo), impactCandidateMetadata(t, compiled, evaluator), currentImpactEvaluator(t, repo), evaluator)
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
	_, err = Compare(repo, simpleCorpus(t, repo), impactCandidateMetadata(t, compiled, evaluator), currentImpactEvaluator(t, repo), evaluator)
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
	corpus, err := NewCorpus(repo, []Case{NewRepositoryCase("write-src", inputs)}, AllEventClasses())
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

func impactCandidateMetadata(t *testing.T, compiled *compiler.CompiledPolicy, evaluator *runtime.CompiledPolicyEvaluator) Candidate {
	t.Helper()
	actions, err := evaluator.ActionRuntime()
	if err != nil {
		t.Fatal(err)
	}
	return Candidate{
		Kind: "policy_file", Name: "candidate", SourceDigest: compiled.SourceDigest,
		LockDigest: actions.LockDigest, ActionPlanIdentity: actions.Evaluator.PlanIdentity(),
		RuleCount: compiled.RuleCount, ActionToolCount: actions.ToolCount,
		ActionRuleCount: actions.ActionRuleCount,
	}
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
