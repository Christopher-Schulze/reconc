package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHookLivenessIsRateLimitedPerRoute(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "session_start", first); err != nil {
		t.Fatal(err)
	}
	path := hookLivenessPath(root)
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordHookLivenessAt(root, "codex", "session_start", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("rate-limited liveness event rewrote state")
	}
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := recordHookLivenessAt(root, "codex", "session_start", first.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["codex"].LastSeen != first.Add(7*time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("liveness timestamp not refreshed: %+v", records["codex"])
	}
	if len(records["codex"].Routes) != 2 {
		t.Fatalf("per-route liveness missing: %+v", records["codex"])
	}
}

func TestHookLivenessCapsRoutesAtReadLimit(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	// Record more distinct events than the read-side cap so the writer must
	// evict the oldest route; the persisted record must stay readable.
	for i := 0; i <= maxHookLivenessRoutes; i++ {
		event := fmt.Sprintf("event-%02d", i)
		if err := recordHookLivenessAt(root, "codex", event, base.Add(time.Duration(i)*7*time.Hour)); err != nil {
			t.Fatalf("record event %d: %v", i, err)
		}
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatalf("liveness record exceeded the read cap: %v", err)
	}
	if got := len(records["codex"].Routes); got != maxHookLivenessRoutes {
		t.Fatalf("routes = %d, want capped at %d", got, maxHookLivenessRoutes)
	}
}

func TestHookLivenessReadIsBoundedAndFailClosed(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := hookLivenessPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, maxHookLivenessBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHookLiveness(repo); err == nil {
		t.Fatal("oversized liveness state must fail closed")
	}
}

func TestHookLivenessRebuildsMissingFastPathMarkerWithoutRewritingState(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first); err != nil {
		t.Fatal(err)
	}
	stateInfo, err := os.Stat(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	markerPath := hookLivenessMarkerPath(root, "codex", "codex-stop")
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("fast-path marker was not rebuilt: %v", err)
	}
	if !markerInfo.ModTime().Equal(first) {
		t.Fatalf("rebuilt marker must preserve the recorded route time: %s", markerInfo.ModTime())
	}
	stateInfoAfter, err := os.Stat(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !stateInfo.ModTime().Equal(stateInfoAfter.ModTime()) {
		t.Fatal("rebuilding a marker rewrote liveness state")
	}
}

func TestHookLivenessRebuildsMissingStateDespiteFreshMarker(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hookLivenessPath(root)); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Hour)
	if err := recordHookLivenessAt(root, "codex", "codex-stop", second); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["codex"].Routes["codex-stop"] != second.Format(time.RFC3339Nano) {
		t.Fatalf("missing liveness state was not rebuilt: %+v", records["codex"])
	}
}

func TestRecordHookLivenessNormalizesRuntimeAndPreservesRoute(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if err := RecordHookLiveness(repo, "codex-session-start", "codex-session-start"); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["codex"].Event != "codex-session-start" || records["codex"].Routes["codex-session-start"] == "" {
		t.Fatalf("normalized runtime liveness missing: %+v", records)
	}
}

func TestHookObservationPreservesLegacyLivenessRoute(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacySeen := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	legacy := fmt.Sprintf(`{"omp":{"runtime":"omp","last_seen":%q,"event":"omp-session-start"}}`, legacySeen)
	if err := os.MkdirAll(filepath.Dir(hookLivenessPath(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookLivenessPath(root), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 1, false, time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if records["omp"].Routes["omp-session-start"] != legacySeen {
		t.Fatalf("legacy liveness route was lost: %+v", records["omp"])
	}
}
