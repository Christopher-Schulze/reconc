package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

func TestPruneObsoleteBootstrapReceiptsRetainsOnlyValidatedHistory(t *testing.T) {
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

	type historicalPair struct {
		planPath    string
		receiptPath string
	}
	pairs := make([]historicalPair, 0, 3)
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
		pairs = append(pairs, historicalPair{planPath: planPath, receiptPath: receiptPath})
	}

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

	pruneObsoleteBootstrapReceipts(repo, currentPlan.PlanDigest)

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
