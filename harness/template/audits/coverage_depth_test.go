package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCachedAuditWrappersPreserveUnderlyingAuditResultsWhenBypassed(t *testing.T) {
	t.Setenv(cacheEnv, "1")
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", minimalTasksMd())
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", taskDetail(
		"TASK-0001-Active-Work",
		"Active",
		"- [~] Execute the active audit.",
		"",
	))
	writeFile(t, root, "docs/spec.md", "# Specification\n")

	tests := []struct {
		name       string
		cached     func(string) []string
		underlying func(string) []string
	}{
		{name: "task state", cached: cachedTaskState, underlying: auditTaskState},
		{name: "spec format", cached: cachedSpecFormat, underlying: auditSpecFormat},
		{name: "schema present", cached: cachedSchemaPresent, underlying: auditSchemaPresent},
		{name: "agents mirror", cached: cachedAgentsMdMirror, underlying: auditAgentsMdMirror},
		{name: "start entrypoint", cached: cachedStartEntrypoint, underlying: auditStartEntrypoint},
		{name: "build baseline", cached: cachedBuildBaseline, underlying: auditBuildBaseline},
		{name: "test coverage", cached: cachedTestCoverage, underlying: auditTestCoverage},
		{name: "durable store", cached: cachedDurableStoreBaseline, underlying: auditDurableStoreBaseline},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.cached(root)
			want := test.underlying(root)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s cached result = %#v, want underlying result %#v", test.name, got, want)
			}
		})
	}
}

func TestCacheInputValueChangesDigest(t *testing.T) {
	first := newCacheInputs()
	first.AddValue("archive-tree", "abc123")
	firstHash, err := first.Hash()
	if err != nil {
		t.Fatalf("hash first cache input: %v", err)
	}

	second := newCacheInputs()
	second.AddValue("archive-tree", "def456")
	secondHash, err := second.Hash()
	if err != nil {
		t.Fatalf("hash second cache input: %v", err)
	}
	if firstHash == secondHash {
		t.Fatalf("AddValue inputs must affect digest, both hashes were %s", firstHash)
	}
}

func TestAuditCacheRejectsEvaluationAcrossInputMutation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "input.txt", "old\n")
	inputs := func() *cacheInputs {
		result := newCacheInputs()
		result.AddFile(filepath.Join(root, "input.txt"))
		return result
	}

	first := runWithCache(root, "mutation-race", inputs(), func() []string {
		writeFile(t, root, "input.txt", "new\n")
		return nil
	})
	if !containsFailure(first, "cache input changed during evaluation") {
		t.Fatalf("concurrent input mutation must fail closed, got %#v", first)
	}

	writeFile(t, root, "input.txt", "old\n")
	calls := 0
	second := runWithCache(root, "mutation-race", inputs(), func() []string {
		calls++
		return nil
	})
	if len(second) != 0 || calls != 1 {
		t.Fatalf("mutated evaluation must not publish a reusable pass: result=%#v calls=%d", second, calls)
	}
}

func TestConfiguredBaselineCacheInputsFollowStackConfig(t *testing.T) {
	t.Run("build baseline", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, stackConfigRel, `stack: go-cli
project: alpha
build:
  enabled: true
  language: go
  require_go_mod: false
  require_cargo_toml: false
  require_frontend_package: false
  require_build_runner: false
  require_build_runner_test: false
  backend_entrypoints: [custom]
durable_store:
  enabled: false
`)
		writeFile(t, root, "backend/custom/main.go", "package main\n")
		if failures := cachedBuildBaseline(root); len(failures) != 0 {
			t.Fatalf("initial configured build baseline failed: %v", failures)
		}
		if err := os.Remove(filepath.Join(root, "backend/custom/main.go")); err != nil {
			t.Fatalf("remove configured build input: %v", err)
		}
		if failures := cachedBuildBaseline(root); !containsFailure(failures, "build baseline missing backend/custom/main.go") {
			t.Fatalf("configured build input change reused a stale pass: %v", failures)
		}
	})

	t.Run("durable store", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, stackConfigRel, `stack: go-cli
project: alpha
build:
  enabled: false
durable_store:
  enabled: true
  store_files: ["backend/{project}/custom/store.go"]
  store_go_tokens: [STORE_TOKEN]
  migration_go_files: [db/custom.go]
  initial_sql: "db/{project}/custom.sql"
  initial_sql_tokens: [SQL_TOKEN]
`)
		writeFile(t, root, "backend/alpha/custom/store.go", "STORE_TOKEN\n")
		writeFile(t, root, "db/custom.go", "package db\n")
		writeFile(t, root, "db/alpha/custom.sql", "SQL_TOKEN\n")
		if failures := cachedDurableStoreBaseline(root); len(failures) != 0 {
			t.Fatalf("initial configured durable-store baseline failed: %v", failures)
		}
		writeFile(t, root, "backend/alpha/custom/store.go", "changed\n")
		if failures := cachedDurableStoreBaseline(root); !containsFailure(failures, `store.go missing durable-store token "STORE_TOKEN"`) {
			t.Fatalf("configured durable-store input change reused a stale pass: %v", failures)
		}
	})
}

func TestSpecAuditEnumerationsRejectUnknownValues(t *testing.T) {
	tests := []struct {
		name    string
		valid   []string
		invalid []string
		check   func(string) bool
	}{
		{
			name:    "carry decision",
			valid:   []string{"CARRY", "IMPROVE", "PROVEN_IRRELEVANT", "GAP"},
			invalid: []string{"", "PENDING", "carry", "DROP"},
			check:   validCarryDecision,
		},
		{
			name:    "claim status",
			valid:   []string{"ACTIVE", "COMPLETED", "PARTIAL", "BLOCKED", "STALE"},
			invalid: []string{"", "PENDING", "active", "DONE"},
			check:   validClaimStatus,
		},
		{
			name:    "claim phase",
			valid:   []string{"CLAIMED", "ATOMIZING", "RESEARCH", "OWNER_DISCOVERY", "CODE_MAPPING", "GAP_WRITING", "RANGE_REALITY_CHECK", "COMPLETED"},
			invalid: []string{"", "PENDING", "claimed", "IMPLEMENTING"},
			check:   validClaimPhase,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range test.valid {
				if !test.check(value) {
					t.Fatalf("%s must accept %q", test.name, value)
				}
			}
			for _, value := range test.invalid {
				if test.check(value) {
					t.Fatalf("%s must reject %q", test.name, value)
				}
			}
		})
	}
}

func TestSpecAuditVerdictsDistinguishRecognizedFromPassing(t *testing.T) {
	valid := []string{"EXCEEDS", "MATCH", "PARTIAL", "MISSING", "WRONG_SHAPE", "PARALLEL_SYSTEM", "WEAK_TESTS", "UNVERIFIED", "NO_CODE_SURFACE"}
	for _, verdict := range valid {
		if !validSpecAuditVerdict(verdict) {
			t.Fatalf("validSpecAuditVerdict(%q) = false", verdict)
		}
	}
	for _, verdict := range []string{"EXCEEDS", "MATCH", "NO_CODE_SURFACE"} {
		if !passingSpecAuditVerdict(verdict) {
			t.Fatalf("passingSpecAuditVerdict(%q) = false", verdict)
		}
	}
	for _, verdict := range []string{"", "PASS", "PARTIAL"} {
		if validSpecAuditVerdict(verdict) && verdict != "PARTIAL" {
			t.Fatalf("validSpecAuditVerdict(%q) = true", verdict)
		}
		if passingSpecAuditVerdict(verdict) {
			t.Fatalf("passingSpecAuditVerdict(%q) = true", verdict)
		}
	}
}

func TestSpecAuditRangeAndTouchHelpersPinBoundarySemantics(t *testing.T) {
	surfaces := []string{" docs/spec-audit/** ", "backend/project/**"}
	if !touchSurfacesContain(surfaces, "docs/spec-audit") {
		t.Fatal("normalized spec-audit touch surface must match prefix")
	}
	if touchSurfacesContain(surfaces, "frontend/") {
		t.Fatal("unrelated touch surface must not match")
	}

	ranges := []specAuditRange{
		{Start: 1, End: 3, Text: "docs/spec.md:L1-L3"},
		{Start: 8, End: 10, Text: "docs/spec.md:L8-L10"},
	}
	if !rangeOverlapsAny(specAuditRange{Start: 3, End: 4}, ranges) {
		t.Fatal("shared endpoint must count as overlap")
	}
	if rangeOverlapsAny(specAuditRange{Start: 4, End: 7}, ranges) {
		t.Fatal("disjoint range must not count as overlap")
	}
	if got, want := formatSpecAuditRanges(ranges), "docs/spec.md:L1-L3, docs/spec.md:L8-L10"; got != want {
		t.Fatalf("formatSpecAuditRanges() = %q, want %q", got, want)
	}
}

func TestAuditSpecAuditGapRecordRequiresEveryDurableField(t *testing.T) {
	const gapID = "GAP-L0001-01"
	failures := auditSpecAuditGapRecord(gapID, "")
	if len(failures) != 11 {
		t.Fatalf("empty gap record produced %d failures, want 11:\n%s", len(failures), strings.Join(failures, "\n"))
	}

	complete := `- Severity: high
- Atom IDs: ATOM-L0001-01
- Spec evidence: docs/spec.md:L1
- Current code evidence: backend/project/a.go:L1
- Exact missing detail: retry contract
- Why current code is insufficient: retry is unbounded
- Minimum spec/research parity required: bounded retry
- Target adaptation: add bounded retry
- Required tests: deterministic exhaustion test
- Acceptance criteria: retry stops at the configured bound
- Close condition: implementation and tests pass
`
	if got := auditSpecAuditGapRecord(gapID, complete); len(got) != 0 {
		t.Fatalf("complete gap record failed:\n%s", strings.Join(got, "\n"))
	}
}

func TestAuditSpecAuditClaimsRejectsOverlapAndMissingArtifacts(t *testing.T) {
	root := t.TempDir()
	state := `# State

## Active Claims
| Claim ID | Spec Range | Phase | Status |
|---|---|---|---|
| bad-claim | docs/spec.md:L1-L3 | IMPLEMENTING | PENDING |
| CLAIM-20260727T120000Z-codex-session-L0003-L0004 | docs/spec.md:L3-L4 | CLAIMED | ACTIVE |
`
	failures := auditSpecAuditClaims(root, state, 4)
	for _, want := range []string{
		`invalid Claim ID "bad-claim"`,
		`invalid Status "PENDING"`,
		`invalid Phase "IMPLEMENTING"`,
		"overlapping active claim range docs/spec.md:L3-L4",
		"missing claim file",
		"missing range artifact",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestAuditSpecAuditResearchCoverageRejectsUntrackedRelationships(t *testing.T) {
	atoms := map[string]specAuditAtom{
		"ATOM-L0001-01": {
			ID:           "ATOM-L0001-01",
			ResearchRefs: []string{"not-research/path", "research/source/contract.md"},
		},
	}
	floors := map[string]specAuditResearchFloor{
		"RF-L0001-01": {
			ID:            "RF-L0001-01",
			SourceRef:     "research/other/contract.md",
			LinkedAtomIDs: []string{"ATOM-L9999-01"},
			GapIDs:        []string{"GAP-L0001-01"},
		},
	}
	failures := auditSpecAuditResearchCoverage(atoms, floors, map[string]string{})
	for _, want := range []string{
		`Research Refs contains invalid ref "not-research/path"`,
		"missing research floor row for atom ATOM-L0001-01 ref research/source/contract.md",
		"linked atom ATOM-L9999-01 does not exist",
		"Gap ID GAP-L0001-01 missing",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}

	floors["RF-L0001-01"] = specAuditResearchFloor{
		ID:            "RF-L0001-01",
		SourceRef:     "research/source/contract.md:L1",
		LinkedAtomIDs: []string{"ATOM-L0001-01"},
	}
	if !researchRefCoveredByFloor("research/source/contract.md", "ATOM-L0001-01", floors) {
		t.Fatal("source ref with a line suffix must cover its linked atom")
	}
}

func TestAuditSpecAuditEvidenceTargetsValidatesPassingAndResearchRefs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "backend/project/valid.go", "package project\n")
	writeFile(t, root, "research/source.md", "source\n")

	atoms := map[string]specAuditAtom{
		"ATOM-L0001-01": {ID: "ATOM-L0001-01", Verdict: "MATCH"},
		"ATOM-L0001-02": {ID: "ATOM-L0001-02", Verdict: "EXCEEDS", ImplementationEvidence: "backend/project/missing.go:L1"},
		"ATOM-L0001-03": {ID: "ATOM-L0001-03", Verdict: "PARTIAL", ImplementationEvidence: "backend/project/missing.go:L1"},
		"ATOM-L0001-04": {ID: "ATOM-L0001-04", Verdict: "MATCH", ImplementationEvidence: "backend/project/valid.go:L2"},
	}
	floors := map[string]specAuditResearchFloor{
		"RF-L0001-01": {ID: "RF-L0001-01", SourceRef: "research/source.md:L2"},
		"RF-L0001-02": {ID: "RF-L0001-02", SourceRef: "external/source.md:L1"},
	}
	failures := auditSpecAuditEvidenceTargets(root, atoms, floors)
	for _, want := range []string{
		"atom ATOM-L0001-01: passing verdict requires",
		"implementation evidence for atom ATOM-L0001-02 points to missing evidence path",
		"implementation evidence for atom ATOM-L0001-04 points outside",
		"research floor RF-L0001-01 points outside",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
	if containsFailure(failures, "ATOM-L0001-03") {
		t.Fatalf("non-passing atoms must not require implementation targets:\n%s", strings.Join(failures, "\n"))
	}
	if containsFailure(failures, "RF-L0001-02") {
		t.Fatalf("non-research source refs must not be validated here:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditSpecAuditAtomCoverageRejectsMissingRowsPrefixesAndGaps(t *testing.T) {
	rangeOne := specAuditRange{Start: 1, End: 2, Text: "docs/spec.md:L1-L2"}
	if failures := auditSpecAuditAtomCoverage([]specAuditRange{rangeOne}, nil, nil); !containsFailure(failures, "has no atom rows") {
		t.Fatalf("missing atoms must fail, got:\n%s", strings.Join(failures, "\n"))
	}

	atoms := map[string]specAuditAtom{
		"ATOM-L0002-01": {
			ID:        "ATOM-L0002-01",
			SpecLines: []specAuditRange{{Start: 1, End: 1, Text: "docs/spec.md:L1-L1"}},
			GapIDs:    []string{"GAP-L0001-01"},
		},
	}
	failures := auditSpecAuditAtomCoverage([]specAuditRange{rangeOne}, atoms, map[string]string{})
	for _, want := range []string{
		"missing atom coverage for docs/spec.md:L2",
		"requires range-derived prefix ATOM-L0001-",
		"Gap ID GAP-L0001-01 missing",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestAuditSpecAuditStateProgressRequiresContiguousCoverage(t *testing.T) {
	rangeOne := specAuditRange{Start: 1, End: 2, Text: "docs/spec.md:L1-L2"}
	atoms := map[string]specAuditAtom{
		"ATOM-L0001-01": {
			ID:        "ATOM-L0001-01",
			SpecLines: []specAuditRange{{Start: 1, End: 1, Text: "docs/spec.md:L1-L1"}},
		},
	}
	if failures := auditSpecAuditStateProgress("- Last Fully Verified Line: none", []specAuditRange{rangeOne}, atoms); !containsFailure(failures, "must advance") {
		t.Fatalf("completed range without progress must fail, got:\n%s", strings.Join(failures, "\n"))
	}
	if failures := auditSpecAuditStateProgress("- Last Fully Verified Line: L2", []specAuditRange{rangeOne}, atoms); !containsFailure(failures, "exceeds contiguous completed coverage 0") {
		t.Fatalf("progress beyond incomplete atom coverage must fail, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestRunDispatchesEveryAuditModeWithoutChangingItsContract(t *testing.T) {
	t.Setenv(cacheEnv, "1")
	root := newWorkflowAuditRepo(t)
	writeFile(t, root, "docs/tasks.md", minimalTasksMd())
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", taskDetail(
		"TASK-0001-Active-Work",
		"Active",
		"- [~] Execute the active audit.",
		"",
	))
	writeFile(t, root, "docs/spec.md", "# Specification\n")

	tests := []struct {
		mode  string
		audit func(string) []string
	}{
		{mode: "all", audit: runAllAudits},
		{mode: "repo-layout", audit: auditRepoLayout},
		{mode: "repo-cleanliness", audit: auditRepoCleanliness},
		{mode: "agent-quality", audit: auditAgentQuality},
		{mode: "task-state", audit: cachedTaskState},
		{mode: "tasks-md-rows-immutable", audit: auditTasksMdRowsImmutable},
		{mode: "git-hooks", audit: auditGitHooks},
		{mode: "agent-hooks", audit: auditAgentHooks},
		{mode: "start-entrypoint", audit: cachedStartEntrypoint},
		{mode: "spec-format", audit: cachedSpecFormat},
		{mode: "spec-audit-artifacts", audit: auditSpecAuditArtifacts},
		{mode: "arch-boundaries", audit: auditArchitectureBoundaries},
		{mode: "module-contracts", audit: auditModuleContracts},
		{mode: "generated-references", audit: auditGeneratedReferences},
		{mode: "build-baseline", audit: cachedBuildBaseline},
		{mode: "durable-store", audit: cachedDurableStoreBaseline},
		{mode: "test-coverage", audit: cachedTestCoverage},
		{mode: "agents-md-mirror", audit: cachedAgentsMdMirror},
		{mode: "schema-present", audit: cachedSchemaPresent},
		{mode: "spec-task-coverage", audit: auditSpecTaskCoverage},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			got := run(root, test.mode)
			want := test.audit(root)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("run(%q) = %#v, want direct audit result %#v", test.mode, got, want)
			}
		})
	}

	if got := run(root, "unknown-mode"); !reflect.DeepEqual(got, []string{`unknown audit mode "unknown-mode"`}) {
		t.Fatalf("unknown mode result = %#v", got)
	}
}

func TestAuditRepoCleanlinessNamesEveryUntrackedPath(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeFile(t, root, "first.txt", "first\n")
	writeFile(t, root, "nested/second.txt", "second\n")

	failures := auditRepoCleanliness(root)
	for _, want := range []string{"Would remove first.txt", "Would remove nested/"} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing untracked path %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestRepoCleanDryRunLinesTrimsAndDropsEmptyOutput(t *testing.T) {
	output := "\n  Would remove first.txt  \r\n\t\nWould remove nested/\n"
	want := []string{"Would remove first.txt", "Would remove nested/"}
	if got := repoCleanDryRunLines(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("repoCleanDryRunLines() = %#v, want %#v", got, want)
	}
}

func TestAuditSpecFormatRejectsEveryFenceLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec.md", "# Spec\n\n```go\ncode\n```\n")
	failures := auditSpecFormat(root)
	if got, want := len(failures), 2; got != want {
		t.Fatalf("auditSpecFormat() returned %d failures, want %d:\n%s", got, want, strings.Join(failures, "\n"))
	}
	if !containsFailure(failures, "line 3") || !containsFailure(failures, "line 5") {
		t.Fatalf("fence failures must retain exact line numbers:\n%s", strings.Join(failures, "\n"))
	}
}

func TestValidSpecLineRefsRequiresEveryCommaSeparatedAnchor(t *testing.T) {
	for _, value := range []string{
		"docs/spec.md:L1",
		"docs/spec.md:L1-L2",
		"docs/spec.md:L1-2",
		"docs/spec.md:L1, docs/spec.md:L3-L5",
	} {
		if !validSpecLineRefs(value) {
			t.Fatalf("validSpecLineRefs(%q) = false, want true", value)
		}
	}
	for _, value := range []string{
		"spec.md:L1",
		"docs/spec.md:1",
		"docs/spec.md:L1-Lx",
		"docs/spec.md:L1, research/source.md:L2",
	} {
		if validSpecLineRefs(value) {
			t.Fatalf("validSpecLineRefs(%q) = true, want false", value)
		}
	}
}

func TestSpecAuditWorkflowActivationRecognizesCurrentRangeAndChangedEvidence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/tasks.md", minimalTasksMd())
	detail := taskDetail("TASK-0001-Active-Work", "Active", "- [~] Audit the range.", "")
	detail = strings.Replace(detail, "Expected Touch Surfaces: docs/tasks/**", "Expected Touch Surfaces: docs/spec-audit/**", 1)
	detail = strings.Replace(detail, "Spec Lines: none", "Spec Lines: docs/spec.md:L1-L2", 1)
	detail = strings.Replace(detail, "Completion Claim: Done means", "Completion Claim: Audit done means", 1)
	writeFile(t, root, "docs/tasks/TASK-0001-Active-Work.md", detail)
	active, failures := currentTaskIsSpecAuditRange(root)
	if len(failures) != 0 || !active {
		t.Fatalf("currentTaskIsSpecAuditRange() = (%t, %#v), want active", active, failures)
	}

	writeFile(t, root, "docs/spec-audit/state.md", specAuditState("NOT_STARTED", 1, "none", ""))
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc(""))
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "audit@example.invalid")
	gitRun(t, root, "config", "user.name", "Audit Test")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "baseline")
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc("| changed |"))
	if !hasChangedSpecAuditEvidence(root) {
		t.Fatal("changed tracked spec-audit evidence must activate the workflow")
	}
}

func TestHasSpecAuditFilesIgnoresScaffoldingAndFindsEvidence(t *testing.T) {
	root := t.TempDir()
	dir := root + "/claims"
	if hasSpecAuditFiles(dir) {
		t.Fatal("missing directory must not report evidence")
	}
	writeFile(t, root, "claims/README.md", "instructions")
	writeFile(t, root, "claims/.hidden.md", "hidden")
	writeFile(t, root, "claims/note.txt", "not evidence")
	writeFile(t, root, "claims/nested/claim.md", "nested")
	if hasSpecAuditFiles(dir) {
		t.Fatal("README, hidden, non-Markdown, and nested entries must not report direct evidence")
	}
	writeFile(t, root, "claims/CLAIM.md", "claim")
	if !hasSpecAuditFiles(dir) {
		t.Fatal("direct Markdown claim must report evidence")
	}
}

func TestReadSpecAuditResearchFloorsReportsEveryMalformedField(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec-audit/research-floor.md", `# Research Floors

## Research Floors
| Research Floor ID | Source Ref | Linked Atom IDs | Carry-Over Decision | Gap IDs |
|---|---|---|---|---|
|  | research/source.md | ATOM-L0001-01 | CARRY | none |
| bad-id | external/source.md |  | DROP | none |
| RF-L0001-01 | research/source.md | ATOM-L0001-01 | GAP | none |
`)
	floors, failures := readSpecAuditResearchFloors(root)
	if len(floors) != 2 {
		t.Fatalf("readSpecAuditResearchFloors() returned %d keyed rows, want 2", len(floors))
	}
	for _, want := range []string{
		"Research Floor ID is empty",
		`invalid Research Floor ID "bad-id"`,
		"Source Ref must start with research/",
		"Linked Atom IDs is empty",
		`invalid Carry-Over Decision "DROP"`,
		"GAP carry-over decision requires Gap IDs",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestReadSpecAuditAtomsReportsIdentityRangeStatusAndGapFailures(t *testing.T) {
	root := t.TempDir()
	rows := `|  | docs/spec.md:L1 | missing id | workflow | none | owner | tests | PASS | PASS | PASS | PASS | PASS | owner:L1 | MATCH | none |
| bad-id | docs/spec.md:L3 | bad | workflow | none | owner | tests | pending |  | PASS | PASS | PASS | pending | UNKNOWN | bad-gap |
| ATOM-L0001-01 | docs/spec.md:L1 | first | workflow | none | owner | tests | PASS | PASS | PASS | PASS | PASS | owner:L1 | MATCH | none |
| ATOM-L0001-01 | docs/spec.md:L2 | duplicate | workflow | none | owner | tests | FAIL | PASS | PASS | PASS | PASS | owner:L1 | EXCEEDS | none |`
	writeFile(t, root, "docs/spec-audit/spec-atoms.md", specAuditAtomsDoc(rows))
	atoms, failures := readSpecAuditAtoms(root, 2)
	if len(atoms) != 2 {
		t.Fatalf("readSpecAuditAtoms() returned %d keyed atoms, want 2", len(atoms))
	}
	for _, want := range []string{
		"Atom ID is empty",
		`invalid Atom ID "bad-id"`,
		"outside docs/spec.md:L1-L2",
		"Spec Status must be explicit",
		"Research Status must be explicit",
		`invalid Verdict "UNKNOWN"`,
		"duplicate Atom ID ATOM-L0001-01",
		"Spec Status must be PASS for EXCEEDS verdict",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestCompletedSpecAuditRangesRejectBadRowsAndCounts(t *testing.T) {
	state := `# State

## Completed Ranges
| Spec Range | Completed By | Completed At | Atom Count | Research Refs Read | Gap Count |
|---|---|---|---|---|---|
| invalid | - | - | x | y | z |
| docs/spec.md:L1-L2 | - | - | x | y | z |
`
	ranges, failures := readCompletedSpecAuditRanges(state, 2)
	if len(ranges) != 1 {
		t.Fatalf("readCompletedSpecAuditRanges() ranges = %#v", ranges)
	}
	for _, want := range []string{
		`invalid Spec Range "invalid"`,
		"Completed By is empty",
		"Completed At is empty",
		"Atom Count must be numeric",
		"Research Refs Read must be numeric",
		"Gap Count must be numeric",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestSpecAuditRangeArtifactAndFileReferencesFailPrecisely(t *testing.T) {
	root := t.TempDir()
	auditRange := specAuditRange{Start: 1, End: 2, Text: "docs/spec.md:L1-L2"}
	atoms := map[string]specAuditAtom{
		"ATOM-L0001-01": {ID: "ATOM-L0001-01", SpecLines: []specAuditRange{auditRange}},
	}
	failures := auditSpecAuditRangeArtifacts(root, []specAuditRange{auditRange}, atoms)
	if !containsFailure(failures, "completed range artifact missing or unreadable") {
		t.Fatalf("missing range artifact failure = %#v", failures)
	}
	writeFile(t, root, "docs/spec-audit/ranges/L0001-L0002.md", "# Incomplete\n")
	failures = auditSpecAuditRangeArtifacts(root, []specAuditRange{auditRange}, atoms)
	for _, want := range []string{"Range Reality Check", "Atom Table", "Spec Lines Read", "Implementation Evidence", "Gaps", "ATOM-L0001-01"} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing range artifact token failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}

	writeFile(t, root, "docs/evidence.md", "one\n")
	cases := []struct {
		ref  specAuditFileLineRef
		want string
	}{
		{ref: specAuditFileLineRef{}, want: "invalid repo-relative evidence path"},
		{ref: specAuditFileLineRef{Path: "../outside", Line: 1}, want: "invalid repo-relative evidence path"},
		{ref: specAuditFileLineRef{Path: "docs/missing.md", Line: 1}, want: "missing evidence path"},
		{ref: specAuditFileLineRef{Path: "docs/evidence.md", Line: 0}, want: "uses invalid line"},
		{ref: specAuditFileLineRef{Path: "docs/evidence.md", Line: 2}, want: "points outside"},
	}
	for _, test := range cases {
		if got := auditRepoFileLineRef(root, test.ref, "test evidence"); !strings.Contains(got, test.want) {
			t.Fatalf("auditRepoFileLineRef(%+v) = %q, want substring %q", test.ref, got, test.want)
		}
	}
	if got := auditRepoFileLineRef(root, specAuditFileLineRef{Path: "docs", Line: 99}, "directory evidence"); got != "" {
		t.Fatalf("directory evidence should be accepted, got %q", got)
	}
}

func TestGeneratedReferenceAuditFailsClosedAcrossSourceAndBuildBoundaries(t *testing.T) {
	t.Setenv(cacheEnv, "1")
	root := t.TempDir()
	writeFile(t, root, stackConfigRel, `stack: go-cli
project: project
build:
  enabled: false
generated_references:
  enabled: true
`)
	failures := auditGeneratedReferences(root)
	if !containsFailure(failures, "generated-references audit source unavailable") {
		t.Fatalf("missing source failure:\n%s", strings.Join(failures, "\n"))
	}

	sourceRoot := "tools/reconc/harness/template/audits/generated_reference"
	writeFile(t, root, sourceRoot+"/README.md", "no Go source\n")
	failures = auditGeneratedReferences(root)
	if !containsFailure(failures, "source contains no Go files") {
		t.Fatalf("empty source failure:\n%s", strings.Join(failures, "\n"))
	}

	writeFile(t, root, sourceRoot+"/main.go", "package main\n\nfunc broken(\n")
	writeFile(t, root, "tools/reconc/harness/template/go.mod", "module fixture\n\ngo 1.26\n")
	failures = auditGeneratedReferences(root)
	if !containsFailure(failures, "generated-references audit build failed") {
		t.Fatalf("broken source build failure:\n%s", strings.Join(failures, "\n"))
	}
}

func TestBuildBaselineChecksEveryConfiguredArtifactAndTokenClass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, stackConfigRel, `stack: hybrid
project: project
build:
  enabled: true
  language: go
  require_go_mod: true
  require_cargo_toml: true
  require_frontend_package: true
  require_build_runner: true
  require_build_runner_test: true
  backend_entrypoints: [project]
  go_mod_tokens: ["module required", "go 1.26"]
  cargo_toml_tokens: ["[package]", "name ="]
  frontend_package_tokens: ['"private": true', '"packageManager": "bun@', '"build"']
  forbidden_frontend_tokens: ['"packageManager": "npm@']
  build_runner_tokens: ['case "build":', 'case "test":']
generated_references:
  enabled: false
`)
	failures := auditBuildBaseline(root)
	for _, want := range []string{
		"build baseline missing go.mod",
		"build baseline missing Cargo.toml",
		"build baseline missing frontend/package.json",
		"build baseline missing scripts/build/build.go",
		"build baseline missing scripts/build/build_test.go",
		"build baseline missing backend/project/main.go",
		"read go.mod:",
		"read Cargo.toml:",
		"read frontend/package.json:",
		"read scripts/build/build.go:",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing absent-artifact failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}

	writeFile(t, root, "go.mod", "module wrong\n")
	writeFile(t, root, "Cargo.toml", "[workspace]\n")
	writeFile(t, root, "frontend/package.json", `{"packageManager": "npm@10"}`)
	writeFile(t, root, "scripts/build/build.go", "package main\n")
	writeFile(t, root, "scripts/build/build_test.go", "package main\n")
	writeFile(t, root, "backend/project/main.go", "package main\n")
	failures = auditBuildBaseline(root)
	for _, want := range []string{
		`go.mod missing "module required"`,
		`go.mod missing "go 1.26"`,
		`Cargo.toml missing "[package]"`,
		`Cargo.toml missing "name ="`,
		`frontend/package.json missing "private": true`,
		`frontend/package.json missing "packageManager": "bun@`,
		`uses forbidden package manager token "packageManager": "npm@`,
		`scripts/build/build.go missing build-baseline token "case \"build\":"`,
		`scripts/build/build.go missing build-baseline token "case \"test\":"`,
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing token failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestNormalizeStackConfigMaterializesEveryEnabledDefault(t *testing.T) {
	cfg := stackConfig{
		Build: buildStackConfig{
			Enabled: true, RequireGoMod: true, RequireCargoToml: true,
			RequireFrontendPackage: true, RequireBuildRunner: true,
		},
		DurableStore: durableStoreStackConfig{Enabled: true},
	}
	normalizeStackConfig(&cfg)
	if cfg.Stack != "go-cli" || cfg.Project != "project" || cfg.Layout != "auto" || cfg.Build.Language != "go" {
		t.Fatalf("identity defaults = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Build.BackendEntrypoints, []string{"project"}) ||
		!reflect.DeepEqual(cfg.Build.GoModTokens, []string{"module ", "go "}) ||
		!reflect.DeepEqual(cfg.Build.CargoTomlTokens, []string{"[package]", "name ="}) {
		t.Fatalf("backend token defaults = %+v", cfg.Build)
	}
	if len(cfg.Build.FrontendPackageTokens) != 5 || len(cfg.Build.ForbiddenFrontendTokens) != 3 ||
		len(cfg.Build.BuildRunnerTokens) != 5 {
		t.Fatalf("frontend/runner defaults = %+v", cfg.Build)
	}
	if len(cfg.DurableStore.StoreFiles) != 3 || len(cfg.DurableStore.MigrationGoFiles) != 2 ||
		cfg.DurableStore.InitialSQL != "db/migrations/{project}/core/001_initial.sql" ||
		len(cfg.DurableStore.StoreGoTokens) != 5 {
		t.Fatalf("durable-store defaults = %+v", cfg.DurableStore)
	}
}

func TestStackRootRelExpandsOnlyDocumentedProjectPlaceholder(t *testing.T) {
	cfg := stackConfig{Project: "alpha"}
	got := stackRootRel(cfg, "myproject/{project}/project-file")
	want := "myproject/alpha/project-file"
	if got != want {
		t.Fatalf("stackRootRel() = %q, want %q", got, want)
	}
}

func TestSchedulingHelpersRejectEveryAmbiguousControlValue(t *testing.T) {
	done := map[string]bool{"TASK-0001-Done": true}
	if !taskExecutable(taskDetailInfo{state: "Active"}, done) ||
		!taskExecutable(taskDetailInfo{state: "Queued", dependencies: []string{"TASK-0001-Done"}}, done) {
		t.Fatal("active or dependency-ready queued tasks must be executable")
	}
	if taskExecutable(taskDetailInfo{state: "Blocked"}, done) ||
		taskExecutable(taskDetailInfo{state: "Queued", dependencies: []string{"TASK-0002-Open"}}, done) {
		t.Fatal("blocked or dependency-waiting tasks must not be executable")
	}

	for _, value := range []string{"P0", "P1", "P2", "P3"} {
		if !validPriority(value) {
			t.Fatalf("validPriority(%q) = false", value)
		}
	}
	for _, value := range []string{"Active", "Queued", "Blocked", "Paused", "Done"} {
		if !validStatusState(value) {
			t.Fatalf("validStatusState(%q) = false", value)
		}
	}
	if validPriority("P4") || validStatusState("Pending") {
		t.Fatal("unknown priority/status must be rejected")
	}

	for _, surface := range []string{"", "/absolute", "../escape", "docs\tbad", ".", "*", "**", "codebase", "docs", "research"} {
		if !invalidTouchSurface(surface) {
			t.Fatalf("invalidTouchSurface(%q) = false", surface)
		}
	}
	if surface := filepath.Join("backend", "project", "**"); invalidTouchSurface(surface) {
		t.Fatalf("invalidTouchSurface(%q) = true for a platform-native bounded path", surface)
	}
	if invalidTouchSurface("backend/project/**") {
		t.Fatal("bounded owner glob must be valid")
	}

	invalid := taskDetailInfo{
		state: "Active", priority: "P4", dependsRaw: "", parallelGroup: "bad group",
		touchSurfaces: []string{"docs"}, orderRationale: "short", scopeType: "Unknown",
		specLinesRaw: "docs/spec.md:1", researchRefs: []string{"external/ref"},
		completionClaim: "short",
	}
	failures := auditSchedulingFields("docs/tasks/TASK-0001.md", invalid, true)
	for _, want := range []string{
		"Priority must be",
		"Parallel Group must be",
		"invalid surface",
		"Depends On must be",
		"Order Rationale must explain",
		"Scope Type must be",
		"Spec Lines must use",
		`invalid ref "external/ref"`,
		"Completion Claim must state",
	} {
		if !containsFailure(failures, want) {
			t.Fatalf("missing scheduling failure %q:\n%s", want, strings.Join(failures, "\n"))
		}
	}
}

func TestTaskScopeTruthRejectsReductionAndUnconsumedResearch(t *testing.T) {
	complete := taskDetailInfo{scopeType: "Complete Feature"}
	failures := auditTaskScopeTruth("task.md", "remaining work deferred to later", complete, " ")
	if !containsFailure(failures, "must not contain gap/deferred/follow-up/partial language") {
		t.Fatalf("complete-feature reduction failure = %#v", failures)
	}

	slice := taskDetailInfo{scopeType: "Slice"}
	failures = auditTaskScopeTruth("task.md", "remaining work deferred to later", slice, " ")
	if !containsFailure(failures, "linked to concrete follow-up TASKs") {
		t.Fatalf("unlinked slice follow-up failure = %#v", failures)
	}
	if failures := auditTaskScopeTruth("task.md", "remaining work in TASK-0002-Follow-Up", slice, " "); len(failures) != 0 {
		t.Fatalf("linked slice follow-up failed: %#v", failures)
	}

	research := taskDetailInfo{scopeType: "Audit Repair", researchRefs: []string{"research/source.md"}}
	failures = auditTaskScopeTruth("task.md", "## Technical Plan\nImplement.\n\n## Acceptance\nPass.", research, " ")
	if !containsFailure(failures, "must require reading/adapting") {
		t.Fatalf("unconsumed research failure = %#v", failures)
	}

	doneContent := "## Final Reality Check\n\nRemaining work is deferred to later."
	failures = auditTaskScopeTruth("task.md", doneContent, complete, "x")
	if !containsFailure(failures, "cannot close with unresolved follow-up") {
		t.Fatalf("done reduced-scope failure = %#v", failures)
	}
}

func TestParseSpecLineRefRejectsZeroReversedAndMalformedRanges(t *testing.T) {
	for _, ref := range []string{"docs/spec.md:L0", "docs/spec.md:L4-L3", "spec.md:L1", "docs/spec.md:Lx"} {
		if start, end, ok := parseSpecLineRef(ref); ok || start != 0 || end != 0 {
			t.Fatalf("parseSpecLineRef(%q) = (%d, %d, %t), want invalid", ref, start, end, ok)
		}
	}
	if start, end, ok := parseSpecLineRef("docs/spec.md:L2-4"); !ok || start != 2 || end != 4 {
		t.Fatalf("valid range = (%d, %d, %t)", start, end, ok)
	}
}

func TestWriteBatchJSONReturnsProtocolExitCodesAndValidJSON(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec.md", "# Spec\n")

	output, exitCode := captureProcessStdout(t, func() int {
		return writeBatchJSON(root, nil)
	})
	if exitCode != 2 || !strings.Contains(output, `"no audit modes provided"`) {
		t.Fatalf("empty batch = (exit %d, %q)", exitCode, output)
	}

	output, exitCode = captureProcessStdout(t, func() int {
		return writeBatchJSON(root, []string{"spec-format"})
	})
	if exitCode != 0 || !strings.Contains(output, `"mode":"spec-format"`) && !strings.Contains(output, `"mode": "spec-format"`) {
		t.Fatalf("passing batch = (exit %d, %q)", exitCode, output)
	}
}

func captureProcessStdout(t *testing.T, run func() int) (string, int) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = writer
	exitCode := run()
	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(body), exitCode
}

func TestSpecAuditStateAndGapReadersCoverActivationAndSectionBoundaries(t *testing.T) {
	root := t.TempDir()
	active, failures := specAuditWorkflowActive(root)
	if active || !containsFailure(failures, "read docs/spec-audit/state.md") {
		t.Fatalf("missing state activation = (%t, %#v)", active, failures)
	}

	writeFile(t, root, "docs/spec-audit/state.md", specAuditState("IN_PROGRESS", 2, "none", ""))
	active, failures = specAuditWorkflowActive(root)
	if !active || len(failures) != 0 {
		t.Fatalf("in-progress state activation = (%t, %#v)", active, failures)
	}

	writeFile(t, root, "docs/spec-audit/state.md", specAuditState("NOT_STARTED", 2, "none", ""))
	writeFile(t, root, "docs/spec-audit/claims/CLAIM.md", "claim\n")
	active, failures = specAuditWorkflowActive(root)
	if !active || len(failures) != 0 {
		t.Fatalf("claim-file activation = (%t, %#v)", active, failures)
	}

	writeFile(t, root, "docs/spec-audit/gaps.md", `# Gaps

## Gap Records

### GAP-L0001-01: First
- Severity: high

### GAP-L0002-01: Second
- Severity: low

## Footer
ignored
`)
	gaps, gapFailures := readSpecAuditGaps(root)
	if len(gapFailures) != 0 || len(gaps) != 2 ||
		!strings.Contains(gaps["GAP-L0001-01"], "Severity: high") ||
		!strings.Contains(gaps["GAP-L0002-01"], "Severity: low") ||
		strings.Contains(gaps["GAP-L0002-01"], "ignored") {
		t.Fatalf("readSpecAuditGaps() = (%#v, %#v)", gaps, gapFailures)
	}

	if value, ok := parseStateInt("- Count: 12", "Count"); !ok || value != 12 {
		t.Fatalf("parseStateInt valid = (%d, %t)", value, ok)
	}
	if _, ok := parseStateInt("- Count: invalid", "Count"); ok {
		t.Fatal("parseStateInt must reject non-integer values")
	}
	if _, ok := parseStateInt("", "Count"); ok {
		t.Fatal("parseStateInt must reject missing values")
	}
}
