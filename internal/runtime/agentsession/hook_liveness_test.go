package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestHookLivenessTrimInvalidatesArtifactsAndAllowsReinsert(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookObservationAt(root, "omp", "event-00", repo, 1, false, base); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxHookLivenessRoutes; i++ {
		event := fmt.Sprintf("event-%02d", i)
		if err := recordHookLivenessAt(root, "omp", event, base.Add(time.Duration(i)*7*time.Hour)); err != nil {
			t.Fatalf("record %s: %v", event, err)
		}
	}
	for _, path := range []string{
		hookLivenessMarkerPath(root, "omp", "event-00"),
		hookObservationPath(root, "omp", "event-00"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("trimmed route artifact remains at %s: %v", path, err)
		}
	}
	if hookLivenessMarkerCurrent(root, "omp", "event-00", base.Add(33*7*time.Hour)) {
		t.Fatal("trimmed route retained a current marker")
	}
	// Reinsert with a timestamp older than every retained route. Trimming must
	// still preserve the route being recorded instead of publishing a marker
	// for an immediately evicted route.
	reinserted := base.Add(time.Minute)
	if err := recordHookLivenessAt(root, "omp", "event-00", reinserted); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := records["omp"].Routes["event-00"]; got != reinserted.Format(time.RFC3339Nano) {
		t.Fatalf("reinserted route = %q", got)
	}
	if got := len(records["omp"].Routes); got != maxHookLivenessRoutes {
		t.Fatalf("routes after reinsert = %d, want %d", got, maxHookLivenessRoutes)
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

func TestHookLivenessRejectsMarkerFromEarlierStateGeneration(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookLivenessAt(root, "codex", "session-start", first); err != nil {
		t.Fatal(err)
	}
	if !hookLivenessMarkerCurrent(root, "codex", "session-start", first.Add(time.Minute)) {
		t.Fatal("new marker is not current")
	}
	if err := recordHookLivenessAt(root, "codex", "codex-stop", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if hookLivenessMarkerCurrent(root, "codex", "session-start", first.Add(2*time.Hour)) {
		t.Fatal("marker from an earlier liveness generation remained current")
	}
	stateBefore, err := os.ReadFile(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := recordHookLivenessAt(root, "codex", "session-start", first.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	stateAfter, err := os.ReadFile(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(stateBefore) != string(stateAfter) {
		t.Fatal("marker recovery rewrote a still-current route record")
	}
	if !hookLivenessMarkerCurrent(root, "codex", "session-start", first.Add(2*time.Hour)) {
		t.Fatal("marker recovery did not bind the current liveness generation")
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

func TestHookObservationUpdatesSidecarWithoutRewritingLiveness(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 1, false, first); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	generationBefore, ok := hookLivenessStateGeneration(root)
	if !ok {
		t.Fatal("liveness generation unavailable")
	}
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 2, true, first.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stateAfter, err := os.ReadFile(hookLivenessPath(root))
	if err != nil {
		t.Fatal(err)
	}
	generationAfter, ok := hookLivenessStateGeneration(root)
	if !ok {
		t.Fatal("liveness generation unavailable after observation")
	}
	if string(stateBefore) != string(stateAfter) || generationBefore != generationAfter {
		t.Fatal("incremental observation rewrote unrelated liveness state")
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	observation := records["omp"].Observations["omp-user-python"]
	if observation.Count != 2 || observation.CodeBytes != 2 || !observation.ExcludeFromContext ||
		observation.LastSeen != first.Add(time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("merged observation = %+v", observation)
	}
}

func TestHookObservationConcurrentUpdatesDoNotLoseCounts(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	const observations = 32
	start := make(chan struct{})
	errors := make(chan error, observations)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	var workers sync.WaitGroup
	for i := 0; i < observations; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			errors <- recordHookObservationAt(root, "omp", "omp-user-python", repo, index, false, now)
		}(i)
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := records["omp"].Observations["omp-user-python"].Count; got != observations {
		t.Fatalf("concurrent observation count = %d, want %d", got, observations)
	}
}

func TestHookObservationRecoversMissingAndOrphanedSidecars(t *testing.T) {
	t.Setenv(StateRootEnv, t.TempDir())
	repo := t.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	orphan := HookObservation{Count: 99, LastSeen: now.Format(time.RFC3339Nano), WorkingDirectory: ".", CodeBytes: 99}
	if err := writeHookObservation(root, "omp", "omp-user-python", now.Format(time.RFC3339Nano), "", orphan); err != nil {
		t.Fatal(err)
	}
	records, err := ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("orphan observation became live: %+v", records)
	}
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 1, false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	records, err = ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := records["omp"].Observations["omp-user-python"].Count; got != 1 {
		t.Fatalf("orphan count was resurrected: %d", got)
	}
	if err := os.Remove(hookObservationPath(root, "omp", "omp-user-python")); err != nil {
		t.Fatal(err)
	}
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 2, false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	records, err = ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := records["omp"].Observations["omp-user-python"].Count; got != 1 {
		t.Fatalf("missing sidecar recovery count = %d, want 1", got)
	}
	staleSidecar, err := os.ReadFile(hookObservationPath(root, "omp", "omp-user-python"))
	if err != nil {
		t.Fatal(err)
	}
	baseRecords, err := readHookLivenessBaseResolved(root)
	if err != nil {
		t.Fatal(err)
	}
	omp := baseRecords["omp"]
	delete(omp.Routes, "omp-user-python")
	baseRecords["omp"] = omp
	if err := writeHookLivenessRecords(root, baseRecords); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookObservationPath(root, "omp", "omp-user-python"), staleSidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	reinserted := now.Add(3 * time.Minute)
	if err := recordHookLivenessAt(root, "omp", "omp-user-python", reinserted); err != nil {
		t.Fatal(err)
	}
	records, err = ReadHookLiveness(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := records["omp"].Observations["omp-user-python"]; exists {
		t.Fatal("stale sidecar from a removed route was resurrected after reinsert")
	}
}

func BenchmarkRecordHookObservationIncremental(b *testing.B) {
	b.Setenv(StateRootEnv, b.TempDir())
	repo := b.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 1, false, now); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, i, false, now.Add(time.Minute)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordHookObservationRouteRefresh(b *testing.B) {
	b.Setenv(StateRootEnv, b.TempDir())
	repo := b.TempDir()
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, 1, false, now); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(hookLivenessWriteInterval)
		if err := recordHookObservationAt(root, "omp", "omp-user-python", repo, i, false, now); err != nil {
			b.Fatal(err)
		}
	}
}
