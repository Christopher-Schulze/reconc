package tasklifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestInspectSectionsAndBoundedBriefing(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Current work", state: StateActive, subTasks: "- [~] Build the real thing"},
		{id: "002", title: "Waiting", state: StateBlocked, subTasks: "- [~] Keep exact context", blocker: strings.Repeat("long blocker ", 40)},
	})
	for index := 0; index < 200; index++ {
		writeFile(t, repo, fmt.Sprintf("docs/tasks/done/%03d-history.md", index+100), []byte("archive noise"))
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if board.Profile != ProfileSections || board.Active == nil || board.Active.ID != "001" {
		t.Fatalf("unexpected board: %#v", board)
	}
	briefing := BuildBriefing(board)
	if briefing.Current == nil || briefing.Current.CurrentSubTask != "Build the real thing" {
		t.Fatalf("unexpected briefing current: %#v", briefing.Current)
	}
	if len([]rune(briefing.Blockers[0].Reason)) > maxBriefingTextRunes {
		t.Fatalf("blocker was not bounded: %d runes", len([]rune(briefing.Blockers[0].Reason)))
	}
	body, err := json.Marshal(briefing)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 2048 {
		t.Fatalf("briefing grew beyond bounded contract: %d bytes", len(body))
	}
}

func TestInspectLogbookAdoptsBlockedDetailState(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0001-Active -> tasks/TASK-0001-Active.md\n\n- [ ] TASK-0001-Active - Active work -> tasks/TASK-0001-Active.md\n- [ ] TASK-0002-Paused - Paused work -> tasks/TASK-0002-Paused.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0001-Active.md", logbookDetail("TASK-0001-Active", "Active", "- [~] Work"))
	writeFile(t, repo, "docs/tasks/TASK-0002-Paused.md", logbookDetail("TASK-0002-Paused", "Blocked", "- [~] Resume later"))
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(board.Blocked) != 1 || board.Blocked[0].ID != "0002" {
		t.Fatalf("blocked detail state not adopted: %#v", board.Blocked)
	}
}

func TestLoadConfigRejectsUnknownTaskLifecycleField(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("rules: []\ntask_lifecycle:\n  done_visble: 10\n"))
	_, err := LoadConfig(repo)
	if err == nil || !strings.Contains(err.Error(), "field done_visble not found") {
		t.Fatalf("expected strict task-lifecycle field error, got %v", err)
	}
}

func TestInspectLogbookRejectsUnsafeArchivedTargetWithoutOpeningArchive(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0001-Active -> tasks/TASK-0001-Active.md\n\n- [ ] TASK-0001-Active - Active work -> tasks/TASK-0001-Active.md\n- [x] TASK-0002-History - History -> ../../outside.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0001-Active.md", logbookDetail("TASK-0001-Active", "Active", "- [~] Work"))
	_, err := Inspect(repo)
	if err == nil || !strings.Contains(err.Error(), "task/detail/unsafe-path") {
		t.Fatalf("unsafe archived target must fail closed, got %v", err)
	}
}

func TestInspectRejectsSymlinkedDetailTarget(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}})
	target := filepath.Join(repo, "docs/tasks/001-current.md")
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, filepath.Dir(outside), filepath.Base(outside), []byte("outside\n"))
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Inspect(repo)
	if err == nil || !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("symlinked detail must fail closed, got %v", err)
	}
}

func TestBriefingBoundsConfiguredEvidenceFields(t *testing.T) {
	fields := make([]string, 0, 10)
	for index := 0; index < 10; index++ {
		fields = append(fields, fmt.Sprintf("Proof %d", index))
	}
	config := "task_lifecycle:\n  profile: sections-v1\n  completion:\n    required_evidence_fields:\n"
	for _, field := range fields {
		config += "      - " + field + "\n"
	}
	repo := sectionedRepo(t, config, []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}})
	board, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	briefing := BuildBriefing(board)
	if len(briefing.RequiredEvidence) != maxBriefingEvidence || briefing.OmittedEvidence != 4 {
		t.Fatalf("evidence briefing is not bounded: %#v", briefing)
	}
}

func TestClaimIsRaceSafe(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "First", state: StateQueued, subTasks: "- [ ] First work"},
		{id: "002", title: "Second", state: StateQueued, subTasks: "- [ ] Second work"},
	})
	var wait sync.WaitGroup
	wait.Add(2)
	errorsOut := make(chan error, 2)
	for _, id := range []string{"001", "002"} {
		id := id
		go func() {
			defer wait.Done()
			_, err := Claim(repo, id)
			errorsOut <- err
		}()
	}
	wait.Wait()
	close(errorsOut)
	successes := 0
	for err := range errorsOut {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one concurrent claim must win, got %d", successes)
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect after claim race: %v", err)
	}
	if board.Active == nil || len(board.Queue) != 1 {
		t.Fatalf("race left invalid board: %#v", board)
	}
	if transactionExists(repo) {
		t.Fatal("successful mutation left a transaction journal")
	}
}

func TestBlockAndResumePreserveCurrentSubTask(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Work", state: StateActive, subTasks: "- [~] Keep this pointer"}})
	result, err := Block(repo, "missing operator credential", "")
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if result.State != StateBlocked {
		t.Fatalf("unexpected block result: %#v", result)
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect blocked: %v", err)
	}
	if board.Active != nil || board.Blocked[0].Blocker != "missing operator credential" {
		t.Fatalf("block state missing: %#v", board)
	}
	if _, err := Resume(repo, "001"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	board, err = Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect resumed: %v", err)
	}
	if board.Active == nil || currentSubTask(board.Active) != "Keep this pointer" || board.Active.Blocker != "" {
		t.Fatalf("resume lost state: %#v", board.Active)
	}
}

func TestSplitRequiresPrecreatedChildrenAndActivatesFirst(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Large parent", state: StateActive, subTasks: "- [~] Decompose"},
		{id: "002", title: "Child A", state: StateQueued, subTasks: "- [ ] Build A", why: "Split from TASK 001."},
		{id: "003", title: "Child B", state: StateQueued, subTasks: "- [ ] Build B", why: "Split from TASK 001."},
	})
	result, err := Split(repo, []string{"002", "003"})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if result.NextTaskID != "002" {
		t.Fatalf("first child not activated: %#v", result)
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect split: %v", err)
	}
	if board.Active == nil || board.Active.ID != "002" || len(board.Blocked) != 1 || board.Blocked[0].ID != "001" {
		t.Fatalf("split state wrong: %#v", board)
	}
}

func TestPromoteArchivesAndActivatesNext(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Finished", state: StateActive, subTasks: "- [x] Complete"},
		{id: "002", title: "Next", state: StateQueued, subTasks: "- [ ] Continue"},
	})
	result, err := Promote(repo, "")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result.State != StateDone || result.NextTaskID != "002" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs/tasks/001-finished.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source detail still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs/tasks/done/001-finished.md")); err != nil {
		t.Fatalf("archived detail missing: %v", err)
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect promoted: %v", err)
	}
	if board.Active == nil || board.Active.ID != "002" || len(board.Done) != 1 {
		t.Fatalf("promoted board wrong: %#v", board)
	}
	overview, err := os.ReadFile(filepath.Join(repo, "docs/tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(overview), "\n\n\n") || !strings.Contains(string(overview), "\n\n## Queue\n") {
		t.Fatalf("promotion left non-canonical section spacing:\n%s", overview)
	}
}

func TestArchiveTerminalSectionedTask(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Terminal", state: StateActive, subTasks: "- [x] Complete"}})
	result, err := Archive(repo)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if result.State != StateDone || result.NextTaskID != "" {
		t.Fatalf("unexpected terminal archive: %#v", result)
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect archived board: %v", err)
	}
	if board.Active != nil || len(board.Done) != 1 {
		t.Fatalf("terminal archive left wrong board: %#v", board)
	}
}

func TestPromoteLogbookUpdatesStateBeforeVerifiedMove(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0001-Current -> tasks/TASK-0001-Current.md\n\n- [ ] TASK-0001-Current - Current -> tasks/TASK-0001-Current.md\n- [ ] TASK-0002-Next - Next -> tasks/TASK-0002-Next.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0001-Current.md", logbookDetail("TASK-0001-Current", "Active", "- [x] Complete"))
	writeFile(t, repo, "docs/tasks/TASK-0002-Next.md", logbookDetail("TASK-0002-Next", "Queued", "- [ ] Continue"))
	result, err := Promote(repo, "")
	if err != nil {
		t.Fatalf("Promote logbook: %v", err)
	}
	if result.NextTaskID != "0002" {
		t.Fatalf("wrong logbook successor: %#v", result)
	}
	archived, err := os.ReadFile(filepath.Join(repo, "docs/tasks/done/TASK-0001-Current.md"))
	if err != nil || !strings.Contains(string(archived), "State: Done") {
		t.Fatalf("archived logbook detail was not finalized: err=%v\n%s", err, archived)
	}
	board, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect promoted logbook: %v", err)
	}
	if board.Active == nil || board.Active.ID != "0002" || currentSubTask(board.Active) != "Continue" {
		t.Fatalf("next logbook TASK not active: %#v", board.Active)
	}
}

func TestArchiveTerminalLogbookWritesExplicitEmptyCurrent(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0001-Terminal -> tasks/TASK-0001-Terminal.md\n\n- [ ] TASK-0001-Terminal - Terminal work -> tasks/TASK-0001-Terminal.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0001-Terminal.md", logbookDetail("TASK-0001-Terminal", "Active", "- [x] Complete"))
	result, err := Archive(repo)
	if err != nil {
		t.Fatalf("Archive logbook: %v", err)
	}
	if result.NextTaskID != "" {
		t.Fatalf("terminal archive selected a successor: %#v", result)
	}
	overview, err := os.ReadFile(filepath.Join(repo, "docs", "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(overview), "Current: none") || strings.Contains(string(overview), "Current: TASK-") {
		t.Fatalf("terminal logbook did not render explicit empty Current:\n%s", overview)
	}
	runState, err := InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if runState.Disposition != RunComplete || runState.OpenTasks != 0 {
		t.Fatalf("terminal logbook run state is not complete: %#v", runState)
	}
}

func TestClaimLogbookFromExplicitEmptyCurrent(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: none\n\n- [ ] TASK-0002-Queued - Queued work -> tasks/TASK-0002-Queued.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0002-Queued.md", logbookDetail("TASK-0002-Queued", "Queued", "- [ ] Start"))
	runState, err := InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if runState.Disposition != RunClaim || runState.TaskID != "0002" {
		t.Fatalf("queued logbook run state is not claimable: %#v", runState)
	}
	if _, err := Claim(repo, "0002"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	runState, err = InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if runState.Disposition != RunContinue || runState.TaskID != "0002" || runState.SubTask != "Start" {
		t.Fatalf("claimed logbook run state is not executable: %#v", runState)
	}
}

func TestInspectRunStateDoesNotClaimTaskWithUnfinishedDependency(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: none\n\n- [ ] TASK-0001-Blocked - Blocked work -> tasks/TASK-0001-Blocked.md\n- [ ] TASK-0002-Waiting - Waiting work -> tasks/TASK-0002-Waiting.md\n"))
	blocked := logbookDetail("TASK-0001-Blocked", "Blocked", "- [ ] Resume")
	blocked = bytes.Replace(blocked, []byte("## Notes\n"), []byte("## Blocker\n\ncredential missing\n\n## Notes\n"), 1)
	writeFile(t, repo, "docs/tasks/TASK-0001-Blocked.md", blocked)
	waiting := logbookDetail("TASK-0002-Waiting", "Queued", "- [ ] Start")
	waiting = bytes.Replace(waiting, []byte("- Depends On: none"), []byte("- Depends On: TASK-0001-Blocked"), 1)
	writeFile(t, repo, "docs/tasks/TASK-0002-Waiting.md", waiting)
	state, err := InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != RunBlocked || state.TaskID != "0002" || state.Blocker != "queued TASKs have unfinished dependencies" {
		t.Fatalf("dependency-blocked queue was treated as executable: %#v", state)
	}
}

func TestBlockLogbookWithoutSuccessorWritesEmptyCurrent(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n\nCurrent: TASK-0003-Blocked -> tasks/TASK-0003-Blocked.md\n\n- [ ] TASK-0003-Blocked - Blocked work -> tasks/TASK-0003-Blocked.md\n"))
	writeFile(t, repo, "docs/tasks/TASK-0003-Blocked.md", logbookDetail("TASK-0003-Blocked", "Active", "- [~] Resume here"))
	if _, err := Block(repo, "credential missing", ""); err != nil {
		t.Fatalf("Block: %v", err)
	}
	overview, err := os.ReadFile(filepath.Join(repo, "docs", "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(overview), "Current: none") {
		t.Fatalf("blocked logbook did not clear Current:\n%s", overview)
	}
	state, err := InspectRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != RunBlocked || state.TaskID != "0003" || state.Blocker != "credential missing" {
		t.Fatalf("blocked logbook run state is wrong: %#v", state)
	}
}

func TestInspectRunStateSectionedDispositions(t *testing.T) {
	tests := []struct {
		name string
		task testTask
		want RunDisposition
	}{
		{name: "active", task: testTask{id: "001", title: "Active", state: StateActive, subTasks: "- [~] Work"}, want: RunContinue},
		{name: "queued", task: testTask{id: "001", title: "Queued", state: StateQueued, subTasks: "- [ ] Work"}, want: RunClaim},
		{name: "blocked", task: testTask{id: "001", title: "Blocked", state: StateBlocked, subTasks: "- [~] Work", blocker: "credential missing"}, want: RunBlocked},
		{name: "complete", task: testTask{id: "001", title: "Done", state: StateDone, subTasks: "- [x] Work"}, want: RunComplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := sectionedRepo(t, "", []testTask{test.task})
			state, err := InspectRunState(repo)
			if err != nil {
				t.Fatal(err)
			}
			if state.Disposition != test.want {
				t.Fatalf("disposition=%q want %q: %#v", state.Disposition, test.want, state)
			}
		})
	}
	state, err := InspectRunState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if state.Disposition != RunAbsent {
		t.Fatalf("missing TASK plane disposition=%q", state.Disposition)
	}
}

func TestRecoverRejectsExternalEditsThenRollsBackExactState(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Queued", state: StateQueued, subTasks: "- [ ] Work"}})
	overviewPath := filepath.Join(repo, "docs/tasks.md")
	before, err := os.ReadFile(overviewPath)
	if err != nil {
		t.Fatal(err)
	}
	after := []byte(strings.Replace(string(before), "- [ ] 001", "- [~] 001", 1))
	journal, err := buildTransaction(repo, "test", []fileMutation{{Path: "docs/tasks.md", After: after}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(journal)
	writeFile(t, repo, transactionRel, body)
	writeFile(t, repo, "docs/tasks.md", []byte("external edit\n"))
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected recovery conflict, got %v", err)
	}
	writeFile(t, repo, "docs/tasks.md", after)
	if err := Recover(repo); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	restored, _ := os.ReadFile(overviewPath)
	if string(restored) != string(before) {
		t.Fatalf("recovery did not restore exact bytes\nwant=%q\ngot=%q", before, restored)
	}
}

func TestRecoverReversesVerifiedMove(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("exact detail\n"))
	files := []fileMutation{}
	moves := []moveMutation{{Source: "docs/tasks/001-source.md", Destination: "docs/tasks/done/001-source.md"}}
	journal, err := buildTransaction(repo, "test-move", files, moves)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(journal)
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, moves); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err != nil {
		t.Fatalf("Recover move: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(repo, "docs/tasks/001-source.md"))
	if err != nil || string(restored) != "exact detail\n" {
		t.Fatalf("move recovery lost content: err=%v body=%q", err, restored)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs/tasks/done/001-source.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("move destination remains after recovery: %v", err)
	}
}

func TestRecoverReversesFileMutationFollowedByVerifiedMove(t *testing.T) {
	repo := t.TempDir()
	before := []byte("State: Active\n")
	after := []byte("State: Done\n")
	writeFile(t, repo, "docs/tasks/001-source.md", before)
	if err := os.Chmod(filepath.Join(repo, "docs/tasks/001-source.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []fileMutation{{Path: "docs/tasks/001-source.md", After: after}}
	moves := []moveMutation{{Source: "docs/tasks/001-source.md", Destination: "docs/tasks/done/001-source.md"}}
	journal, err := buildTransaction(repo, "test-file-move", files, moves)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(journal)
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, moves); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err != nil {
		t.Fatalf("Recover combined file and move mutation: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(repo, "docs/tasks/001-source.md"))
	if err != nil || string(restored) != string(before) {
		t.Fatalf("combined recovery lost before image: err=%v body=%q", err, restored)
	}
	info, err := os.Stat(filepath.Join(repo, "docs/tasks/001-source.md"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("combined recovery changed file mode: %v", info.Mode().Perm())
	}
}

func BenchmarkInspectIgnoresUnlinkedArchiveGrowth(b *testing.B) {
	repo := sectionedRepo(b, "", []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}})
	for index := 0; index < 5000; index++ {
		writeFile(b, repo, fmt.Sprintf("docs/tasks/done/%04d-history.md", index), []byte("archived history"))
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := Inspect(repo); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInspectLogbookLargeArchiveIndex(b *testing.B) {
	repo := b.TempDir()
	writeFile(b, repo, ".reconc.yml", []byte("task_lifecycle:\n  profile: logbook-v1\n"))
	var overview strings.Builder
	overview.WriteString("# Tasks\n\nCurrent: TASK-0001-Active -> tasks/TASK-0001-Active.md\n\n")
	overview.WriteString("- [ ] TASK-0001-Active - Active work -> tasks/TASK-0001-Active.md\n")
	for index := 1000; index < 6000; index++ {
		fmt.Fprintf(&overview, "- [x] TASK-%04d-History - Archived history -> tasks/done/TASK-%04d-History.md\n", index, index)
	}
	writeFile(b, repo, "docs/tasks.md", []byte(overview.String()))
	writeFile(b, repo, "docs/tasks/TASK-0001-Active.md", logbookDetail("TASK-0001-Active", "Active", "- [~] Work"))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := Inspect(repo); err != nil {
			b.Fatal(err)
		}
	}
}

type testTask struct {
	id       string
	title    string
	state    State
	subTasks string
	blocker  string
	why      string
}

type testWriter interface {
	Helper()
	Fatal(args ...any)
	TempDir() string
}

func sectionedRepo(t testWriter, config string, tasks []testTask) string {
	t.Helper()
	repo := t.TempDir()
	if config == "" {
		config = "task_lifecycle:\n  profile: sections-v1\n"
	}
	writeFile(t, repo, ".reconc.yml", []byte(config))
	sections := map[State][]string{StateActive: {}, StateQueued: {}, StateBlocked: {}, StateDone: {}}
	for _, task := range tasks {
		slug := strings.ToLower(strings.ReplaceAll(task.title, " ", "-"))
		detailRel := fmt.Sprintf("tasks/%s-%s.md", task.id, slug)
		repoDetailRel := "docs/" + detailRel
		if task.state == StateDone {
			detailRel = fmt.Sprintf("tasks/done/%s-%s.md", task.id, slug)
			repoDetailRel = "docs/" + detailRel
		}
		headingState := task.state
		if headingState == StatePaused {
			headingState = StateBlocked
		}
		_, icon := sectionStateRendering(headingState)
		sections[headingState] = append(sections[headingState], fmt.Sprintf("- [%s] %s %s -> %s", icon, task.id, task.title, detailRel))
		why := task.why
		if why == "" {
			why = "Test motivation."
		}
		var blocker string
		if task.blocker != "" {
			blocker = "\n## Blocker\n\n" + task.blocker + "\n"
		}
		detail := fmt.Sprintf("# TASK %s: %s\n\n## Why\n\n%s\n\n## Acceptance\n\n- Measurable result.\n\n## Sub-Tasks\n\n%s\n%s\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n", task.id, task.title, why, task.subTasks, blocker)
		writeFile(t, repo, repoDetailRel, []byte(detail))
	}
	overview := "# TASK Control Plane\n\n## Active\n\n" + strings.Join(sections[StateActive], "\n") + "\n\n## Queue\n\n" + strings.Join(sections[StateQueued], "\n") + "\n\n## Blocked\n\n" + strings.Join(sections[StateBlocked], "\n") + "\n\n## Done\n\n" + strings.Join(sections[StateDone], "\n") + "\n"
	writeFile(t, repo, "docs/tasks.md", []byte(overview))
	return repo
}

func logbookDetail(name, state, subTasks string) []byte {
	return []byte(fmt.Sprintf("# %s\n\n## Why\n\nReason.\n\n## Status\n\nState: %s\n\n## Scheduling\n\n- Depends On: none\n\n## Technical Plan\n\nReal plan.\n\n## Acceptance\n\n- Result.\n\n## Sub-Tasks\n\n%s\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n", name, state, subTasks))
}

func writeFile(t interface {
	Helper()
	Fatal(args ...any)
}, repo, rel string, body []byte) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
