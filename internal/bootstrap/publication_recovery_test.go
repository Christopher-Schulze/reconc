package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublishArtifactRecoversOnlyExactReservedStage(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	artifact := desiredArtifact{
		component: "recovery-test",
		path:      "owned.txt",
		mode:      0o644,
		content:   []byte("owned\n"),
	}
	expected := bytesSHA256(artifact.content)
	exactStage := filepath.Join(root, ".owned.txt.reconc-bootstrap-"+digest[:12]+".tmp")
	lookalike := filepath.Join(root, ".owned.txt.reconc-bootstrap-"+strings.Repeat("b", 12)+".tmp")
	if err := os.WriteFile(exactStage, artifact.content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lookalike, []byte("user-owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	record, directories, err := publishArtifact(root, artifact, artifact.path, expected, digest)
	defer closeCreatedDirectoryIdentities(directories)
	if err != nil {
		t.Fatal(err)
	}
	defer record.close()
	if _, err := os.Lstat(exactStage); !os.IsNotExist(err) {
		t.Fatalf("exact stale stage remains: %v", err)
	}
	if body, err := os.ReadFile(lookalike); err != nil || string(body) != "user-owned\n" {
		t.Fatalf("lookalike changed: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(record.path); err != nil || string(body) != "owned\n" {
		t.Fatalf("recovered publication = %q, %v", body, err)
	}
}

func TestPublishArtifactRecoversPublishedTargetWithExactStageEvidence(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("c", 64)
	artifact := desiredArtifact{
		component: "recovery-test",
		path:      "owned.txt",
		mode:      0o644,
		content:   []byte("owned\n"),
	}
	expected := bytesSHA256(artifact.content)
	stage := filepath.Join(root, ".owned.txt.reconc-bootstrap-"+digest[:12]+".tmp")
	target := filepath.Join(root, artifact.path)
	if err := os.WriteFile(stage, artifact.content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, artifact.content, 0o600); err != nil {
		t.Fatal(err)
	}

	record, directories, err := publishArtifact(root, artifact, artifact.path, expected, digest)
	defer closeCreatedDirectoryIdentities(directories)
	if err != nil {
		t.Fatal(err)
	}
	defer record.close()
	if record.path != target {
		t.Fatalf("recovered record path = %q, want %q", record.path, target)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("published-stage residue remains: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || !modeSatisfies(info.Mode(), artifact.mode) {
		t.Fatalf("recovered target mode = %v, err=%v", info, err)
	}
}

func TestApplyRecoversCrashAfterTargetPublication(t *testing.T) {
	bootstrapTestHome(t)
	root := t.TempDir()
	plan, err := BuildPlan(Request{RepoRoot: root, Profile: ProfileMinimal}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := buildDesiredArtifacts(root, plan.Selection, plan.ProductVersion)
	if err != nil {
		t.Fatal(err)
	}
	var crashed desiredArtifact
	for _, artifact := range artifacts {
		if artifact.sourcePath == "" {
			crashed = artifact
			break
		}
	}
	if crashed.path == "" {
		t.Fatal("bootstrap fixture has no inline artifact")
	}
	target := filepath.Join(root, filepath.FromSlash(crashed.path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(
		filepath.Dir(target),
		"."+filepath.Base(target)+".reconc-bootstrap-"+plan.PlanDigest[:12]+".tmp",
	)
	if err := os.WriteFile(stage, crashed.content, os.FileMode(crashed.mode)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, crashed.content, os.FileMode(crashed.mode)); err != nil {
		t.Fatal(err)
	}

	report, err := Apply(plan, plan.ProductVersion)
	if err != nil || report.Status != ApplyComplete {
		t.Fatalf("crash recovery apply: report=%+v err=%v", report, err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("crash stage remains after apply: %v", err)
	}
	verification, err := Verify(plan)
	if err != nil || !verification.Valid {
		t.Fatalf("recovered plan verification: verification=%+v err=%v", verification, err)
	}
}

func TestPublishArtifactPreservesAmbiguousReservedStage(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("d", 64)
	artifact := desiredArtifact{
		component: "recovery-test",
		path:      "owned.txt",
		mode:      0o644,
		content:   []byte("owned\n"),
	}
	stage := filepath.Join(root, ".owned.txt.reconc-bootstrap-"+digest[:12]+".tmp")
	if err := os.WriteFile(stage, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := publishArtifact(root, artifact, artifact.path, bytesSHA256(artifact.content), digest); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous stage error = %v", err)
	}
	if body, err := os.ReadFile(stage); err != nil || string(body) != "foreign\n" {
		t.Fatalf("ambiguous stage changed: body=%q err=%v", body, err)
	}
	if _, err := os.Lstat(filepath.Join(root, artifact.path)); !os.IsNotExist(err) {
		t.Fatalf("ambiguous recovery published a target: %v", err)
	}
}

func TestPublishArtifactReturnsRollbackCapableRecordAfterParentReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit deterministic replacement of this open directory handle")
	}
	root := t.TempDir()
	parentPath := filepath.Join(root, "parent")
	oldParentPath := filepath.Join(root, "parent-old")
	artifact := desiredArtifact{
		component: "recovery-test",
		path:      "parent/owned.txt",
		mode:      0o644,
		content:   []byte("owned\n"),
	}
	hooks := publicationHooks{beforeParentValidation: func(target string) error {
		if err := os.Rename(parentPath, oldParentPath); err != nil {
			return err
		}
		if err := os.Mkdir(parentPath, 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, []byte("external\n"), 0o644)
	}}
	record, directories, err := publishArtifactWithHooks(
		root, artifact, artifact.path, bytesSHA256(artifact.content), strings.Repeat("e", 64), hooks,
	)
	defer closeCreatedDirectoryIdentities(directories)
	if err == nil || record.path == "" || record.file == nil || record.parent == nil {
		t.Fatalf("parent replacement result: record=%+v err=%v", record, err)
	}
	if body, readErr := os.ReadFile(filepath.Join(parentPath, "owned.txt")); readErr != nil || string(body) != "external\n" {
		t.Fatalf("external replacement changed: body=%q err=%v", body, readErr)
	}
	if err := os.Remove(filepath.Join(parentPath, "owned.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldParentPath, parentPath); err != nil {
		t.Fatal(err)
	}
	if err := removeCreatedRecord(&record); err != nil {
		t.Fatalf("retry rollback with preserved record: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parentPath, "owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("recovered rollback target remains: %v", err)
	}
}

type swappingJSONTarget struct {
	path string
}

func (target *swappingJSONTarget) UnmarshalJSON(body []byte) error {
	if err := os.Rename(target.path, target.path+".opened"); err != nil {
		return err
	}
	return os.WriteFile(target.path, body, 0o600)
}

func TestDecodeStrictJSONSnapshotRejectsReplacementDuringDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.json")
	body, err := json.Marshal(map[string]bool{"valid": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	target := &swappingJSONTarget{path: path}
	if err := decodeStrictJSONSnapshot(path, "test payload", 1024, target); err == nil ||
		!strings.Contains(err.Error(), "changed") {
		t.Fatalf("replacement during decode error = %v", err)
	}
}
