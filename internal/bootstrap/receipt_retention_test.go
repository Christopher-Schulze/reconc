package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

func TestPruneObsoleteBootstrapReceiptsRetainsOnlyValidatedHistory(t *testing.T) {
	repo, currentPlan, pairs := bootstrapRetentionFixture(t)
	currentPlanPath := filepath.Join(repo, filepath.FromSlash(recordedPlanPath(currentPlan)))

	malformedPath := filepath.Join(repo, ".reconc", "bootstrap-install-000000000000.json")
	if err := os.WriteFile(malformedPath, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedPath := filepath.Join(repo, ".reconc", "bootstrap-install-111111111111.json")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Base(malformedPath), linkedPath); err != nil {
			t.Fatal(err)
		}
	}

	if warnings := pruneObsoleteBootstrapReceipts(repo, currentPlan.PlanDigest); len(warnings) != 0 {
		t.Fatalf("unexpected retention warnings: %v", warnings)
	}

	assertPathExists(t, currentPlanPath)
	assertPathExists(t, filepath.Join(repo, filepath.FromSlash(installReceiptPath(currentPlan.PlanDigest))))
	assertPathMissing(t, pairs[0].planPath)
	assertPathMissing(t, pairs[0].receiptPath)
	for _, pair := range pairs[1:] {
		assertPathExists(t, pair.planPath)
		assertPathExists(t, pair.receiptPath)
	}
	assertPathExists(t, malformedPath)
	if runtime.GOOS != "windows" {
		assertPathExists(t, linkedPath)
	}
}

func TestPruneObsoleteBootstrapReceiptsReportsReceiptRemovalFailure(t *testing.T) {
	repo, currentPlan, pairs := bootstrapRetentionFixture(t)
	injected := errors.New("injected receipt removal failure")
	warnings := pruneObsoleteBootstrapReceiptsWithRemover(repo, currentPlan.PlanDigest, func(path, expectedSHA string) bootstrapRetentionRemoval {
		if path == pairs[0].receiptPath {
			return bootstrapRetentionRemoval{err: injected}
		}
		return removeValidatedRegularFileOutcome(path, expectedSHA)
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], injected.Error()) || !strings.Contains(warnings[0], "receipt removal") {
		t.Fatalf("retention warnings = %v", warnings)
	}
	assertPathExists(t, pairs[0].receiptPath)
	assertPathExists(t, pairs[0].planPath)
}

func TestPruneObsoleteBootstrapReceiptsRollsBackAfterPlanRemovalFailure(t *testing.T) {
	repo, currentPlan, pairs := bootstrapRetentionFixture(t)
	receiptBefore, err := os.ReadFile(pairs[0].receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	receiptBeforeInfo, err := os.Stat(pairs[0].receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected plan removal failure")
	warnings := pruneObsoleteBootstrapReceiptsWithRemover(repo, currentPlan.PlanDigest, func(path, expectedSHA string) bootstrapRetentionRemoval {
		if path == pairs[0].planPath {
			return bootstrapRetentionRemoval{err: injected}
		}
		return removeValidatedRegularFileOutcome(path, expectedSHA)
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], injected.Error()) || !strings.Contains(warnings[0], "plan removal") {
		t.Fatalf("retention warnings = %v", warnings)
	}
	receiptAfter, err := os.ReadFile(pairs[0].receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receiptAfter, receiptBefore) {
		t.Fatal("receipt bytes changed after retention rollback")
	}
	receiptAfterInfo, err := os.Stat(pairs[0].receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !receiptAfterInfo.ModTime().Equal(receiptBeforeInfo.ModTime()) {
		t.Fatalf("receipt modification time changed after retention rollback: before=%s after=%s", receiptBeforeInfo.ModTime(), receiptAfterInfo.ModTime())
	}
	assertPathExists(t, pairs[0].receiptPath)
	assertPathExists(t, pairs[0].planPath)
}

func TestPruneObsoleteBootstrapReceiptsPreservesConcurrentPlanMutation(t *testing.T) {
	repo, currentPlan, pairs := bootstrapRetentionFixture(t)
	replacement := []byte("user replacement\n")
	injectedMutation := false
	warnings := pruneObsoleteBootstrapReceiptsWithRemover(repo, currentPlan.PlanDigest, func(path, expectedSHA string) bootstrapRetentionRemoval {
		if path == pairs[0].receiptPath && !injectedMutation {
			injectedMutation = true
			if err := os.WriteFile(pairs[0].planPath, replacement, 0o600); err != nil {
				t.Fatalf("mutate historical plan: %v", err)
			}
		}
		return removeValidatedRegularFileOutcome(path, expectedSHA)
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "plan removal") {
		t.Fatalf("retention warnings = %v", warnings)
	}
	planAfter, err := os.ReadFile(pairs[0].planPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(planAfter, replacement) {
		t.Fatal("concurrently replaced plan was not preserved")
	}
	assertPathExists(t, pairs[0].receiptPath)
}

func TestInspectObsoleteBootstrapReceiptRejectsMalformedDigestBeforePathDerivation(t *testing.T) {
	malformedDigests := []struct {
		name   string
		digest string
	}{
		{name: "forward-slash traversal", digest: "aaaaaaaaaaaa/../../../outside-plan"},
		{name: "backslash traversal", digest: `aaaaaaaaaaaa\..\..\outside`},
		{name: "drive prefix", digest: `C:\outside\bootstrap-plan`},
		{name: "uppercase hex", digest: strings.Repeat("A", 64)},
		{name: "mixed-case hex", digest: strings.Repeat("a", 63) + "F"},
		{name: "short", digest: strings.Repeat("a", 63)},
		{name: "long", digest: strings.Repeat("a", 65)},
	}
	for _, test := range malformedDigests {
		t.Run(test.name, func(t *testing.T) {
			sandbox := t.TempDir()
			repo := filepath.Join(sandbox, "repo")
			if err := os.Mkdir(repo, 0o700); err != nil {
				t.Fatal(err)
			}
			controlDirectory := filepath.Join(repo, ".reconc")
			if err := os.Mkdir(controlDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(controlDirectory, "bootstrap-install-aaaaaaaaaaaa.json")
			receiptBody, err := json.Marshal(InstallReceipt{PlanDigest: test.digest})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receiptPath, receiptBody, 0o600); err != nil {
				t.Fatal(err)
			}
			outsidePath := filepath.Join(sandbox, "outside-plan.json")
			outsideBody := []byte("must remain untouched\n")
			if err := os.WriteFile(outsidePath, outsideBody, 0o600); err != nil {
				t.Fatal(err)
			}

			observedReceiptPaths := []string{}
			observedPlanPaths := []string{}
			_, ok := inspectObsoleteBootstrapReceiptWithHooks(repo, receiptPath, strings.Repeat("f", 64), bootstrapReceiptInspectionHooks{
				readReceipt: func(path string, maximum int64) ([]byte, os.FileInfo, error) {
					observedReceiptPaths = append(observedReceiptPaths, path)
					return boundedio.ReadRegularFileSnapshot(path, maximum)
				},
				loadPlan: func(path string) (*Plan, error) {
					observedPlanPaths = append(observedPlanPaths, path)
					return nil, errors.New("unexpected plan read")
				},
			})
			if ok {
				t.Fatal("malformed receipt was accepted")
			}
			if len(observedReceiptPaths) != 1 || observedReceiptPaths[0] != receiptPath {
				t.Fatalf("receipt reads = %v, want only %s", observedReceiptPaths, receiptPath)
			}
			if len(observedPlanPaths) != 0 {
				t.Fatalf("plan reads = %v, want none", observedPlanPaths)
			}
			assertPathExists(t, receiptPath)
			actualOutsideBody, err := os.ReadFile(outsidePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actualOutsideBody, outsideBody) {
				t.Fatal("outside pair member changed")
			}
			removalCalled := false
			warnings := pruneObsoleteBootstrapReceiptsWithRemover(repo, strings.Repeat("f", 64), func(string, string) bootstrapRetentionRemoval {
				removalCalled = true
				return bootstrapRetentionRemoval{removed: true}
			})
			if len(warnings) != 0 || removalCalled {
				t.Fatalf("malformed retention candidate triggered cleanup: warnings=%v removal=%t", warnings, removalCalled)
			}
			assertPathExists(t, receiptPath)
			assertPathExists(t, outsidePath)
		})
	}
}

func TestInspectObsoleteBootstrapReceiptRejectsOutsideReceiptBeforeRead(t *testing.T) {
	repo := t.TempDir()
	receiptPath := filepath.Join(t.TempDir(), "bootstrap-install-aaaaaaaaaaaa.json")
	readCalled := false
	_, ok := inspectObsoleteBootstrapReceiptWithHooks(repo, receiptPath, strings.Repeat("f", 64), bootstrapReceiptInspectionHooks{
		readReceipt: func(string, int64) ([]byte, os.FileInfo, error) {
			readCalled = true
			return nil, nil, errors.New("unexpected receipt read")
		},
		loadPlan: LoadPlan,
	})
	if ok || readCalled {
		t.Fatalf("outside receipt accepted or read: accepted=%t read=%t", ok, readCalled)
	}
}

func TestInspectObsoleteBootstrapReceiptUsesSafePathsForValidPair(t *testing.T) {
	repo, currentPlan, pairs := bootstrapRetentionFixture(t)
	observedReceiptPaths := []string{}
	observedPlanPaths := []string{}
	validated, ok := inspectObsoleteBootstrapReceiptWithHooks(repo, pairs[0].receiptPath, currentPlan.PlanDigest, bootstrapReceiptInspectionHooks{
		readReceipt: func(path string, maximum int64) ([]byte, os.FileInfo, error) {
			observedReceiptPaths = append(observedReceiptPaths, path)
			return boundedio.ReadRegularFileSnapshot(path, maximum)
		},
		loadPlan: func(path string) (*Plan, error) {
			observedPlanPaths = append(observedPlanPaths, path)
			return LoadPlan(path)
		},
	})
	if !ok {
		t.Fatal("valid historical pair was rejected")
	}
	if len(observedReceiptPaths) != 1 || observedReceiptPaths[0] != pairs[0].receiptPath {
		t.Fatalf("receipt reads = %v, want %s", observedReceiptPaths, pairs[0].receiptPath)
	}
	if len(observedPlanPaths) != 1 || observedPlanPaths[0] != pairs[0].planPath {
		t.Fatalf("plan reads = %v, want %s", observedPlanPaths, pairs[0].planPath)
	}
	if validated.receiptPath != pairs[0].receiptPath || validated.planPath != pairs[0].planPath {
		t.Fatalf("validated paths = %s + %s", validated.receiptPath, validated.planPath)
	}
}

type bootstrapRetentionPairPaths struct {
	planPath    string
	receiptPath string
}

func bootstrapRetentionFixture(t *testing.T) (string, *Plan, []bootstrapRetentionPairPaths) {
	t.Helper()
	repo, repositoryReceipt := initializeSyncFixture(t, ProfileMinimal)
	currentPlanPath := filepath.Join(repo, filepath.FromSlash(".reconc/bootstrap-plan-"+repositoryReceipt.PlanDigest+".json"))
	currentPlan, err := LoadPlan(currentPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	repo = currentPlan.RepoRoot
	currentReceipt, _, err := loadInstallReceipt(currentPlan)
	if err != nil {
		t.Fatal(err)
	}
	pairs := make([]bootstrapRetentionPairPaths, 0, 3)
	baseTime := time.Unix(1_700_000_000, 0)
	for index := range 3 {
		plan := cloneBootstrapPlan(t, currentPlan)
		plan.ProductVersion = fmt.Sprintf("0.8.%d", index)
		plan.PlanDigest = ""
		plan.PlanDigest, err = computePlanDigest(plan)
		if err != nil {
			t.Fatal(err)
		}
		planBody, err := encodePlan(plan)
		if err != nil {
			t.Fatal(err)
		}
		planPath := filepath.Join(repo, filepath.FromSlash(recordedPlanPath(plan)))
		if err := os.WriteFile(planPath, planBody, 0o600); err != nil {
			t.Fatal(err)
		}
		receipt := cloneInstallReceipt(t, currentReceipt)
		receipt.ProductVersion = plan.ProductVersion
		receipt.PlanDigest = plan.PlanDigest
		oldRecordedPath := recordedPlanPath(currentPlan)
		for entryIndex := range receipt.Entries {
			if receipt.Entries[entryIndex].Path == oldRecordedPath {
				receipt.Entries[entryIndex].Path = recordedPlanPath(plan)
				receipt.Entries[entryIndex].SHA256 = bytesSHA256(planBody)
			}
		}
		sort.Slice(receipt.Entries, func(left, right int) bool {
			return receipt.Entries[left].Path < receipt.Entries[right].Path
		})
		receipt.Digest = ""
		receipt.Digest, err = computeInstallReceiptDigest(receipt)
		if err != nil {
			t.Fatal(err)
		}
		receiptBody, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		receiptPath := filepath.Join(repo, filepath.FromSlash(installReceiptPath(plan.PlanDigest)))
		if err := os.WriteFile(receiptPath, append(receiptBody, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		modified := baseTime.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(receiptPath, modified, modified); err != nil {
			t.Fatal(err)
		}
		loadedPlan, loadErr := LoadPlan(planPath)
		if loadErr != nil {
			t.Fatalf("load historical plan %d: %v", index, loadErr)
		}
		if validateErr := validateInstallReceipt(loadedPlan, receipt); validateErr != nil {
			t.Fatalf("validate historical receipt %d: %v", index, validateErr)
		}
		if _, ok := inspectObsoleteBootstrapReceipt(repo, receiptPath, currentPlan.PlanDigest); !ok {
			t.Fatalf("historical receipt %d was not recognized as a validated obsolete pair", index)
		}
		pairs = append(pairs, bootstrapRetentionPairPaths{planPath: planPath, receiptPath: receiptPath})
	}
	return repo, currentPlan, pairs
}

func cloneInstallReceipt(t *testing.T, receipt *InstallReceipt) *InstallReceipt {
	t.Helper()
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var clone InstallReceipt
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, got %v", path, err)
	}
}
