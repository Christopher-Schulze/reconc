package bootstrap

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	maxBootstrapReceiptDirectoryEntries = 4096
	maxObsoleteBootstrapReceipts        = 2
)

type obsoleteBootstrapReceipt struct {
	receiptPath string
	receiptSHA  string
	planPath    string
	planSHA     string
	modified    time.Time
}

type validatedObsoleteBootstrapReceipt struct {
	plan       *Plan
	info       os.FileInfo
	receiptSHA string
	planSHA    string
}

// pruneObsoleteBootstrapReceipts retains the current private bootstrap
// receipt plus the two newest independently validated historical pairs.
// Unknown, malformed, linked, current, or partially present state is untouched.
func pruneObsoleteBootstrapReceipts(root string, currentPlanDigest string) {
	directory := filepath.Join(root, ".reconc")
	entries, err := boundedio.ReadDirNoSymlink(directory, maxBootstrapReceiptDirectoryEntries)
	if err != nil {
		return
	}
	candidates := make([]obsoleteBootstrapReceipt, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.HasPrefix(name, "bootstrap-install-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		receiptPath := filepath.Join(directory, name)
		validated, ok := inspectObsoleteBootstrapReceipt(root, receiptPath, currentPlanDigest)
		if !ok {
			continue
		}
		candidates = append(candidates, obsoleteBootstrapReceipt{
			receiptPath: receiptPath,
			receiptSHA:  validated.receiptSHA,
			planPath:    filepath.Join(root, filepath.FromSlash(recordedPlanPath(validated.plan))),
			planSHA:     validated.planSHA,
			modified:    validated.info.ModTime(),
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].modified.Equal(candidates[right].modified) {
			return candidates[left].receiptPath > candidates[right].receiptPath
		}
		return candidates[left].modified.After(candidates[right].modified)
	})
	if len(candidates) <= maxObsoleteBootstrapReceipts {
		return
	}
	for _, candidate := range candidates[maxObsoleteBootstrapReceipts:] {
		if removeValidatedRegularFile(candidate.receiptPath, candidate.receiptSHA) {
			_ = removeValidatedRegularFile(candidate.planPath, candidate.planSHA)
		}
	}
}

func inspectObsoleteBootstrapReceipt(root, receiptPath, currentPlanDigest string) (*validatedObsoleteBootstrapReceipt, bool) {
	body, info, err := boundedio.ReadRegularFileSnapshot(receiptPath, maxInstallReceiptBytes)
	if err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	var receipt InstallReceipt
	if decoder.Decode(&receipt) != nil {
		return nil, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || receipt.PlanDigest == currentPlanDigest || len(receipt.PlanDigest) < 12 ||
		filepath.Base(receiptPath) != filepath.Base(installReceiptPath(receipt.PlanDigest)) {
		return nil, false
	}
	planPath := filepath.Join(root, filepath.FromSlash(".reconc/bootstrap-plan-"+receipt.PlanDigest+".json"))
	plan, err := LoadPlan(planPath)
	if err != nil || plan.RepoRoot != root || validateInstallReceipt(plan, &receipt) != nil {
		return nil, false
	}
	planSHA := ""
	for _, entry := range receipt.Entries {
		if entry.Path == recordedPlanPath(plan) && entry.Ownership == "file" && entry.Mode == 0o600 {
			planSHA = entry.SHA256
			break
		}
	}
	if !validSHA256(planSHA) {
		return nil, false
	}
	return &validatedObsoleteBootstrapReceipt{
		plan: plan, info: info, receiptSHA: bytesSHA256(body), planSHA: planSHA,
	}, true
}

func removeValidatedRegularFile(path, expectedSHA string) bool {
	record, err := captureCreatedRecord(path)
	if err != nil {
		return false
	}
	defer record.close()
	if record.sha256 != expectedSHA {
		return false
	}
	return removeCreatedRecord(&record) == nil
}
