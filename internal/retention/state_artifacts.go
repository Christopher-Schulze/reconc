package retention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	maxPreDecisionArtifactBytes     = 16 * 1024
	maxTaintResolutionArtifactBytes = 32 * 1024
	evidenceTaintVersion            = "evidence-taint-v1"
	evidenceTaintResolutionVersion  = "evidence-taint-resolution-v1"
)

type retainedEvidenceTaint struct {
	FormatVersion string `json:"format_version"`
	RepoRoot      string `json:"repo_root"`
	SessionID     string `json:"session_id"`
	Field         string `json:"field"`
	Limit         string `json:"limit"`
	SegmentCount  uint64 `json:"segment_count,omitempty"`
	SegmentDigest string `json:"segment_digest,omitempty"`
}

type retainedTaintResolution struct {
	FormatVersion string                `json:"format_version"`
	Token         string                `json:"token"`
	Reason        string                `json:"reason"`
	Taint         retainedEvidenceTaint `json:"taint"`
}

type taintResolutionProtection struct {
	names         map[string]bool
	all           bool
	evidenceNames map[string]bool
	evidenceAll   bool
}

func inspectPreDecisionArtifact(_ string, name string, info os.FileInfo) error {
	if !validSessionArtifactName(name) {
		return errors.New("filename is not an owned session artifact")
	}
	if info.Size() > maxPreDecisionArtifactBytes {
		return fmt.Errorf("file exceeds %d bytes", maxPreDecisionArtifactBytes)
	}
	return nil
}

func inspectTaintResolutionArtifact(repoRoot string) stateArtifactInspector {
	return func(path, name string, info os.FileInfo) error {
		stem, ok := lowercaseDigestFileStem(name)
		if !ok {
			return errors.New("filename is not an owned taint-resolution token")
		}
		body, identity, err := boundedio.ReadRegularFileSnapshot(path, maxTaintResolutionArtifactBytes)
		if err != nil {
			return err
		}
		if !sameArtifactSnapshot(info, identity) {
			return errors.New("identity changed during taint-resolution inspection")
		}
		var resolution retainedTaintResolution
		if err := json.Unmarshal(body, &resolution); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if resolution.FormatVersion != evidenceTaintResolutionVersion || resolution.Token != stem {
			return errors.New("taint-resolution identity mismatch")
		}
		if reason := strings.TrimSpace(resolution.Reason); reason == "" || reason != resolution.Reason || len(reason) > 512 {
			return errors.New("taint-resolution reason is invalid")
		}
		token, err := retainedEvidenceTaintToken(resolution.Taint, repoRoot)
		if err != nil {
			return err
		}
		if token != resolution.Token {
			return errors.New("taint-resolution token does not match its evidence taint")
		}
		return nil
	}
}

func resolveTaintResolutionProtection(project, repoRoot string) (taintResolutionProtection, error) {
	path := filepath.Join(project, "evidence-taint.json")
	body, _, err := boundedio.ReadRegularFileSnapshot(path, maxPreDecisionArtifactBytes)
	if errors.Is(err, os.ErrNotExist) {
		return taintResolutionProtection{}, nil
	}
	if err != nil {
		return taintResolutionProtection{all: true, evidenceAll: true}, err
	}
	var taint retainedEvidenceTaint
	if err := json.Unmarshal(body, &taint); err != nil {
		return taintResolutionProtection{all: true, evidenceAll: true}, fmt.Errorf("invalid evidence taint JSON: %w", err)
	}
	token, err := retainedEvidenceTaintToken(taint, repoRoot)
	if err != nil {
		return taintResolutionProtection{all: true, evidenceAll: true}, err
	}
	return taintResolutionProtection{
		names: map[string]bool{token + ".json": true},
		evidenceNames: map[string]bool{
			SessionFileID(taint.SessionID): true,
		},
	}, nil
}

func retainedEvidenceTaintToken(taint retainedEvidenceTaint, repoRoot string) (string, error) {
	if taint.FormatVersion != evidenceTaintVersion || taint.RepoRoot != repoRoot ||
		strings.TrimSpace(taint.SessionID) == "" || strings.TrimSpace(taint.Field) == "" {
		return "", errors.New("evidence taint identity is invalid")
	}
	if taint.SegmentCount > 64 {
		return "", errors.New("evidence taint segment count exceeds 64")
	}
	body, err := json.Marshal(taint)
	if err != nil {
		return "", fmt.Errorf("marshal evidence taint token: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func validSessionArtifactName(name string) bool {
	if filepath.Ext(name) != ".json" {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	if stem == "" || len(stem) > maxSessionFileStem {
		return false
	}
	for _, char := range stem {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func lowercaseDigestFileStem(name string) (string, bool) {
	if len(name) != sha256.Size*2+len(".json") || !strings.HasSuffix(name, ".json") {
		return "", false
	}
	stem := strings.TrimSuffix(name, ".json")
	for _, char := range stem {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return "", false
			}
		}
	}
	return stem, true
}

func sameArtifactSnapshot(discovered, opened os.FileInfo) bool {
	return discovered != nil && opened != nil && os.SameFile(discovered, opened) &&
		discovered.Mode() == opened.Mode() && discovered.Size() == opened.Size() &&
		discovered.ModTime().Equal(opened.ModTime())
}
