package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditTaskSpecBindingsAcceptsExactSemanticBinding(t *testing.T) {
	root := t.TempDir()
	spec := "Browser downloads stay inside the owned directory.\nDisk quota rejects oversized downloads.\n"
	writeFile(t, root, "docs/spec.md", spec)
	digest := taskSpecTestDigest("Browser downloads stay inside the owned directory.")
	content := taskSpecTestDetail("docs/spec.md:L1", "docs/spec.md:L1@sha256:"+digest+"@browser+downloads")

	failures := auditTaskSpecBindings(root, "docs/tasks/TASK-0001.md", content, taskDetailInfo{
		specLinesRaw:    "docs/spec.md:L1",
		completionClaim: "Browser downloads remain owned and bounded by disk quota.",
	}, true)
	if len(failures) != 0 {
		t.Fatalf("valid semantic binding failed:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskSpecBindingsAcceptsCRLFTaskContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec.md", "Browser downloads stay owned.\r\n")
	digest := taskSpecTestDigest("Browser downloads stay owned.")
	content := strings.ReplaceAll(taskSpecTestDetail("docs/spec.md:L1", "docs/spec.md:L1@sha256:"+digest+"@browser+downloads"), "\n", "\r\n")
	info := taskDetailInfo{specLinesRaw: "docs/spec.md:L1", completionClaim: "Browser downloads stay owned."}
	if failures := auditTaskSpecBindings(root, "crlf", content, info, true); len(failures) != 0 {
		t.Fatalf("CRLF TASK and spec content must validate:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskSpecBindingsRejectsOriginalMismatchClass(t *testing.T) {
	root := t.TempDir()
	specLine := "Policy integration makes browser requests go through Policy Engine."
	writeFile(t, root, "docs/spec.md", specLine+"\n")
	digest := taskSpecTestDigest(specLine)
	content := taskSpecTestDetail("docs/spec.md:L1", "docs/spec.md:L1@sha256:"+digest+"@policy+downloads")

	failures := auditTaskSpecBindings(root, "docs/tasks/TASK-2356-C.md", content, taskDetailInfo{
		specLinesRaw:    "docs/spec.md:L1",
		completionClaim: "Owned downloads enforce content size, disk quota, and cleanup.",
	}, true)
	if !containsFailure(failures, `term "policy" does not occur in the TASK claim surface`) ||
		!containsFailure(failures, `term "downloads" does not occur in the cited spec bytes`) {
		t.Fatalf("semantic mismatch must fail on both sides, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskSpecBindingsRejectsDigestDrift(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec.md", "Browser downloads stay owned.\n")
	content := taskSpecTestDetail("docs/spec.md:L1", "docs/spec.md:L1@sha256:"+strings.Repeat("0", 64)+"@browser+downloads")

	failures := auditTaskSpecBindings(root, "docs/tasks/TASK-0001.md", content, taskDetailInfo{
		specLinesRaw:    "docs/spec.md:L1",
		completionClaim: "Browser downloads stay owned.",
	}, true)
	if !containsFailure(failures, "digest drift") {
		t.Fatalf("changed spec bytes must invalidate the binding, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskSpecBindingsRejectsMissingDuplicateAndReorderedBindings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec.md", "Browser downloads stay owned.\nDisk quota bounds downloads.\n")
	digestOne := taskSpecTestDigest("Browser downloads stay owned.")
	digestTwo := taskSpecTestDigest("Disk quota bounds downloads.")
	info := taskDetailInfo{specLinesRaw: "docs/spec.md:L1, docs/spec.md:L2", completionClaim: "Browser downloads use owned storage and disk quota."}

	missing := taskSpecTestDetail(info.specLinesRaw, "none")
	if failures := auditTaskSpecBindings(root, "missing", missing, info, true); !containsFailure(failures, "must bind every Spec Lines ref") {
		t.Fatalf("missing bindings must fail, got:\n%s", strings.Join(failures, "\n"))
	}

	reorderedRaw := fmt.Sprintf("docs/spec.md:L2@sha256:%s@disk+downloads; docs/spec.md:L1@sha256:%s@browser+downloads", digestTwo, digestOne)
	reordered := taskSpecTestDetail(info.specLinesRaw, reorderedRaw)
	if failures := auditTaskSpecBindings(root, "reordered", reordered, info, true); !containsFailure(failures, "must match Spec Lines ref") {
		t.Fatalf("reordered bindings must fail, got:\n%s", strings.Join(failures, "\n"))
	}

	duplicateInfo := taskDetailInfo{specLinesRaw: "docs/spec.md:L1, docs/spec.md:L1", completionClaim: info.completionClaim}
	duplicateRaw := fmt.Sprintf("docs/spec.md:L1@sha256:%s@browser+downloads; docs/spec.md:L1@sha256:%s@browser+downloads", digestOne, digestOne)
	duplicate := taskSpecTestDetail(duplicateInfo.specLinesRaw, duplicateRaw)
	if failures := auditTaskSpecBindings(root, "duplicate", duplicate, duplicateInfo, true); !containsFailure(failures, "duplicate Spec Binding ref") {
		t.Fatalf("duplicate refs must fail, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskSpecBindingsRequiresExplicitNone(t *testing.T) {
	info := taskDetailInfo{specLinesRaw: "none"}
	valid := taskSpecTestDetail("none", "none")
	if failures := auditTaskSpecBindings(t.TempDir(), "valid", valid, info, true); len(failures) != 0 {
		t.Fatalf("explicit none must pass, got:\n%s", strings.Join(failures, "\n"))
	}
	missing := strings.Replace(valid, "- Spec Bindings: none\n", "", 1)
	if failures := auditTaskSpecBindings(t.TempDir(), "missing", missing, info, true); !containsFailure(failures, "must be none") {
		t.Fatalf("missing explicit none must fail, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestAuditTaskSpecBindingsRejectsDuplicateFieldAndTrailingPhantomLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/spec.md", "Browser downloads stay owned.\n")
	digest := taskSpecTestDigest("")
	info := taskDetailInfo{specLinesRaw: "docs/spec.md:L2", completionClaim: "Browser downloads stay owned."}
	phantom := taskSpecTestDetail(info.specLinesRaw, "docs/spec.md:L2@sha256:"+digest+"@browser+downloads")
	if failures := auditTaskSpecBindings(root, "phantom", phantom, info, true); !containsFailure(failures, "is outside docs/spec.md") {
		t.Fatalf("the split artifact after a trailing newline must not be bindable, got:\n%s", strings.Join(failures, "\n"))
	}

	duplicate := strings.Replace(taskSpecTestDetail("none", "none"), "- Spec Bindings: none\n", "- Spec Bindings: none\n- Spec Bindings: none\n", 1)
	if failures := auditTaskSpecBindings(root, "duplicate-field", duplicate, taskDetailInfo{specLinesRaw: "none"}, true); !containsFailure(failures, "exactly one Spec Bindings field") {
		t.Fatalf("duplicate Spec Bindings fields must fail, got:\n%s", strings.Join(failures, "\n"))
	}
}

func TestTaskSpecStemHandlesRegularAndSuffixPlurals(t *testing.T) {
	cases := map[string]string{
		"goroutines":  "goroutine",
		"diagnostics": "diagnostic",
		"processes":   "process",
		"policies":    "policy",
	}
	for input, want := range cases {
		if got := taskSpecStem(input); got != want {
			t.Errorf("taskSpecStem(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestScaffoldBootstrapTaskHasValidSpecBinding(t *testing.T) {
	root := filepath.Join("..", "repo-root-scaffold")
	contentBytes, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "TASK-0001-Bootstrap-Reconc.md"))
	if err != nil {
		t.Fatalf("read scaffold TASK: %v", err)
	}
	content := string(contentBytes)
	scheduling := taskSpecSection(content, "## Scheduling")
	info := taskDetailInfo{
		specLinesRaw:    taskSpecBulletField(scheduling, "Spec Lines"),
		completionClaim: taskSpecBulletField(scheduling, "Completion Claim"),
	}
	if failures := auditTaskSpecBindings(root, "docs/tasks/TASK-0001-Bootstrap-Reconc.md", content, info, true); len(failures) != 0 {
		t.Fatalf("scaffold TASK Spec Binding must be valid:\n%s", strings.Join(failures, "\n"))
	}
}

func taskSpecTestDetail(specLines string, bindings string) string {
	return fmt.Sprintf(`# TASK-0001-Browser-Downloads

## Why
Browser downloads need owned storage and disk quota enforcement.

## Scheduling
- Spec Lines: %s
- Spec Bindings: %s

## Technical Plan
- Keep browser downloads inside owned storage.

## Acceptance
- Browser downloads respect disk quota.

## Sub-Tasks
- [~] Enforce owned browser downloads.
`, specLines, bindings)
}

func taskSpecTestDigest(text string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
}
