package tasklifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunDisposition is the bounded TASK state consumed by autonomous run control.
type RunDisposition string

const (
	RunContinue RunDisposition = "continue"
	RunClaim    RunDisposition = "claim"
	RunBlocked  RunDisposition = "blocked"
	RunComplete RunDisposition = "complete"
	RunAbsent   RunDisposition = "absent"
	RunInvalid  RunDisposition = "invalid"
)

// RunState is a compact, typed continuation snapshot. It intentionally omits
// completed history, acceptance prose, notes, and other token-heavy fields.
type RunState struct {
	Profile     Profile        `json:"profile,omitempty"`
	Disposition RunDisposition `json:"disposition"`
	TaskID      string         `json:"task_id,omitempty"`
	TaskTitle   string         `json:"task_title,omitempty"`
	TaskPath    string         `json:"task_path,omitempty"`
	SubTask     string         `json:"sub_task,omitempty"`
	Blocker     string         `json:"blocker,omitempty"`
	OpenTasks   int            `json:"open_tasks"`
}

// InspectRunState reads the configured lifecycle once and reduces it to the
// exact information required by the Stop hotpath.
func InspectRunState(repoRoot string) (RunState, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return RunState{}, err
	}
	return InspectRunStateResolved(root)
}

// InspectRunStateResolved reads the compact run snapshot from an already
// canonical repository root.
func InspectRunStateResolved(root string) (RunState, error) {
	if state, ok, err := inspectActiveSectionsRunState(root); ok || err != nil {
		return state, err
	}
	board, err := inspectResolved(root)
	if err != nil {
		return RunState{}, err
	}
	if board == nil {
		return RunState{Disposition: RunAbsent}, nil
	}
	openTasks := len(board.Queue) + len(board.Blocked)
	if board.Active != nil {
		openTasks++
		return RunState{
			Profile: board.Profile, Disposition: RunContinue,
			TaskID: board.Active.ID, TaskTitle: board.Active.Title,
			TaskPath: board.Active.Path, SubTask: currentSubTask(board.Active),
			OpenTasks: openTasks,
		}, nil
	}
	if len(board.Queue) > 0 {
		next, err := selectNext(board, "", false)
		if err != nil {
			return RunState{}, err
		}
		if next != nil {
			return RunState{
				Profile: board.Profile, Disposition: RunClaim,
				TaskID: next.ID, TaskTitle: next.Title, TaskPath: next.Path,
				OpenTasks: openTasks,
			}, nil
		}
		waiting := board.Queue[0]
		return RunState{
			Profile: board.Profile, Disposition: RunBlocked,
			TaskID: waiting.ID, TaskTitle: waiting.Title, TaskPath: waiting.Path,
			Blocker: "queued TASKs have unfinished dependencies", OpenTasks: openTasks,
		}, nil
	}
	if len(board.Blocked) > 0 {
		blocked := board.Blocked[0]
		return RunState{
			Profile: board.Profile, Disposition: RunBlocked,
			TaskID: blocked.ID, TaskTitle: blocked.Title, TaskPath: blocked.Path,
			Blocker:   truncateBriefing(strings.TrimSpace(blocked.Blocker)),
			OpenTasks: openTasks,
		}, nil
	}
	return RunState{Profile: board.Profile, Disposition: RunComplete}, nil
}

// inspectActiveSectionsRunState validates only the live sections-v1 row and
// detail. Queue or terminal states fall back to the complete parser. This
// keeps the common Stop path independent of archived history size without
// weakening mutation and terminal validation.
func inspectActiveSectionsRunState(root string) (RunState, bool, error) {
	cfg, err := LoadConfig(root)
	if err != nil || !cfg.Enabled {
		return RunState{}, false, err
	}
	if cfg.Profile != ProfileAuto && cfg.Profile != ProfileSections {
		return RunState{}, false, nil
	}
	overviewPath := filepath.Join(root, filepath.FromSlash(cfg.OverviewPath))
	pathGuard := runPathGuard{root: root, seen: make(map[string]struct{}, 8)}
	paths := []struct {
		label string
		path  string
	}{
		{label: "TASK runtime state", path: filepath.Join(root, filepath.FromSlash(transactionRel))},
		{label: "TASK detail_dir", path: filepath.Join(root, filepath.FromSlash(cfg.DetailDir))},
		{label: "TASK done_dir", path: filepath.Join(root, filepath.FromSlash(cfg.DoneDir))},
		{label: cfg.OverviewPath, path: overviewPath},
	}
	for _, item := range paths {
		if err := pathGuard.reject(item.path); err != nil {
			return RunState{}, false, fmt.Errorf("unsafe %s: %w", item.label, err)
		}
	}
	body, err := readTaskControlFile(overviewPath)
	if errors.Is(err, os.ErrNotExist) {
		return RunState{}, false, nil
	}
	if err != nil {
		return RunState{}, false, fmt.Errorf("read %s: %w", cfg.OverviewPath, err)
	}
	if cfg.Profile == ProfileAuto && (!bytes.Contains(body, []byte("\n## Active\n")) || bytes.Contains(body, []byte("\nCurrent:"))) {
		return RunState{}, false, nil
	}
	snapshot, ok := scanActiveSectionsRows(body, cfg)
	if !ok || snapshot.Active.Path == "" {
		return RunState{}, false, nil
	}
	pendingTransaction, err := transactionExists(root)
	if err != nil {
		return RunState{}, true, err
	}
	if pendingTransaction {
		return RunState{}, false, nil
	}
	subTask, activeSubs, ok, err := inspectRunSectionsDetail(root, cfg, snapshot.Active, &pathGuard)
	if err != nil {
		return RunState{}, true, err
	}
	if !ok || activeSubs != 1 || subTask == "" {
		return RunState{}, false, nil
	}
	for _, task := range snapshot.Queue {
		_, activeSubs, valid, detailErr := inspectRunSectionsDetail(root, cfg, task, &pathGuard)
		if detailErr != nil {
			return RunState{}, true, detailErr
		}
		if !valid || activeSubs != 0 {
			return RunState{}, false, nil
		}
	}
	for _, task := range snapshot.Blocked {
		_, activeSubs, valid, detailErr := inspectRunSectionsDetail(root, cfg, task, &pathGuard)
		if detailErr != nil {
			return RunState{}, true, detailErr
		}
		if !valid || activeSubs > 1 {
			return RunState{}, false, nil
		}
	}
	latest, err := readTaskControlFile(overviewPath)
	pendingTransaction, transactionErr := transactionExists(root)
	if transactionErr != nil {
		return RunState{}, true, transactionErr
	}
	if err != nil || !bytes.Equal(body, latest) || pendingTransaction {
		return RunState{}, true, &ValidationError{Issues: []Issue{
			issue("task/read/concurrent-mutation", cfg.OverviewPath, 0, "TASK state changed while it was being read", "retry the read; if a transaction remains, run `reconc task recover`"),
		}}
	}
	return RunState{
		Profile: ProfileSections, Disposition: RunContinue,
		TaskID: snapshot.Active.ID, TaskTitle: snapshot.Active.Title, TaskPath: snapshot.Active.Path,
		SubTask: subTask, OpenTasks: snapshot.OpenTasks,
	}, true, nil
}

type runPathGuard struct {
	root string
	seen map[string]struct{}
}

func (guard *runPathGuard) reject(abs string) error {
	rel, err := filepath.Rel(guard.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes the repository")
	}
	current := guard.root
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		if _, ok := guard.seen[current]; ok {
			continue
		}
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect path component %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path uses symlink component %s", current)
		}
		guard.seen[current] = struct{}{}
	}
	return nil
}

type runSection uint8

const (
	runSectionNone runSection = iota
	runSectionActive
	runSectionQueue
	runSectionBlocked
	runSectionDone
)

type runRowIdentity struct {
	id   []byte
	path []byte
}

type runSectionsSnapshot struct {
	Active    Task
	Queue     []Task
	Blocked   []Task
	OpenTasks int
}

func scanActiveSectionsRows(body []byte, cfg Config) (runSectionsSnapshot, bool) {
	section := runSectionNone
	sectionCounts := [5]uint8{}
	identities := make([]runRowIdentity, 0, 16)
	snapshot := runSectionsSnapshot{}
	doneTasks := 0
	for len(body) > 0 {
		line := body
		if newline := bytes.IndexByte(body, '\n'); newline >= 0 {
			line, body = body[:newline], body[newline+1:]
		} else {
			body = nil
		}
		switch {
		case bytes.Equal(line, []byte("## Active")):
			section = runSectionActive
			sectionCounts[section]++
			continue
		case bytes.Equal(line, []byte("## Queue")):
			section = runSectionQueue
			sectionCounts[section]++
			continue
		case bytes.Equal(line, []byte("## Blocked")):
			section = runSectionBlocked
			sectionCounts[section]++
			continue
		case bytes.Equal(line, []byte("## Done")):
			section = runSectionDone
			sectionCounts[section]++
			continue
		}
		if !bytes.HasPrefix(line, []byte("- [")) {
			continue
		}
		var wantPrefix []byte
		switch section {
		case runSectionActive:
			wantPrefix = []byte("- [~] ")
		case runSectionQueue:
			wantPrefix = []byte("- [ ] ")
		case runSectionBlocked:
			wantPrefix = []byte("- [!] ")
		case runSectionDone:
			wantPrefix = []byte("- [x] ")
		}
		if len(wantPrefix) == 0 || !bytes.HasPrefix(line, wantPrefix) {
			return runSectionsSnapshot{}, false
		}
		row, ok := parseRunSectionsRow(line[len(wantPrefix):])
		if !ok || !runSectionsTargetAllowed(cfg, row.path, section == runSectionDone) {
			return runSectionsSnapshot{}, false
		}
		for _, identity := range identities {
			if bytes.Equal(identity.id, row.id) || bytes.Equal(identity.path, row.path) {
				return runSectionsSnapshot{}, false
			}
		}
		identities = append(identities, runRowIdentity{id: row.id, path: row.path})
		if section == runSectionDone {
			doneTasks++
			continue
		}
		snapshot.OpenTasks++
		task := Task{ID: string(row.id), Title: string(row.title), Path: filepath.ToSlash(string(row.path))}
		switch section {
		case runSectionActive:
			if snapshot.Active.Path != "" {
				return runSectionsSnapshot{}, false
			}
			snapshot.Active = task
		case runSectionQueue:
			snapshot.Queue = append(snapshot.Queue, task)
		case runSectionBlocked:
			snapshot.Blocked = append(snapshot.Blocked, task)
		}
	}
	for section := runSectionActive; section <= runSectionDone; section++ {
		if sectionCounts[section] != 1 {
			return runSectionsSnapshot{}, false
		}
	}
	if doneTasks > cfg.DoneVisible {
		return runSectionsSnapshot{}, false
	}
	return snapshot, true
}

func runSectionsTargetAllowed(cfg Config, target []byte, done bool) bool {
	path := filepath.Clean(filepath.FromSlash(string(target)))
	if filepath.IsAbs(path) {
		return false
	}
	overviewDir := filepath.Dir(filepath.FromSlash(cfg.OverviewPath))
	rel := filepath.Clean(filepath.Join(overviewDir, path))
	detailDir := filepath.Clean(filepath.FromSlash(cfg.DetailDir))
	doneDir := filepath.Clean(filepath.FromSlash(cfg.DoneDir))
	if done {
		return pathWithinDir(rel, doneDir)
	}
	return pathWithinDir(rel, detailDir) && !pathWithinDir(rel, doneDir)
}

type parsedRunRow struct {
	id    []byte
	title []byte
	path  []byte
}

func parseRunSectionsRow(row []byte) (parsedRunRow, bool) {
	if len(row) < 8 || row[0] < '0' || row[0] > '9' || row[1] < '0' || row[1] > '9' || row[2] < '0' || row[2] > '9' || row[3] != ' ' {
		return parsedRunRow{}, false
	}
	separator := bytes.LastIndex(row[4:], []byte(" -> "))
	if separator < 0 {
		return parsedRunRow{}, false
	}
	separator += 4
	title := bytes.TrimSpace(row[4:separator])
	target := bytes.TrimSpace(row[separator+4:])
	if len(title) == 0 || !bytes.HasSuffix(target, []byte(".md")) {
		return parsedRunRow{}, false
	}
	return parsedRunRow{id: row[:3], title: title, path: target}, true
}

func inspectRunSectionsDetail(root string, cfg Config, task Task, pathGuard *runPathGuard) (string, int, bool, error) {
	target := filepath.Clean(filepath.FromSlash(task.Path))
	overviewDir := filepath.Dir(filepath.FromSlash(cfg.OverviewPath))
	detailDir := filepath.Clean(filepath.FromSlash(cfg.DetailDir))
	doneDir := filepath.Clean(filepath.FromSlash(cfg.DoneDir))
	abs := filepath.Clean(filepath.Join(root, overviewDir, target))
	rel, relErr := filepath.Rel(root, abs)
	if relErr != nil || filepath.IsAbs(target) || !pathWithinDir(rel, detailDir) || pathWithinDir(rel, doneDir) {
		return "", 0, false, nil
	}
	if err := pathGuard.reject(abs); err != nil {
		return "", 0, true, fmt.Errorf("unsafe %s: %w", task.Path, err)
	}
	body, err := readTaskControlFile(abs)
	if err != nil {
		return "", 0, true, fmt.Errorf("read %s: %w", task.Path, err)
	}
	expectedH1 := "# TASK " + task.ID + ": " + task.Title
	if first, _, _ := bytes.Cut(body, []byte{'\n'}); strings.TrimSpace(string(first)) != expectedH1 {
		return "", 0, false, nil
	}
	var headings [][]byte
	inSubTasks := false
	current := ""
	activeSubTasks := 0
	subTasks := 0
	for len(body) > 0 {
		line := body
		if newline := bytes.IndexByte(body, '\n'); newline >= 0 {
			line, body = body[:newline], body[newline+1:]
		} else {
			body = nil
		}
		if bytes.HasPrefix(line, []byte("## ")) {
			heading := bytes.TrimSpace(line[3:])
			if len(heading) == 0 || containsByteSlice(headings, heading) {
				return "", 0, false, nil
			}
			headings = append(headings, heading)
			inSubTasks = bytes.Equal(heading, []byte("Sub-Tasks"))
			continue
		}
		if !inSubTasks {
			continue
		}
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("- [")) {
			continue
		}
		if len(trimmed) <= 6 || trimmed[0] != '-' || trimmed[1] != ' ' || trimmed[2] != '[' || trimmed[4] != ']' || trimmed[5] != ' ' || (trimmed[3] != ' ' && trimmed[3] != '~' && trimmed[3] != 'x') {
			return "", 0, false, nil
		}
		subTasks++
		if trimmed[3] == '~' {
			current = strings.TrimSpace(string(trimmed[6:]))
			activeSubTasks++
		}
	}
	for _, required := range sectionsForProfile[ProfileSections] {
		if !containsByteSlice(headings, []byte(required)) {
			return "", 0, false, nil
		}
	}
	for _, required := range cfg.Completion.RequiredSections {
		if !containsByteSlice(headings, []byte(required)) {
			return "", 0, false, nil
		}
	}
	if subTasks == 0 || (activeSubTasks > 0 && current == "") {
		return "", 0, false, nil
	}
	return current, activeSubTasks, true, nil
}

func containsByteSlice(values [][]byte, target []byte) bool {
	for _, value := range values {
		if bytes.Equal(value, target) {
			return true
		}
	}
	return false
}
