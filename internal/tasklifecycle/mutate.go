package tasklifecycle

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxBlockerBytes = 500

var stateFieldRE = regexp.MustCompile(`^State:\s*(Active|Queued|Blocked|Paused|Done)\s*$`)

// MutationResult is stable JSON for every state-changing command.
type MutationResult struct {
	Action        string `json:"action"`
	TaskID        string `json:"task_id"`
	TaskPath      string `json:"task_path"`
	PreviousState State  `json:"previous_state"`
	State         State  `json:"state"`
	NextTaskID    string `json:"next_task_id,omitempty"`
}

// Claim activates one queued TASK when no TASK is currently active.
func Claim(repoRoot, id string) (MutationResult, error) {
	return mutate(repoRoot, "claim", func(board *Board) (MutationResult, []fileMutation, []moveMutation, error) {
		if board.Active != nil {
			return MutationResult{}, nil, nil, fmt.Errorf("TASK %s is already active; block or archive it before claiming another TASK", board.Active.ID)
		}
		task, err := requireTask(board, id, StateQueued)
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		if !dependenciesDone(board, task) {
			return MutationResult{}, nil, nil, fmt.Errorf("TASK %s has unfinished dependencies: %s", task.ID, strings.Join(task.Dependencies, ", "))
		}
		detail, err := activateDetail(board.Profile, task.rawDetail)
		if err != nil {
			return MutationResult{}, nil, nil, fmt.Errorf("activate %s: %w", task.Path, err)
		}
		overview, err := setOverviewStates(board, []overviewStateChange{{Task: task, State: StateActive}}, task)
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		files, err := board.fileMutations(overload(task, detail), overview)
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		return result("claim", task, StateQueued, StateActive, ""), files, nil, nil
	})
}

// Block moves the active TASK to Blocked. When queued work exists, nextID is
// optional and defaults to the first executable queued TASK.
func Block(repoRoot, reason, nextID string) (MutationResult, error) {
	reason, err := normalizeBlocker(reason)
	if err != nil {
		return MutationResult{}, err
	}
	return mutate(repoRoot, "block", func(board *Board) (MutationResult, []fileMutation, []moveMutation, error) {
		return buildBlockMutation(board, reason, nextID)
	})
}

func buildBlockMutation(board *Board, reason, nextID string) (MutationResult, []fileMutation, []moveMutation, error) {
	if board.Active == nil {
		return MutationResult{}, nil, nil, fmt.Errorf("no active TASK to block")
	}
	current := board.Active
	next, err := selectNext(board, nextID, false)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	details, changes, err := blockedDetails(board, current, next, reason)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	overview, err := setOverviewStates(board, changes, next)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	files, err := board.fileMutations(details, overview)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	return result("block", current, StateActive, StateBlocked, taskID(next)), files, nil, nil
}

func blockedDetails(board *Board, current, next *Task, reason string) (map[*Task][]byte, []overviewStateChange, error) {
	currentDetail, err := setDetailState(board.Profile, current.rawDetail, StateBlocked, reason)
	if err != nil {
		return nil, nil, err
	}
	details := map[*Task][]byte{current: currentDetail}
	changes := []overviewStateChange{{Task: current, State: StateBlocked}}
	if next == nil {
		return details, changes, nil
	}
	nextDetail, err := activateDetail(board.Profile, next.rawDetail)
	if err != nil {
		return nil, nil, err
	}
	details[next] = nextDetail
	return details, append(changes, overviewStateChange{Task: next, State: StateActive}), nil
}

// Resume reactivates a blocked TASK when no TASK is active.
func Resume(repoRoot, id string) (MutationResult, error) {
	return mutate(repoRoot, "resume", func(board *Board) (MutationResult, []fileMutation, []moveMutation, error) {
		if board.Active != nil {
			return MutationResult{}, nil, nil, fmt.Errorf("TASK %s is active; finish or block it before resuming another TASK", board.Active.ID)
		}
		task, err := requireTask(board, id, StateBlocked, StatePaused)
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		detail, err := setDetailState(board.Profile, task.rawDetail, StateActive, "")
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		overview, err := setOverviewStates(board, []overviewStateChange{{Task: task, State: StateActive}}, task)
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		files, err := board.fileMutations(overload(task, detail), overview)
		if err != nil {
			return MutationResult{}, nil, nil, err
		}
		return result("resume", task, task.State, StateActive, ""), files, nil, nil
	})
}

// Split blocks the active parent and atomically activates the first named
// pre-existing queued child. It never fabricates child TASK files.
func Split(repoRoot string, childIDs []string) (MutationResult, error) {
	childIDs = cleanUnique(childIDs)
	if len(childIDs) < 2 {
		return MutationResult{}, fmt.Errorf("split requires at least two distinct child TASK identifiers")
	}
	return mutate(repoRoot, "split", func(board *Board) (MutationResult, []fileMutation, []moveMutation, error) {
		return buildSplitMutation(board, childIDs)
	})
}

func buildSplitMutation(board *Board, childIDs []string) (MutationResult, []fileMutation, []moveMutation, error) {
	parent, children, err := requireSplitChildren(board, childIDs)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	parentDetail, err := setDetailState(board.Profile, parent.rawDetail, StateBlocked, "Split into TASKs "+strings.Join(childIDs, ", "))
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	childDetail, err := activateDetail(board.Profile, children[0].rawDetail)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	changes := []overviewStateChange{{Task: parent, State: StateBlocked}, {Task: children[0], State: StateActive}}
	overview, err := setOverviewStates(board, changes, children[0])
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	files, err := board.fileMutations(map[*Task][]byte{parent: parentDetail, children[0]: childDetail}, overview)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	return result("split", parent, StateActive, StateBlocked, children[0].ID), files, nil, nil
}

func requireSplitChildren(board *Board, childIDs []string) (*Task, []*Task, error) {
	if board.Active == nil {
		return nil, nil, fmt.Errorf("no active parent TASK to split")
	}
	parent := board.Active
	children := make([]*Task, 0, len(childIDs))
	for _, id := range childIDs {
		child, err := requireTask(board, id, StateQueued)
		if err != nil {
			return nil, nil, err
		}
		if !detailReferencesParent(child, parent) {
			return nil, nil, fmt.Errorf("child TASK %s Why section must reference parent %s before split", child.ID, parent.ID)
		}
		children = append(children, child)
	}
	if !dependenciesDone(board, children[0]) {
		return nil, nil, fmt.Errorf("first child TASK %s has unfinished dependencies", children[0].ID)
	}
	return parent, children, nil
}

// Promote archives the completed active TASK and activates the selected or
// first executable queued TASK.
func Promote(repoRoot, nextID string) (MutationResult, error) {
	return archive(repoRoot, "promote", nextID, true)
}

// Archive archives a terminal active TASK without selecting a successor.
func Archive(repoRoot string) (MutationResult, error) {
	return archive(repoRoot, "archive", "", false)
}

// CheckCompletion returns every completion failure for one TASK. An empty
// slice means the detail is ready for Promote or Archive.
func CheckCompletion(repoRoot, id string) (string, []Issue, error) {
	board, err := Load(repoRoot)
	if err != nil {
		return "", nil, err
	}
	var task *Task
	if id == "" {
		task = board.Active
	} else {
		task, _ = board.findTask(id)
	}
	if task == nil {
		return "", nil, fmt.Errorf("TASK %q was not found", id)
	}
	issues := completionIssues(task, board.Config)
	sortIssues(issues)
	return task.ID, issues, nil
}

func archive(repoRoot, action, nextID string, promote bool) (MutationResult, error) {
	return mutate(repoRoot, action, func(board *Board) (MutationResult, []fileMutation, []moveMutation, error) {
		return buildArchiveMutation(board, action, nextID, promote)
	})
}

func buildArchiveMutation(board *Board, action, nextID string, promote bool) (MutationResult, []fileMutation, []moveMutation, error) {
	current, next, err := archiveTasks(board, action, nextID, promote)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	sourceRel, destinationRel, target, err := archivePaths(board, current)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	details, changes, err := archiveDetails(board, current, next, target)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	overview, err := setOverviewStates(board, changes, next)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	files, err := board.fileMutations(details, overview)
	if err != nil {
		return MutationResult{}, nil, nil, err
	}
	mutationResult := result(action, current, StateActive, StateDone, taskID(next))
	mutationResult.TaskPath = destinationRel
	moves := []moveMutation{{Source: sourceRel, Destination: destinationRel}}
	return mutationResult, files, moves, nil
}

func archiveTasks(board *Board, action, nextID string, promote bool) (*Task, *Task, error) {
	if board.Active == nil {
		return nil, nil, fmt.Errorf("no active TASK to %s", action)
	}
	current := board.Active
	if issues := completionIssues(current, board.Config); len(issues) > 0 {
		sortIssues(issues)
		return nil, nil, &ValidationError{Issues: issues}
	}
	if !promote {
		if len(board.Queue) > 0 {
			return nil, nil, fmt.Errorf("%d queued TASK(s) remain; use `reconc task promote` so one becomes active", len(board.Queue))
		}
		return current, nil, nil
	}
	next, err := selectNext(board, nextID, true)
	if err != nil {
		return nil, nil, err
	}
	return current, next, nil
}

func archivePaths(board *Board, current *Task) (string, string, string, error) {
	sourceAbs, err := board.resolveTaskTarget(current.Path, current.State)
	if err != nil {
		return "", "", "", err
	}
	destinationRel := filepath.ToSlash(filepath.Join(board.Config.DoneDir, filepath.Base(sourceAbs)))
	destinationAbs := filepath.Join(board.RepoRoot, filepath.FromSlash(destinationRel))
	overviewDir := filepath.Dir(filepath.Join(board.RepoRoot, filepath.FromSlash(board.Config.OverviewPath)))
	target, err := filepath.Rel(overviewDir, destinationAbs)
	if err != nil {
		return "", "", "", err
	}
	sourceRel, err := filepath.Rel(board.RepoRoot, sourceAbs)
	return filepath.ToSlash(sourceRel), destinationRel, filepath.ToSlash(target), err
}

func archiveDetails(board *Board, current, next *Task, target string) (map[*Task][]byte, []overviewStateChange, error) {
	details := map[*Task][]byte{}
	changes := []overviewStateChange{{Task: current, State: StateDone, Path: target}}
	if board.Profile == ProfileLogbook {
		doneDetail, err := setDetailState(board.Profile, current.rawDetail, StateDone, "")
		if err != nil {
			return nil, nil, err
		}
		details[current] = doneDetail
	}
	if next == nil {
		return details, changes, nil
	}
	nextDetail, err := activateDetail(board.Profile, next.rawDetail)
	if err != nil {
		return nil, nil, err
	}
	details[next] = nextDetail
	return details, append(changes, overviewStateChange{Task: next, State: StateActive}), nil
}

type mutationBuilder func(*Board) (MutationResult, []fileMutation, []moveMutation, error)

func mutate(repoRoot, action string, build mutationBuilder) (MutationResult, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return MutationResult{}, err
	}
	var result MutationResult
	err = withMutationLock(root, func() error {
		board, loadErr := Load(root)
		if loadErr != nil {
			return loadErr
		}
		built, files, moves, buildErr := build(board)
		if buildErr != nil {
			return buildErr
		}
		if applyErr := applyTransaction(root, action, files, moves); applyErr != nil {
			return applyErr
		}
		result = built
		return nil
	})
	return result, err
}

type overviewStateChange struct {
	Task  *Task
	State State
	Path  string
}

func setOverviewStates(board *Board, changes []overviewStateChange, active *Task) ([]byte, error) {
	lines := append([]string{}, board.overviewLines...)
	if board.Profile == ProfileSections {
		return setSectionedOverview(lines, changes, board.Config.DoneVisible)
	}
	return setLogbookOverview(lines, changes, active)
}

func setSectionedOverview(lines []string, changes []overviewStateChange, doneVisible int) ([]byte, error) {
	for _, change := range changes {
		updated, err := moveSectionedRow(lines, change.Task.ID, change.Task.Title, changedTaskPath(change), change.State)
		if err != nil {
			return nil, err
		}
		lines = updated
	}
	lines = normalizeSectionSpacing(trimDoneRows(lines, doneVisible))
	return []byte(strings.Join(lines, "\n")), nil
}

func normalizeSectionSpacing(lines []string) []string {
	out := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		if !isLifecycleSectionHeading(lines[index]) {
			out = append(out, lines[index])
			index++
			continue
		}
		out = append(out, lines[index], "")
		contentStart := len(out)
		index++
		for index < len(lines) && !strings.HasPrefix(lines[index], "## ") {
			if strings.TrimSpace(lines[index]) != "" {
				out = append(out, lines[index])
			}
			index++
		}
		if len(out) > contentStart {
			out = append(out, "")
		}
	}
	return out
}

func isLifecycleSectionHeading(line string) bool {
	switch line {
	case "## Active", "## Queue", "## Blocked", "## Done":
		return true
	default:
		return false
	}
}

func setLogbookOverview(lines []string, changes []overviewStateChange, active *Task) ([]byte, error) {
	for _, change := range changes {
		if err := replaceLogbookRow(lines, change); err != nil {
			return nil, err
		}
	}
	for index, line := range lines {
		if strings.HasPrefix(line, "Current:") {
			if active == nil {
				lines[index] = "Current: none"
			} else {
				lines[index] = fmt.Sprintf("Current: %s -> %s", active.Name, active.Path)
			}
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	return nil, fmt.Errorf("logbook overview lost its Current line")
}

func replaceLogbookRow(lines []string, change overviewStateChange) error {
	icon := " "
	if change.State == StateDone {
		icon = "x"
	}
	for index, line := range lines {
		match := logbookRowRE.FindStringSubmatch(line)
		if match == nil || match[2] != change.Task.Name {
			continue
		}
		lines[index] = fmt.Sprintf("- [%s] %s - %s -> %s", icon, change.Task.Name, change.Task.Title, changedTaskPath(change))
		return nil
	}
	return fmt.Errorf("overview row for %s disappeared during mutation", change.Task.Name)
}

func changedTaskPath(change overviewStateChange) string {
	if change.Path != "" {
		return change.Path
	}
	return change.Task.Path
}

func moveSectionedRow(lines []string, id, title, path string, state State) ([]string, error) {
	found := false
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		match := sectionsRowRE.FindStringSubmatch(line)
		if match != nil && match[2] == id {
			found = true
			continue
		}
		filtered = append(filtered, line)
	}
	if !found {
		return nil, fmt.Errorf("overview row for TASK %s disappeared during mutation", id)
	}
	heading, icon := sectionStateRendering(state)
	row := fmt.Sprintf("- [%s] %s %s -> %s", icon, id, title, path)
	insert := -1
	for index, line := range filtered {
		if line != "## "+heading {
			continue
		}
		insert = index + 1
		for insert < len(filtered) && strings.TrimSpace(filtered[insert]) == "" {
			insert++
		}
		break
	}
	if insert < 0 {
		return nil, fmt.Errorf("overview missing ## %s during mutation", heading)
	}
	filtered = append(filtered, "")
	copy(filtered[insert+1:], filtered[insert:])
	filtered[insert] = row
	return filtered, nil
}

func trimDoneRows(lines []string, limit int) []string {
	seen := 0
	out := make([]string, 0, len(lines))
	insideDone := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			insideDone = line == "## Done"
		}
		if insideDone {
			match := sectionsRowRE.FindStringSubmatch(line)
			if match != nil && match[1] == "x" {
				seen++
				if seen > limit {
					continue
				}
			}
		}
		out = append(out, line)
	}
	return out
}

func sectionStateRendering(state State) (string, string) {
	switch state {
	case StateActive:
		return "Active", "~"
	case StateBlocked, StatePaused:
		return "Blocked", "!"
	case StateDone:
		return "Done", "x"
	default:
		return "Queue", " "
	}
}

func (board *Board) fileMutations(details map[*Task][]byte, overview []byte) ([]fileMutation, error) {
	files := []fileMutation{{Path: board.Config.OverviewPath, After: overview}}
	tasks := make([]*Task, 0, len(details))
	for task := range details {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Path < tasks[j].Path })
	for _, task := range tasks {
		abs, err := board.resolveOverviewTarget(task.Path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(board.RepoRoot, abs)
		if err != nil {
			return nil, err
		}
		files = append(files, fileMutation{Path: filepath.ToSlash(rel), After: details[task]})
	}
	return files, nil
}

func overload(task *Task, body []byte) map[*Task][]byte {
	return map[*Task][]byte{task: body}
}

func activateDetail(profile Profile, body []byte) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	hasActive := false
	firstOpen := -1
	insideSubTasks := false
	for index, line := range lines {
		if strings.HasPrefix(line, "## ") {
			insideSubTasks = line == "## Sub-Tasks"
			continue
		}
		if !insideSubTasks {
			continue
		}
		match := subTaskRE.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		if match[1] == "~" {
			hasActive = true
		}
		if match[1] == " " && firstOpen < 0 {
			firstOpen = index
		}
	}
	if !hasActive {
		if firstOpen < 0 {
			return nil, fmt.Errorf("TASK has no open Sub-Task to activate")
		}
		lines[firstOpen] = strings.Replace(lines[firstOpen], "- [ ]", "- [~]", 1)
	}
	updated := []byte(strings.Join(lines, "\n"))
	return setDetailState(profile, updated, StateActive, "")
}

func setDetailState(profile Profile, body []byte, state State, blocker string) ([]byte, error) {
	if profile == ProfileSections {
		return setBlockerSection(body, blocker), nil
	}
	lines := strings.Split(string(body), "\n")
	stateIndex, blockerIndex, statusEnd := logbookStatusIndexes(lines)
	if stateIndex < 0 {
		return nil, fmt.Errorf("logbook TASK detail has no State field under ## Status")
	}
	lines[stateIndex] = "State: " + logbookState(state)
	lines = updateLogbookBlocker(lines, blocker, blockerIndex, statusEnd)
	return []byte(strings.Join(lines, "\n")), nil
}

func logbookStatusIndexes(lines []string) (int, int, int) {
	insideStatus := false
	stateIndex := -1
	blockerIndex := -1
	statusEnd := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if insideStatus {
				statusEnd = index
				break
			}
			insideStatus = line == "## Status"
			continue
		}
		if !insideStatus {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if stateFieldRE.MatchString(trimmed) {
			stateIndex = index
		}
		if strings.HasPrefix(trimmed, "Blocker:") {
			blockerIndex = index
		}
	}
	return stateIndex, blockerIndex, statusEnd
}

func updateLogbookBlocker(lines []string, blocker string, blockerIndex, statusEnd int) []string {
	if blockerIndex >= 0 {
		if blocker == "" {
			return append(lines[:blockerIndex], lines[blockerIndex+1:]...)
		}
		lines[blockerIndex] = "Blocker: " + blocker
		return lines
	}
	if blocker == "" {
		return lines
	}
	if statusEnd < 0 {
		statusEnd = len(lines)
	}
	lines = append(lines, "")
	copy(lines[statusEnd+1:], lines[statusEnd:])
	lines[statusEnd] = "Blocker: " + blocker
	return lines
}

func setBlockerSection(body []byte, blocker string) []byte {
	lines := strings.Split(string(body), "\n")
	start, end := -1, -1
	for index, line := range lines {
		if line == "## Blocker" {
			start = index
			continue
		}
		if start >= 0 && index > start && strings.HasPrefix(line, "## ") {
			end = index
			break
		}
	}
	if start >= 0 {
		if end < 0 {
			end = len(lines)
		}
		lines = append(lines[:start], lines[end:]...)
	}
	if blocker == "" {
		return []byte(strings.Join(lines, "\n"))
	}
	insert := len(lines)
	for index, line := range lines {
		if line == "## Notes" {
			insert = index
			break
		}
	}
	block := []string{"## Blocker", "", blocker, ""}
	result := make([]string, 0, len(lines)+len(block))
	result = append(result, lines[:insert]...)
	result = append(result, block...)
	result = append(result, lines[insert:]...)
	return []byte(strings.Join(result, "\n"))
}

func selectNext(board *Board, requested string, allowBlocked bool) (*Task, error) {
	if requested != "" {
		task, ok := board.findTask(requested)
		if !ok {
			return nil, fmt.Errorf("next TASK %q was not found", requested)
		}
		if task.State != StateQueued && !(allowBlocked && (task.State == StateBlocked || task.State == StatePaused)) {
			return nil, fmt.Errorf("next TASK %s is %s, not an eligible queued%s TASK", task.ID, task.State, blockedSuffix(allowBlocked))
		}
		if !dependenciesDone(board, task) {
			return nil, fmt.Errorf("next TASK %s has unfinished dependencies", task.ID)
		}
		return task, nil
	}
	for _, task := range board.Queue {
		if dependenciesDone(board, task) {
			return task, nil
		}
	}
	return nil, nil
}

func blockedSuffix(allow bool) string {
	if allow {
		return " or blocked"
	}
	return ""
}

func dependenciesDone(board *Board, task *Task) bool {
	if len(task.Dependencies) == 0 {
		return true
	}
	done := make(map[string]bool, len(board.doneIDs)+len(board.Done)*2)
	for id := range board.doneIDs {
		done[id] = true
	}
	for _, item := range board.Done {
		done[item.ID] = true
		done[item.Name] = true
	}
	for _, dependency := range task.Dependencies {
		if !done[dependency] {
			return false
		}
	}
	return true
}

func requireTask(board *Board, id string, states ...State) (*Task, error) {
	task, ok := board.findTask(strings.TrimSpace(id))
	if !ok {
		return nil, fmt.Errorf("TASK %q was not found", id)
	}
	for _, state := range states {
		if task.State == state {
			return task, nil
		}
	}
	return nil, fmt.Errorf("TASK %s is %s; expected %s", task.ID, task.State, joinStates(states))
}

func joinStates(states []State) string {
	values := make([]string, len(states))
	for index, state := range states {
		values[index] = string(state)
	}
	return strings.Join(values, " or ")
}

func detailReferencesParent(child, parent *Task) bool {
	sections, _, _ := parseH2Sections(strings.Split(string(child.rawDetail), "\n"))
	why := strings.Join(sections["Why"], "\n")
	return strings.Contains(why, parent.Name) || strings.Contains(why, "TASK "+parent.ID) || strings.Contains(why, "TASK-"+parent.ID)
}

func normalizeBlocker(reason string) (string, error) {
	reason = strings.Join(strings.Fields(strings.TrimSpace(reason)), " ")
	if reason == "" {
		return "", fmt.Errorf("block requires a non-empty --reason")
	}
	if len([]byte(reason)) > maxBlockerBytes {
		return "", fmt.Errorf("block reason exceeds %d bytes", maxBlockerBytes)
	}
	return reason, nil
}

func logbookState(state State) string {
	return map[State]string{StateActive: "Active", StateQueued: "Queued", StateBlocked: "Blocked", StatePaused: "Paused", StateDone: "Done"}[state]
}

func result(action string, task *Task, before, after State, next string) MutationResult {
	return MutationResult{Action: action, TaskID: task.ID, TaskPath: task.Path, PreviousState: before, State: after, NextTaskID: next}
}

func taskID(task *Task) string {
	if task == nil {
		return ""
	}
	return task.ID
}
