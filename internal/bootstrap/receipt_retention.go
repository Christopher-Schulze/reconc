package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
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

type bootstrapRetentionRemoval struct {
	removed bool
	err     error
}

type bootstrapRetentionRemover func(path, expectedSHA string) bootstrapRetentionRemoval

type bootstrapRetentionSnapshot struct {
	path     string
	data     []byte
	mode     os.FileMode
	sha256   string
	modified time.Time
}

// pruneObsoleteBootstrapReceipts retains the current private bootstrap
// receipt plus the two newest independently validated historical pairs.
// Unknown, malformed, linked, current, or partially present state is untouched.
func pruneObsoleteBootstrapReceipts(root string, currentPlanDigest string) []string {
	return pruneObsoleteBootstrapReceiptsWithRemover(root, currentPlanDigest, removeValidatedRegularFileOutcome)
}

func pruneObsoleteBootstrapReceiptsWithRemover(
	root string,
	currentPlanDigest string,
	remover bootstrapRetentionRemover,
) []string {
	warnings := []string{}
	directory := filepath.Join(root, ".reconc")
	entries, err := boundedio.ReadDirNoSymlink(directory, maxBootstrapReceiptDirectoryEntries)
	if err != nil {
		return append(warnings, fmt.Sprintf("inspect bootstrap receipt directory %s: %v", directory, err))
	}
	if remover == nil {
		remover = removeValidatedRegularFileOutcome
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
		return warnings
	}
	for _, candidate := range candidates[maxObsoleteBootstrapReceipts:] {
		if err := removeValidatedBootstrapPair(candidate, remover); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	return warnings
}

func appendBootstrapRetentionWarnings(nextAction string, warnings []string) string {
	if len(warnings) == 0 {
		return nextAction
	}
	var builder strings.Builder
	builder.WriteString(nextAction)
	for _, warning := range warnings {
		builder.WriteString("\nWarning: ")
		builder.WriteString(warning)
	}
	return builder.String()
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

func removeValidatedRegularFileOutcome(path, expectedSHA string) bootstrapRetentionRemoval {
	record, err := captureCreatedRecord(path)
	if err != nil {
		return bootstrapRetentionRemoval{err: err}
	}
	defer record.close()
	if record.sha256 != expectedSHA {
		return bootstrapRetentionRemoval{err: fmt.Errorf("checksum changed for %s", path)}
	}
	err = removeCreatedRecord(&record)
	_, lstatErr := os.Lstat(path)
	if errors.Is(lstatErr, os.ErrNotExist) {
		return bootstrapRetentionRemoval{removed: true, err: err}
	}
	if err == nil {
		err = fmt.Errorf("target remains after removal: %s", path)
	}
	return bootstrapRetentionRemoval{err: err}
}

func captureBootstrapRetentionSnapshot(path, expectedSHA string) (bootstrapRetentionSnapshot, error) {
	record, err := captureCreatedRecord(path)
	if err != nil {
		return bootstrapRetentionSnapshot{}, err
	}
	defer record.close()
	if record.sha256 != expectedSHA {
		return bootstrapRetentionSnapshot{}, fmt.Errorf("checksum changed for %s", path)
	}
	if _, err := record.file.Seek(0, io.SeekStart); err != nil {
		return bootstrapRetentionSnapshot{}, fmt.Errorf("seek %s: %w", path, err)
	}
	data, err := io.ReadAll(io.LimitReader(record.file, maxInstallReceiptBytes+1))
	if err != nil {
		return bootstrapRetentionSnapshot{}, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := record.file.Stat()
	if err != nil {
		return bootstrapRetentionSnapshot{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if int64(len(data)) > maxInstallReceiptBytes || !sameCreatedSnapshot(record.info, info) {
		return bootstrapRetentionSnapshot{}, fmt.Errorf("file changed or exceeds %d bytes: %s", maxInstallReceiptBytes, path)
	}
	if bytesSHA256(data) != expectedSHA {
		return bootstrapRetentionSnapshot{}, fmt.Errorf("checksum changed while reading %s", path)
	}
	return bootstrapRetentionSnapshot{
		path: path, data: data, mode: info.Mode().Perm(), sha256: expectedSHA, modified: info.ModTime(),
	}, nil
}

func removeValidatedBootstrapPair(
	candidate obsoleteBootstrapReceipt,
	remover bootstrapRetentionRemover,
) error {
	if remover == nil {
		remover = removeValidatedRegularFileOutcome
	}
	receipt, err := captureBootstrapRetentionSnapshot(candidate.receiptPath, candidate.receiptSHA)
	if err != nil {
		return fmt.Errorf("bootstrap receipt retention pair %s + %s skipped before removal: receipt: %w",
			filepath.Base(candidate.receiptPath), filepath.Base(candidate.planPath), err)
	}
	plan, err := captureBootstrapRetentionSnapshot(candidate.planPath, candidate.planSHA)
	if err != nil {
		return fmt.Errorf("bootstrap receipt retention pair %s + %s skipped before removal: plan: %w",
			filepath.Base(candidate.receiptPath), filepath.Base(candidate.planPath), err)
	}

	receiptResult := remover(candidate.receiptPath, candidate.receiptSHA)
	if !receiptResult.removed || receiptResult.err != nil {
		if receiptResult.err == nil {
			receiptResult.err = errors.New("removal did not remove the receipt")
		}
		rollbackErr := error(nil)
		if receiptResult.removed {
			rollbackErr = restoreBootstrapRetentionSnapshot(receipt)
		}
		return bootstrapRetentionPairFailure(candidate, "receipt removal", receiptResult.err, rollbackErr)
	}

	planResult := remover(candidate.planPath, candidate.planSHA)
	if planResult.removed && planResult.err == nil {
		return nil
	}
	if planResult.err == nil {
		planResult.err = errors.New("removal did not remove the plan")
	}
	rollbackErr := restoreBootstrapRetentionSnapshot(receipt)
	if planResult.removed {
		rollbackErr = errors.Join(restoreBootstrapRetentionSnapshot(plan), rollbackErr)
	}
	return bootstrapRetentionPairFailure(candidate, "plan removal", planResult.err, rollbackErr)
}

func bootstrapRetentionPairFailure(
	candidate obsoleteBootstrapReceipt,
	phase string,
	primary error,
	rollbackErr error,
) error {
	message := fmt.Errorf("bootstrap receipt retention pair %s + %s failed during %s: %w",
		filepath.Base(candidate.receiptPath), filepath.Base(candidate.planPath), phase, primary)
	if rollbackErr != nil {
		return errors.Join(message, fmt.Errorf("bootstrap receipt retention rollback failed: %w", rollbackErr))
	}
	return message
}

func restoreBootstrapRetentionSnapshot(snapshot bootstrapRetentionSnapshot) (resultErr error) {
	parent, parentInfo, name, err := openCreatedParent(snapshot.path)
	if err != nil {
		return fmt.Errorf("open rollback parent for %s: %w", snapshot.path, err)
	}
	defer func() {
		closeErr := parent.Close()
		if closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close rollback parent for %s: %w", snapshot.path, closeErr))
		}
	}()
	if current, lstatErr := parent.Lstat(name); lstatErr == nil {
		if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
			return fmt.Errorf("refuse rollback after target replacement: %s", snapshot.path)
		}
		return fmt.Errorf("refuse rollback over existing target: %s", snapshot.path)
	} else if !errors.Is(lstatErr, os.ErrNotExist) {
		return fmt.Errorf("inspect rollback target %s: %w", snapshot.path, lstatErr)
	}
	if err := validateCreatedParent(parent, parentInfo, snapshot.path); err != nil {
		return fmt.Errorf("validate rollback parent for %s: %w", snapshot.path, err)
	}
	if _, err := atomicfile.WriteNew(snapshot.path, snapshot.data, snapshot.mode); err != nil {
		return fmt.Errorf("restore %s: %w", snapshot.path, err)
	}
	if err := os.Chtimes(snapshot.path, snapshot.modified, snapshot.modified); err != nil {
		return fmt.Errorf("restore timestamps for %s: %w", snapshot.path, err)
	}
	if err := validateCreatedParent(parent, parentInfo, snapshot.path); err != nil {
		return fmt.Errorf("validate rollback parent after restoring %s: %w", snapshot.path, err)
	}
	restored, err := captureCreatedRecord(snapshot.path)
	if err != nil {
		return fmt.Errorf("verify restored %s: %w", snapshot.path, err)
	}
	defer restored.close()
	if restored.sha256 != snapshot.sha256 {
		return fmt.Errorf("restored checksum differs for %s", snapshot.path)
	}
	if restored.info.Mode().Perm() != snapshot.mode.Perm() {
		return fmt.Errorf("restored mode differs for %s", snapshot.path)
	}
	return nil
}
