package agentsession

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/retention"
)

const maxEvidenceResolutionBytes = 32 * 1024

func withEvidenceTaintLock(root string, fn func() error) error {
	deadline := time.NewTimer(boundedWait(agentSessionLockTimeout))
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		release, contended, err := tryEvidenceTaintLock(root)
		if err != nil {
			return err
		}
		if !contended {
			return errors.Join(fn(), release())
		}
		select {
		case <-deadline.C:
			return &filelock.AcquireError{Kind: filelock.ErrLockTimeout, Timeout: agentSessionLockTimeout}
		case <-ticker.C:
		}
	}
}

func tryEvidenceTaintLock(root string) (func() error, bool, error) {
	var lock *os.File
	var unlock func() error
	contended := false
	// Establish the permanent project lease before global retention can scan
	// its lock directory. Release the global lock before session publication,
	// which may itself create directories under that global boundary.
	err := withProjectStatePublicationLock(filepath.Clean(stateRoot()), func() error {
		path := filepath.Join(projectDir(root), "locks", retention.EvidenceTaintLockName)
		if err := repairPrivateStateDir(filepath.Dir(path)); err != nil {
			return err
		}
		var err error
		lock, err = privatefs.OpenLock(path)
		if err != nil {
			return err
		}
		unlock, err = filelock.TryLock(lock)
		if err != nil {
			if filelock.IsContended(err) {
				contended = true
				return lock.Close()
			}
			return errors.Join(err, lock.Close())
		}
		return nil
	})
	if err != nil {
		if unlock != nil {
			return nil, false, errors.Join(err, unlock(), lock.Close())
		}
		return nil, false, err
	}
	if contended {
		return nil, true, nil
	}
	return func() error { return errors.Join(unlock(), lock.Close()) }, false, nil
}

func resolveEvidenceTaintWithLeases(root, active, token, reason string) (EvidenceTaintStatus, error) {
	origin := active
	taint, err := loadEvidenceTaint(root)
	if err != nil {
		return EvidenceTaintStatus{}, err
	}
	if taint != nil {
		origin = taint.SessionID
	}
	ids := []string{}
	for _, id := range []string{active, origin} {
		if id != "" && !containsString(ids, id) {
			if err := validateSessionID(id); err != nil {
				return EvidenceTaintStatus{}, err
			}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var result EvidenceTaintStatus
	err = withEvidenceSessionLeases(root, ids, func() error {
		return withEvidenceTaintLock(root, func() error {
			var resolveErr error
			result, resolveErr = resolveEvidenceTaintLocked(root, active, token, reason)
			return resolveErr
		})
	})
	return result, err
}

func withEvidenceSessionLeases(root string, ids []string, fn func() error) error {
	if len(ids) == 0 {
		return fn()
	}
	return withSessionLock(root, ids[0], func() error {
		return withEvidenceSessionLeases(root, ids[1:], fn)
	})
}

func evidenceWindowNeedsReset(taint evidenceTaint) bool {
	return taint.Field == "command_texts" || taint.Field == "evidence_segments" ||
		taint.Limit == "segment_count" || taint.Limit == "segment_storage" || taint.Limit == "aggregate_bytes"
}

func readEvidenceResolution(root, token string) (evidenceTaintResolution, bool, error) {
	var resolution evidenceTaintResolution
	if !evidenceHexIdentity(token, 64) {
		return resolution, false, errors.New("invalid evidence resolution token")
	}
	body, err := boundedio.ReadRegularFile(evidenceTaintResolutionPath(root, token), maxEvidenceResolutionBytes)
	if errors.Is(err, os.ErrNotExist) {
		return resolution, false, nil
	}
	if err != nil {
		return resolution, false, err
	}
	if err := json.Unmarshal(body, &resolution); err != nil {
		return resolution, false, fmt.Errorf("decode evidence resolution: %w", err)
	}
	expected, err := evidenceTaintToken(resolution.Taint)
	if err != nil {
		return resolution, false, err
	}
	if resolution.FormatVersion != "evidence-taint-resolution-v1" || resolution.Token != token || expected != token ||
		resolution.Taint.FormatVersion != evidenceTaintFormatVersion || resolution.Taint.SegmentCount > maxEvidenceSegments ||
		resolution.Taint.RepoRoot != root || strings.TrimSpace(resolution.Taint.Field) == "" ||
		strings.TrimSpace(resolution.Reason) == "" || strings.TrimSpace(resolution.Reason) != resolution.Reason || len(resolution.Reason) > 512 {
		return resolution, false, errors.New("evidence resolution identity is invalid")
	}
	if err := validateSessionID(resolution.Taint.SessionID); err != nil {
		return resolution, false, err
	}
	return resolution, true, nil
}

func publishEvidenceResolution(path string, resolution evidenceTaintResolution, body []byte) error {
	previous, present, err := readEvidenceResolution(resolution.Taint.RepoRoot, resolution.Token)
	if err != nil {
		return err
	}
	if present {
		if previous.Taint != resolution.Taint {
			return errors.New("existing evidence resolution describes a different taint")
		}
		return nil
	}
	_, err = atomicfile.WritePrivateNew(path, body, 0o600)
	return err
}

func evidenceTaintWasResolved(state SessionState) (bool, error) {
	token := state.EvidenceTaintToken
	if token == "" {
		return false, nil
	}
	resolution, present, err := readEvidenceResolution(state.RepoRoot, token)
	if err != nil || !present {
		return false, err
	}
	return resolution.Taint.Field == state.EvidenceOverflowReason && resolution.Taint.Limit == state.EvidenceOverflowLimit, nil
}

func clearResolvedOriginOverflow(root string, taint evidenceTaint, setAside *[]string) error {
	state, err := readRecoverableSessionState(root, taint.SessionID, setAside)
	if err != nil {
		return err
	}
	if !state.EvidenceOverflow || state.EvidenceOverflowReason != taint.Field || state.EvidenceOverflowLimit != taint.Limit {
		return nil
	}
	state.EvidenceOverflow = false
	state.EvidenceOverflowReason = ""
	state.EvidenceOverflowLimit = ""
	state.EvidenceTaintToken = ""
	return saveSessionStateLocked(state)
}

func evidenceHexIdentity(value string, length int) bool {
	if len(value) != length {
		return false
	}
	body, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(body) == value
}
