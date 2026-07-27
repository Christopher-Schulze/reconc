package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestValidatePlanRejectsEveryStructuralContradiction(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	valid, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if len(valid.Actions) < 2 {
		t.Fatalf("fixture produced only %d actions", len(valid.Actions))
	}

	tests := []struct {
		name   string
		mutate func(*Plan)
		want   string
	}{
		{name: "format", mutate: func(plan *Plan) { plan.FormatVersion = "future" }, want: "format_version"},
		{name: "repository", mutate: func(plan *Plan) { plan.RepoRoot = filepath.Join(repo, "..", filepath.Base(repo)) }, want: "not canonical"},
		{name: "product version", mutate: func(plan *Plan) { plan.ProductVersion = " " }, want: "product_version"},
		{name: "profile", mutate: func(plan *Plan) { plan.Selection.Profile = "unknown" }, want: "unknown bootstrap profile"},
		{name: "missing default packs", mutate: func(plan *Plan) { plan.Selection.Packs = nil }, want: "default packs"},
		{name: "reordered default packs", mutate: func(plan *Plan) {
			plan.Selection.Packs[0], plan.Selection.Packs[1] = plan.Selection.Packs[1], plan.Selection.Packs[0]
		}, want: "preserve profile default pack"},
		{name: "unknown pack", mutate: func(plan *Plan) { plan.Selection.Packs = append(plan.Selection.Packs, "unknown-pack") }, want: "pack"},
		{name: "unknown hook", mutate: func(plan *Plan) { plan.Selection.Hooks = []string{"unknown"} }, want: "unsupported hook"},
		{name: "duplicate hooks", mutate: func(plan *Plan) {
			plan.Selection.Hooks = []string{hooks.KindClaudeCode, hooks.KindClaudeCode}
		}, want: "uniquely sorted"},
		{name: "owned wrapper trust", mutate: func(plan *Plan) {
			plan.Selection.Profile = ProfileGoverned
			plan.Selection.TrustExistingWrapper = true
		}, want: "cannot trust"},
		{name: "relative binary", mutate: func(plan *Plan) {
			plan.Selection.Binary = &BinarySelection{SourcePath: "reconc", SHA256: strings.Repeat("a", 64), OS: runtime.GOOS, Arch: runtime.GOARCH}
		}, want: "source and checksum"},
		{name: "binary platform", mutate: func(plan *Plan) {
			plan.Selection.Binary = &BinarySelection{SourcePath: filepath.Join(repo, "reconc"), SHA256: strings.Repeat("a", 64), OS: "plan9", Arch: "amd64"}
		}, want: "unsupported"},
		{name: "incomplete action", mutate: func(plan *Plan) { plan.Actions[0].Component = "" }, want: "incomplete"},
		{name: "escaping action", mutate: func(plan *Plan) { plan.Actions[0].Path = "../escape" }, want: "escapes repository"},
		{name: "unsupported mode", mutate: func(plan *Plan) { plan.Actions[0].Mode = 0o600 }, want: "unsupported mode"},
		{name: "unordered actions", mutate: func(plan *Plan) { plan.Actions[1].Path = plan.Actions[0].Path }, want: "uniquely sorted"},
		{name: "invalid action state", mutate: func(plan *Plan) { plan.Actions[0].State = "future" }, want: "invalid state"},
		{name: "contradictory create", mutate: func(plan *Plan) { plan.Actions[0].ExistingKind = "file" }, want: "contradictory existing state"},
		{name: "contradictory unchanged", mutate: func(plan *Plan) {
			plan.Actions[0].State = ActionUnchanged
			plan.Actions[0].ExistingKind = "file"
			plan.Actions[0].ExistingSHA256 = strings.Repeat("b", 64)
		}, want: "contradictory existing state"},
		{name: "invalid conflict", mutate: func(plan *Plan) {
			plan.Actions[0].State = ActionConflict
			plan.Actions[0].ExistingKind = "file"
			plan.Actions[0].CandidatePath = "wrong"
		}, want: "invalid candidate state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneBootstrapPlan(t, valid)
			test.mutate(plan)
			plan.PlanDigest, err = computePlanDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidatePlan() error = %v, want substring %q", err, test.want)
			}
		})
	}

	if err := ValidatePlan(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("ValidatePlan(nil) error = %v", err)
	}
	badDigest := cloneBootstrapPlan(t, valid)
	badDigest.PlanDigest = strings.Repeat("0", 64)
	if err := ValidatePlan(badDigest); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("bad digest error = %v", err)
	}
}

func TestPlanPersistenceRejectsAmbiguousOrForeignState(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	missing := filepath.Join(root, "missing.json")
	if _, err := LoadPlan(missing); err == nil || !strings.Contains(err.Error(), "open bootstrap plan") {
		t.Fatalf("missing plan error = %v", err)
	}
	unknown := filepath.Join(root, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlan(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
	multiple := filepath.Join(root, "multiple.json")
	data, err := encodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(multiple, append(data, []byte("{}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlan(multiple); err == nil || !strings.Contains(err.Error(), "exactly one JSON document") {
		t.Fatalf("multiple-document error = %v", err)
	}

	outputDirectory := filepath.Join(root, "output")
	if err := os.Mkdir(outputDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePlan(outputDirectory, plan); err == nil || !strings.Contains(err.Error(), "read bootstrap plan output") {
		t.Fatalf("directory output error = %v", err)
	}
	blockedParent := filepath.Join(root, "parent-file")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePlan(filepath.Join(blockedParent, "plan.json"), plan); err == nil || !strings.Contains(err.Error(), "read bootstrap plan output") {
		t.Fatalf("blocked parent error = %v", err)
	}

	foreignRepo := t.TempDir()
	foreign, err := BuildPlan(Request{RepoRoot: foreignRepo, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "plan.json")
	if action, err := WritePlan(planPath, plan); err != nil || action != "created" {
		t.Fatalf("WritePlan() = %q, %v", action, err)
	}
	if _, err := ReplacePlan(planPath, foreign); err == nil || !strings.Contains(err.Error(), "refuse to replace bootstrap plan") {
		t.Fatalf("cross-repository replacement error = %v", err)
	}

	replacement := cloneBootstrapPlan(t, plan)
	replacement.ProductVersion = "next-version"
	replacement.PlanDigest, err = computePlanDigest(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if action, err := ReplacePlan(planPath, replacement); err != nil || action != "replaced" {
		t.Fatalf("ReplacePlan() = %q, %v", action, err)
	}
	loaded, err := LoadPlan(planPath)
	if err != nil || loaded.ProductVersion != "next-version" {
		t.Fatalf("replaced plan = %+v, %v", loaded, err)
	}
}

func TestBinarySelectionAndWriteFailureDiagnostics(t *testing.T) {
	if selection, err := CurrentBinarySelection(); err != nil || selection.SourcePath == "" || !validSHA256(selection.SHA256) {
		t.Fatalf("CurrentBinarySelection() = %+v, %v", selection, err)
	}
	if _, err := BinarySelectionFor("missing", "", runtime.GOOS, runtime.GOARCH); err == nil || !strings.Contains(err.Error(), "resolve binary artifact identity") {
		t.Fatalf("missing artifact error = %v", err)
	}
	if _, err := BinarySelectionFor(t.TempDir(), "", runtime.GOOS, runtime.GOARCH); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory artifact error = %v", err)
	}
	artifact := filepath.Join(t.TempDir(), "reconc")
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BinarySelectionFor(artifact, strings.Repeat("0", 64), runtime.GOOS, runtime.GOARCH); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum error = %v", err)
	}
	if _, err := StableBinaryName("windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	if _, err := StableBinaryName("windows", "arm64"); err == nil {
		t.Fatal("unsupported Windows architecture was accepted")
	}

	primary := errors.New("primary")
	got := combineWriteFailure("write", primary, errors.New("close"), errors.New("cleanup")).Error()
	for _, part := range []string{"write: primary", "close: close", "cleanup: cleanup"} {
		if !strings.Contains(got, part) {
			t.Fatalf("combined error %q lacks %q", got, part)
		}
	}
	if got := combineWriteFailure("write", primary, nil, os.ErrNotExist).Error(); strings.Contains(got, "cleanup") {
		t.Fatalf("not-exist cleanup leaked into error: %q", got)
	}
}

func TestBootstrapTransactionHelpersPreserveCreateOnlyBoundaries(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	artifact := desiredArtifact{component: "test", path: "nested/file.txt", mode: 0o644, content: []byte("payload")}
	expected := bytesSHA256(artifact.content)

	record, directories, err := publishArtifact(root, artifact, artifact.path, expected, digest)
	if err != nil {
		t.Fatal(err)
	}
	if record.path != filepath.Join(root, "nested", "file.txt") || len(directories) != 1 {
		t.Fatalf("publication record = %+v, directories=%+v", record, directories)
	}
	closeCreatedDirectoryIdentities(directories)
	if body, err := os.ReadFile(record.path); err != nil || string(body) != "payload" {
		t.Fatalf("published body = %q, %v", body, err)
	}

	if _, _, err := publishArtifact(root, artifact, artifact.path, expected, digest); err == nil || !strings.Contains(err.Error(), "publish bootstrap artifact") {
		t.Fatalf("existing target publication error = %v", err)
	}
	bad := desiredArtifact{component: "test", path: "bad.txt", mode: 0o644, content: []byte("payload")}
	if _, _, err := publishArtifact(root, bad, bad.path, strings.Repeat("0", 64), digest); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum mismatch error = %v", err)
	}
	stageArtifact := desiredArtifact{component: "test", path: "staged.txt", mode: 0o644, content: []byte("payload")}
	stage := filepath.Join(root, ".staged.txt.reconc-bootstrap-"+digest[:12]+".tmp")
	if err := os.WriteFile(stage, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := publishArtifact(root, stageArtifact, stageArtifact.path, expected, digest); err == nil || !strings.Contains(err.Error(), "create bootstrap staging") {
		t.Fatalf("occupied staging error = %v", err)
	}

	copySource := filepath.Join(root, "copy-source")
	copyTarget := filepath.Join(root, "copy-target")
	if err := os.WriteFile(copySource, []byte("copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyStagedExclusive(copySource, copyTarget, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyStagedExclusive(copySource, copyTarget, 0o600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive copy existing-target error = %v", err)
	}
	if err := copyStagedExclusive(filepath.Join(root, "missing"), filepath.Join(root, "unused"), 0o600); err == nil {
		t.Fatal("exclusive copy accepted a missing source")
	}

	inline := filepath.Join(root, "inline")
	file, err := os.OpenFile(inline, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactBody(file, desiredArtifact{content: []byte("inline")}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	binaryTarget, err := os.OpenFile(filepath.Join(root, "binary-copy"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactBody(binaryTarget, desiredArtifact{sourcePath: copySource}); err != nil {
		t.Fatal(err)
	}
	if err := binaryTarget.Close(); err != nil {
		t.Fatal(err)
	}
	missingTarget, err := os.OpenFile(filepath.Join(root, "missing-copy"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactBody(missingTarget, desiredArtifact{sourcePath: filepath.Join(root, "missing-source")}); err == nil || !strings.Contains(err.Error(), "open binary source") {
		t.Fatalf("missing binary source error = %v", err)
	}
	if err := missingTarget.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapTransactionValidationAndRollbackBranches(t *testing.T) {
	root := t.TempDir()
	content := []byte("content")
	artifact := desiredArtifact{component: "test", path: "file.txt", mode: 0o644, content: content}
	action := Action{Component: "test", Path: "file.txt", Mode: 0o644, DesiredSHA256: bytesSHA256(content), State: ActionCreate, ExistingKind: "absent"}
	if _, err := validateArtifactsMatchPlan([]desiredArtifact{artifact}, nil); err == nil || !strings.Contains(err.Error(), "count changed") {
		t.Fatalf("artifact count error = %v", err)
	}
	missing := action
	missing.Path = "other.txt"
	if _, err := validateArtifactsMatchPlan([]desiredArtifact{artifact}, []Action{missing}); err == nil || !strings.Contains(err.Error(), "no longer resolves") {
		t.Fatalf("missing artifact error = %v", err)
	}
	drifted := action
	drifted.Mode = 0o755
	if _, err := validateArtifactsMatchPlan([]desiredArtifact{artifact}, []Action{drifted}); err == nil || !strings.Contains(err.Error(), "drifted") {
		t.Fatalf("artifact drift error = %v", err)
	}
	if resolved, err := validateArtifactsMatchPlan([]desiredArtifact{artifact}, []Action{action}); err != nil || resolved[action.Path].component != "test" {
		t.Fatalf("valid artifact map = %+v, %v", resolved, err)
	}

	plan := &Plan{RepoRoot: root, Actions: []Action{action}}
	if err := preflightPlanState(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, action.Path), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preflightPlanState(plan); err == nil || !strings.Contains(err.Error(), "appeared after planning") {
		t.Fatalf("appeared target error = %v", err)
	}
	info, err := os.Stat(filepath.Join(root, action.Path))
	if err != nil {
		t.Fatal(err)
	}
	existing := action
	existing.State = ActionUnchanged
	existing.ExistingKind = "file"
	existing.ExistingMode = uint32(info.Mode().Perm())
	existing.ExistingSHA256 = bytesSHA256(content)
	plan.Actions = []Action{existing}
	if err := preflightPlanState(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, action.Path), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preflightPlanState(plan); err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("changed target error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, action.Path)); err != nil {
		t.Fatal(err)
	}
	if err := preflightPlanState(plan); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("missing existing target error = %v", err)
	}

	createdPath := filepath.Join(root, "created.txt")
	if err := os.WriteFile(createdPath, []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := captureCreatedRecord(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack, err := rollbackCreated(root, []createdRecord{record}, nil); err != nil || strings.Join(rolledBack, ",") != "created.txt" {
		t.Fatalf("rollback = %v, %v", rolledBack, err)
	}
	if err := removeCreatedRecord(record); err != nil {
		t.Fatalf("idempotent removal = %v", err)
	}
	changedPath := filepath.Join(root, "changed.txt")
	if err := os.WriteFile(changedPath, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedRecord, err := captureCreatedRecord(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changedPath, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeCreatedRecord(changedRecord); err == nil || !strings.Contains(err.Error(), "externally changed") {
		t.Fatalf("changed record removal error = %v", err)
	}
	if _, err := captureCreatedRecord(root); err == nil || !strings.Contains(err.Error(), "not a real regular file") {
		t.Fatalf("directory record error = %v", err)
	}

	parent := filepath.Join(root, "one", "two")
	directories, err := createSafeParents(root, parent)
	if err != nil || len(directories) != 2 {
		t.Fatalf("createSafeParents() = %+v, %v", directories, err)
	}
	if got := deepestDirectoriesFirst(directories); len(got) != 2 || got[0].path != parent {
		t.Fatalf("directory order = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(parent, "foreign"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rollbackCreated(root, nil, directories); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("non-empty directory rollback error = %v", err)
	}
	if _, err := createSafeParents(root, filepath.Join(root, "..", "escape")); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping parent error = %v", err)
	}
	fileParent := filepath.Join(root, "file-parent")
	if err := os.WriteFile(fileParent, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := createSafeParents(root, filepath.Join(fileParent, "child")); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("file parent error = %v", err)
	}
}

func TestApplyRejectsVersionAndBlockingIssueBeforeMutation(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileMinimal}, "planned")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, "running"); err == nil || !strings.Contains(err.Error(), "not the running") {
		t.Fatalf("version mismatch error = %v", err)
	}
	blocked := cloneBootstrapPlan(t, plan)
	blocked.BlockingIssues = []string{"operator action required"}
	blocked.PlanDigest, err = computePlanDigest(blocked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(blocked, "planned"); err == nil || !strings.Contains(err.Error(), "operator action required") {
		t.Fatalf("blocking issue error = %v", err)
	}
	if got := bootstrapHookRuntimeName(hooks.KindDevinCLI); got != "devin" {
		t.Fatalf("Devin runtime name = %q", got)
	}
	if got := bootstrapHookRuntimeName(hooks.KindCodex); got != hooks.KindCodex {
		t.Fatalf("Codex runtime name = %q", got)
	}
	if got := quoteBootstrapArgument(`a"b`); got != `"a\"b"` {
		t.Fatalf("quoted argument = %q", got)
	}
	if got := joinApplyRollbackError(errors.New("apply"), errors.New("rollback")).Error(); !strings.Contains(got, "apply") || !strings.Contains(got, "rollback") {
		t.Fatalf("joined apply error = %q", got)
	}
}

func cloneBootstrapPlan(t *testing.T, source *Plan) *Plan {
	t.Helper()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var clone Plan
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}
