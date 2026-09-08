package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

type briefingFilesystemEntry struct {
	Mode    os.FileMode
	Size    int64
	ModTime int64
	Digest  [sha256.Size]byte
	IsDir   bool
}

func briefingFilesystemInventory(t *testing.T, roots ...string) map[string]briefingFilesystemEntry {
	t.Helper()
	inventory := make(map[string]briefingFilesystemEntry)
	for _, root := range roots {
		root = filepath.Clean(root)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			observed := briefingFilesystemEntry{
				Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano(), IsDir: info.IsDir(),
			}
			if info.Mode().IsRegular() {
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				observed.Digest = sha256.Sum256(body)
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			inventory[root+string(filepath.Separator)+relative] = observed
			return nil
		})
		if err != nil {
			t.Fatalf("inventory %s: %v", root, err)
		}
	}
	return inventory
}

func overflowBriefingFixture(t *testing.T) (string, agentsession.SessionState, string, string) {
	t.Helper()
	repo := makeAssertRepo(t, "rules: []\n")
	state, err := agentsession.InitializeSessionState(repo, "briefing-overflow-inspection")
	if err != nil {
		t.Fatal(err)
	}
	state.EvidenceOverflow = true
	state.EvidenceOverflowReason = "commands"
	state.EvidenceOverflowLimit = "item_bytes"
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(filepath.Dir(filepath.Dir(state.ReportPath)), "sessions", filepath.Base(state.ReportPath))
	if err := os.WriteFile(statePath, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Dir(filepath.Dir(state.ReportPath))
	taintPath := filepath.Join(projectRoot, "evidence-taint.json")
	if err := os.Remove(taintPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(projectRoot, "locks", filepath.Base(state.ReportPath)),
		filepath.Join(projectRoot, "locks", "active-session.lock"),
	} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	return repo, state, projectRoot, taintPath
}

func TestSessionBriefingOverflowInspectionIsFilesystemReadOnly(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo, state, projectRoot, taintPath := overflowBriefingFixture(t)
	before := briefingFilesystemInventory(t, repo, projectRoot)

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	after := briefingFilesystemInventory(t, repo, projectRoot)
	if len(before) != len(after) {
		t.Fatalf("session-briefing changed file membership: before=%d after=%d\n%s", len(before), len(after), stdout.String())
	}
	for path, want := range before {
		if got, ok := after[path]; !ok || got != want {
			t.Fatalf("session-briefing changed %s: before=%+v after=%+v", path, want, got)
		}
	}
	if _, err := os.Stat(taintPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session-briefing persisted taint for overflowed state: %v", err)
	}
	for _, want := range []string{"session_evidence_status", "uncertain", state.SessionID} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("briefing missing %q: %s", want, stdout.String())
		}
	}
}

func TestEvidenceStatusInspectsOverflowWithoutRepairingTaint(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo, state, projectRoot, taintPath := overflowBriefingFixture(t)
	before := briefingFilesystemInventory(t, repo, projectRoot)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "evidence-status", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("evidence-status: %v", err)
	}
	after := briefingFilesystemInventory(t, repo, projectRoot)
	if len(before) != len(after) {
		t.Fatalf("evidence-status changed file membership: before=%d after=%d", len(before), len(after))
	}
	for path, want := range before {
		if got, ok := after[path]; !ok || got != want {
			t.Fatalf("evidence-status changed %s: before=%+v after=%+v", path, want, got)
		}
	}
	if _, err := os.Stat(taintPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evidence-status persisted taint for overflowed state: %v", err)
	}
	if !strings.Contains(stdout.String(), state.SessionID) || !strings.Contains(stdout.String(), "commands") || !strings.Contains(stdout.String(), `"persisted": false`) {
		t.Fatalf("evidence-status did not report effective overflow: %s", stdout.String())
	}
}

func TestSessionBriefingMalformedActiveStateReportsUncertaintyReadOnly(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo, state, projectRoot, _ := overflowBriefingFixture(t)
	statePath := filepath.Join(projectRoot, "sessions", filepath.Base(state.ReportPath))
	if err := os.WriteFile(statePath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := briefingFilesystemInventory(t, repo, projectRoot)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"session-briefing", repo, "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("session-briefing: %v", err)
	}
	after := briefingFilesystemInventory(t, repo, projectRoot)
	if len(before) != len(after) {
		t.Fatalf("malformed briefing changed file membership")
	}
	for path, want := range before {
		if got, ok := after[path]; !ok || got != want {
			t.Fatalf("malformed briefing changed %s: before=%+v after=%+v", path, want, got)
		}
	}
	if !strings.Contains(stdout.String(), "policy_report_error") || !strings.Contains(stdout.String(), "not valid JSON") {
		t.Fatalf("malformed active state was not reported: %s", stdout.String())
	}
}
