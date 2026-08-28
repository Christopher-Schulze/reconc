package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshnessFileReplacementBeforeOpenUsesOneCoherentSnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.yml")
	replacement := filepath.Join(directory, "replacement.yml")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := []byte("replacement\n")
	if err := os.WriteFile(replacement, body, 0o640); err != nil {
		t.Fatal(err)
	}
	wantInfo, err := os.Lstat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	original := withFreshnessFileSnapshot
	withFreshnessFileSnapshot = func(observedPath string, maximum int64, use func(*os.File, os.FileInfo) error) error {
		if err := os.Rename(replacement, observedPath); err != nil {
			return err
		}
		return original(observedPath, maximum, use)
	}
	t.Cleanup(func() { withFreshnessFileSnapshot = original })

	var total int64
	observation, err := observeFreshnessFile(path, &total, make([]byte, 1024))
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256(body)
	if !observation.Exists || observation.Size != wantInfo.Size() ||
		observation.Mode != uint32(wantInfo.Mode()) || observation.ModTime != wantInfo.ModTime().UnixNano() ||
		observation.Identity != freshnessIdentity(wantInfo) || observation.Digest != hex.EncodeToString(wantDigest[:]) ||
		total != wantInfo.Size() {
		t.Fatalf("replacement freshness observation is mixed: %#v total=%d", observation, total)
	}
}

func TestFreshnessFileReplacementAfterOpenFailsClosed(t *testing.T) {
	for _, phase := range []string{"before-read", "after-read"} {
		t.Run(phase, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "source.yml")
			replacement := filepath.Join(directory, "replacement.yml")
			if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(replacement, []byte("new\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			original := withFreshnessFileSnapshot
			var replacementErr error
			withFreshnessFileSnapshot = func(observedPath string, maximum int64, use func(*os.File, os.FileInfo) error) error {
				return original(observedPath, maximum, func(file *os.File, opened os.FileInfo) error {
					if phase == "before-read" {
						replacementErr = os.Rename(replacement, observedPath)
						if replacementErr != nil {
							return replacementErr
						}
					}
					if err := use(file, opened); err != nil {
						return err
					}
					if phase == "after-read" {
						replacementErr = os.Rename(replacement, observedPath)
						return replacementErr
					}
					return nil
				})
			}
			t.Cleanup(func() { withFreshnessFileSnapshot = original })

			total := int64(7)
			_, err := observeFreshnessFile(path, &total, make([]byte, 1024))
			if replacementErr != nil {
				t.Skipf("open-file replacement unavailable: %v", replacementErr)
			}
			if err == nil || !strings.Contains(err.Error(), "changed") {
				t.Fatalf("%s replacement was accepted: %v", phase, err)
			}
			if total != 7 {
				t.Fatalf("failed %s observation charged %d aggregate bytes", phase, total)
			}
		})
	}
}

func TestFreshnessFileBudgetUsesOpenedSizeAndCommitsAfterValidation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.yml")
	replacement := filepath.Join(directory, "replacement.yml")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("bb"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := withFreshnessFileSnapshot
	withFreshnessFileSnapshot = func(observedPath string, maximum int64, use func(*os.File, os.FileInfo) error) error {
		if err := os.Rename(replacement, observedPath); err != nil {
			return err
		}
		return original(observedPath, maximum, use)
	}
	t.Cleanup(func() { withFreshnessFileSnapshot = original })

	total := int64(maxFreshnessTotalBytes - 1)
	if _, err := observeFreshnessFile(path, &total, make([]byte, 1024)); err == nil ||
		!strings.Contains(err.Error(), "bounded byte budget") {
		t.Fatalf("replacement size bypassed aggregate budget: %v", err)
	}
	if total != maxFreshnessTotalBytes-1 {
		t.Fatalf("failed budget admission charged aggregate bytes: %d", total)
	}

	withFreshnessFileSnapshot = original
	exact := filepath.Join(directory, "exact.yml")
	if err := os.WriteFile(exact, []byte("cc"), 0o600); err != nil {
		t.Fatal(err)
	}
	total = maxFreshnessTotalBytes - 2
	if _, err := observeFreshnessFile(exact, &total, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	if total != maxFreshnessTotalBytes {
		t.Fatalf("exact aggregate boundary = %d, want %d", total, maxFreshnessTotalBytes)
	}
}

func TestRuntimePlanFreshnessDetectsSameSizeChangeWithRestoredMetadata(t *testing.T) {
	withRECONCHome(t)
	original := "rules:\n  - id: abc\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: one\n"
	updated := strings.Replace(original, "message: one", "message: two", 1)
	if len(original) != len(updated) {
		t.Fatal("restored-metadata fixture changed size")
	}
	repo := makeRepo(t, "# project\n", "", original)
	evaluator := NewEvaluator()
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	before, err := os.Lstat(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte(updated), before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(policyPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("fixture metadata was not restored: before=%v after=%v", before, after)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !freshnessInvalidationError(err) {
		t.Fatalf("same-size restored-metadata mutation was not detected: %v", err)
	}
}
