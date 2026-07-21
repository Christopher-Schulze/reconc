package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/hooks"
)

type removalMutation struct {
	relative string
	path     string
	before   []byte
	after    []byte
	mode     os.FileMode
	remove   bool
}

type removalCandidate struct {
	relative string
	content  []byte
	mode     uint32
}

// Remove reverses one applied bootstrap plan using its tamper-evident install
// receipt. It never infers ownership from a filename alone.
func Remove(plan *Plan) (*RemovalReport, error) {
	report := &RemovalReport{
		FormatVersion: RemovalFormatVersion, Status: RemovalRolledBack,
		Removed: []string{}, Updated: []string{}, Preserved: []string{},
		Candidates: []string{}, RolledBack: []string{},
	}
	if err := ValidatePlan(plan); err != nil {
		return report, err
	}
	report.RepoRoot = plan.RepoRoot
	report.PlanDigest = plan.PlanDigest
	receipt, receiptRelative, err := loadInstallReceipt(plan)
	report.ReceiptPath = receiptRelative
	if err != nil {
		return report, err
	}

	mutations := []removalMutation{}
	candidates := []removalCandidate{}
	for _, entry := range receipt.Entries {
		mutation, candidate, preserved, inspectErr := planReceiptEntryRemoval(plan.RepoRoot, entry)
		if inspectErr != nil {
			report.Preserved = append(report.Preserved, entry.Path+": "+inspectErr.Error())
			continue
		}
		if preserved != "" {
			report.Preserved = append(report.Preserved, entry.Path+": "+preserved)
		}
		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
		if candidate != nil {
			candidates = append(candidates, *candidate)
		}
	}
	if len(report.Preserved) > 0 || len(candidates) > 0 {
		created, dirs, candidateErr := materializeRemovalCandidates(plan, candidates)
		defer closeCreatedDirectoryIdentities(dirs)
		if candidateErr != nil {
			rolledBack, rollbackErr := rollbackCreated(plan.RepoRoot, created, dirs)
			report.RolledBack = rolledBack
			return report, joinApplyRollbackError(candidateErr, rollbackErr)
		}
		for _, candidate := range candidates {
			report.Candidates = append(report.Candidates, candidate.relative)
		}
		report.Status = RemovalDrift
		report.NextAction = "Review each removal candidate and preserved drift item; reconcile user-owned changes, then rerun bootstrap remove with the same plan."
		sort.Strings(report.Candidates)
		sort.Strings(report.Preserved)
		return report, nil
	}

	receiptPath, err := safeBootstrapTarget(plan.RepoRoot, receiptRelative)
	if err != nil {
		return report, err
	}
	receiptBody, receiptMode, err := readRemovalFile(receiptPath, maxInstallReceiptBytes)
	if err != nil {
		return report, fmt.Errorf("read bootstrap install receipt before removal: %w", err)
	}
	mutations = append(mutations, removalMutation{
		relative: receiptRelative, path: receiptPath, before: receiptBody,
		mode: receiptMode, remove: true,
	})
	removed, updated, rolledBack, err := applyRemovalTransaction(mutations)
	report.Removed = removed
	report.Updated = updated
	report.RolledBack = rolledBack
	if err != nil {
		return report, err
	}
	// The private receipt is lifecycle state, not a product artifact in the
	// public removal summary.
	report.Removed = removeString(report.Removed, receiptRelative)
	report.Status = RemovalComplete
	report.NextAction = "Bootstrap-owned files and managed blocks were removed. Review the repository diff, then remove any intentionally preserved shared hook wrapper manually only after all hook platforms are gone."
	sort.Strings(report.Removed)
	sort.Strings(report.Updated)
	return report, nil
}

func planReceiptEntryRemoval(root string, entry InstallReceiptEntry) (*removalMutation, *removalCandidate, string, error) {
	target, err := safeBootstrapTarget(root, entry.Path)
	if err != nil {
		return nil, nil, "", err
	}
	body, mode, err := readRemovalFile(target, maxBinaryBytes)
	if os.IsNotExist(err) {
		return nil, nil, "already absent", nil
	}
	if err != nil {
		return nil, nil, "", err
	}
	digest := bytesSHA256(body)
	exact := digest == entry.SHA256 && modeSatisfies(mode, entry.Mode)
	if exact {
		switch entry.Ownership {
		case "file":
			return &removalMutation{relative: entry.Path, path: target, before: body, mode: mode, remove: true}, nil, "", nil
		case "managed-block":
			stripped, found, stripErr := stripReceiptManagedBlock(string(body), entry.BlockStart, entry.BlockEnd)
			if stripErr != nil {
				return nil, nil, "", stripErr
			}
			if !found {
				return nil, nil, "managed block is missing", nil
			}
			return &removalMutation{relative: entry.Path, path: target, before: body, after: []byte(stripped), mode: mode}, nil, "", nil
		}
	}
	if entry.BlockStart == "" {
		return nil, nil, "content or mode drifted; receipt ownership is no longer exact", nil
	}
	stripped, found, stripErr := stripReceiptManagedBlock(string(body), entry.BlockStart, entry.BlockEnd)
	if stripErr != nil {
		return nil, nil, "", stripErr
	}
	if !found {
		return nil, nil, "managed block drifted or is missing; current file preserved", nil
	}
	candidateBody := []byte(stripped)
	candidatePath := entry.Path + ".reconc-remove-candidate-" + bytesSHA256(candidateBody)[:12]
	return nil, &removalCandidate{relative: candidatePath, content: candidateBody, mode: entry.Mode}, "drifted managed file preserved", nil
}

func stripReceiptManagedBlock(content, start, end string) (string, bool, error) {
	if start == hooks.CodexActivationBlockStart && end == hooks.CodexActivationBlockEnd {
		return hooks.RemoveCodexActivation(content)
	}
	if start == "" || end == "" {
		return content, false, nil
	}
	if strings.Count(content, start) > 1 || strings.Count(content, end) > 1 {
		return "", false, fmt.Errorf("duplicate managed block markers")
	}
	startIndex := strings.Index(content, start)
	endIndex := strings.Index(content, end)
	if startIndex == -1 && endIndex == -1 {
		return content, false, nil
	}
	if startIndex == -1 || endIndex == -1 || endIndex < startIndex {
		return "", false, fmt.Errorf("incomplete managed block markers")
	}
	endIndex += len(end)
	if endIndex < len(content) && content[endIndex] == '\n' {
		endIndex++
	}
	return content[:startIndex] + content[endIndex:], true, nil
}

func materializeRemovalCandidates(plan *Plan, candidates []removalCandidate) ([]createdRecord, []createdDirectory, error) {
	created := []createdRecord{}
	dirs := []createdDirectory{}
	for _, candidate := range candidates {
		target, err := safeBootstrapTarget(plan.RepoRoot, candidate.relative)
		if err != nil {
			return created, dirs, err
		}
		digest := bytesSHA256(candidate.content)
		if info, err := os.Lstat(target); err == nil {
			if !info.Mode().IsRegular() || !modeSatisfies(info.Mode(), candidate.mode) {
				return created, dirs, fmt.Errorf("removal candidate exists with incompatible type or mode: %s", candidate.relative)
			}
			current, hashErr := fileSHA256(target)
			if hashErr != nil || current != digest {
				return created, dirs, fmt.Errorf("removal candidate exists with different content: %s", candidate.relative)
			}
			continue
		} else if !os.IsNotExist(err) {
			return created, dirs, fmt.Errorf("inspect removal candidate %s: %w", candidate.relative, err)
		}
		artifact := desiredArtifact{component: "removal-candidate", path: candidate.relative, mode: candidate.mode, content: candidate.content}
		record, createdDirs, err := publishArtifact(plan.RepoRoot, artifact, candidate.relative, digest, plan.PlanDigest)
		dirs = appendUniqueDirectories(dirs, createdDirs...)
		if record.path != "" {
			created = append(created, record)
		}
		if err != nil {
			return created, dirs, err
		}
	}
	return created, dirs, nil
}

func readRemovalFile(path string, limit int64) ([]byte, os.FileMode, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || pathInfo.Size() > limit {
		return nil, 0, fmt.Errorf("%s is not a bounded real regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if !os.SameFile(pathInfo, openedInfo) {
		_ = file.Close()
		return nil, 0, fmt.Errorf("%s changed while opening", path)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if len(body) > int(limit) {
		return nil, 0, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, 0, err
	}
	return body, pathInfo.Mode().Perm(), nil
}

func applyRemovalTransaction(mutations []removalMutation) ([]string, []string, []string, error) {
	for _, mutation := range mutations {
		current, _, err := readRemovalFile(mutation.path, maxBinaryBytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("revalidate removal target %s: %w", mutation.relative, err)
		}
		if !bytes.Equal(current, mutation.before) {
			return nil, nil, nil, fmt.Errorf("removal target changed after preflight: %s", mutation.relative)
		}
	}
	applied := []removalMutation{}
	removed := []string{}
	updated := []string{}
	for _, mutation := range mutations {
		var err error
		if mutation.remove {
			err = os.Remove(mutation.path)
			if err == nil {
				err = syncRemovalParent(mutation.path)
			}
		} else {
			_, err = atomicfile.WriteIfChanged(mutation.path, mutation.after, mutation.mode)
		}
		if err == nil {
			applied = append(applied, mutation)
			if mutation.remove {
				removed = append(removed, mutation.relative)
			} else {
				updated = append(updated, mutation.relative)
			}
			continue
		}
		rolledBack, rollbackErr := rollbackRemovalMutations(applied)
		return removed, updated, rolledBack, fmt.Errorf("apply removal mutation %s: %w", mutation.relative, errors.Join(err, rollbackErr))
	}
	return removed, updated, nil, nil
}

func rollbackRemovalMutations(applied []removalMutation) ([]string, error) {
	rolledBack := []string{}
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		mutation := applied[index]
		if mutation.remove {
			if _, err := os.Lstat(mutation.path); err == nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("refuse to overwrite path that appeared during removal rollback: %s", mutation.relative))
				continue
			} else if !os.IsNotExist(err) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("inspect removal rollback target %s: %w", mutation.relative, err))
				continue
			}
		} else {
			current, _, err := readRemovalFile(mutation.path, maxBinaryBytes)
			if err != nil || !bytes.Equal(current, mutation.after) {
				if err == nil {
					err = fmt.Errorf("content changed after removal mutation")
				}
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("refuse to overwrite removal rollback target %s: %w", mutation.relative, err))
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(mutation.path), 0o755); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if _, err := atomicfile.WriteIfChanged(mutation.path, mutation.before, mutation.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		rolledBack = append(rolledBack, mutation.relative)
	}
	sort.Strings(rolledBack)
	return rolledBack, rollbackErr
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
