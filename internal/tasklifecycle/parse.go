package tasklifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	sectionsRowRE = regexp.MustCompile(`^- \[([ ~!x])\] ([0-9]{3}) (.+) -> (.+\.md)$`)
	logbookRowRE  = regexp.MustCompile(`^- \[([ x])\] (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) - (.+) -> (.+\.md)$`)
	logbookNameRE = regexp.MustCompile(`^TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)
	logbookCurRE  = regexp.MustCompile(`^Current: (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) -> (.+\.md)$`)
	logbookNoneRE = regexp.MustCompile(`^Current: none$`)
	subTaskRE     = regexp.MustCompile(`^- \[([ ~x])\] (.+)$`)
	bulletFieldRE = regexp.MustCompile(`^(?:- )?([A-Za-z][A-Za-z0-9 _-]*):\s*(.*)$`)
)

var sectionsForProfile = map[Profile][]string{
	ProfileSections: {"Why", "Acceptance", "Sub-Tasks", "Notes", "Deviations"},
	ProfileLogbook:  {"Why", "Status", "Scheduling", "Technical Plan", "Acceptance", "Sub-Tasks", "Notes", "Deviations"},
}

// Inspect reads TASK state without creating locks, reports, or cache files.
// A repository without an overview returns (nil, nil).
func Inspect(repoRoot string) (*Board, error) {
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	return inspectResolved(root)
}

// inspectResolved reads TASK state from a canonical repository root. Runtime
// hotpaths use it after resolving the root once at their process boundary.
func inspectResolved(root string) (*Board, error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	if err := validateTaskRuntimePaths(root, cfg); err != nil {
		return nil, err
	}
	overviewPath := filepath.Join(root, filepath.FromSlash(cfg.OverviewPath))
	if err := rejectSymlinkComponents(root, overviewPath); err != nil {
		return nil, fmt.Errorf("unsafe %s: %w", cfg.OverviewPath, err)
	}
	body, err := os.ReadFile(overviewPath)
	if errors.Is(err, os.ErrNotExist) {
		if cfg.Configured {
			return nil, &ValidationError{Issues: []Issue{{
				ID: "task/overview/missing", Path: cfg.OverviewPath,
				Message:     "the explicitly configured TASK overview is missing",
				Remediation: "restore " + cfg.OverviewPath + " or disable task_lifecycle explicitly",
			}}}
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfg.OverviewPath, err)
	}
	if transactionExists(root) {
		return nil, &ValidationError{Issues: []Issue{{
			ID: "task/transaction/pending", Path: transactionRel,
			Message:     "an interrupted TASK mutation is pending",
			Remediation: "run `reconc task recover " + root + "` before reading or changing TASK state",
		}}}
	}
	return parseOverviewSnapshot(root, cfg, overviewPath, body)
}

func validateTaskRuntimePaths(root string, cfg Config) error {
	paths := []struct {
		label string
		path  string
	}{
		{label: "runtime state", path: transactionRel},
		{label: "detail_dir", path: cfg.DetailDir},
		{label: "done_dir", path: cfg.DoneDir},
	}
	for _, item := range paths {
		abs := filepath.Join(root, filepath.FromSlash(item.path))
		if err := rejectSymlinkComponents(root, abs); err != nil {
			return fmt.Errorf("unsafe TASK %s: %w", item.label, err)
		}
	}
	return nil
}

func parseOverviewSnapshot(root string, cfg Config, overviewPath string, body []byte) (*Board, error) {
	profile, err := detectProfile(cfg.Profile, string(body))
	if err != nil {
		return nil, err
	}
	doneTargetDir, err := filepath.Rel(filepath.Dir(filepath.FromSlash(cfg.OverviewPath)), filepath.FromSlash(cfg.DoneDir))
	if err != nil {
		return nil, fmt.Errorf("resolve done_dir relative to overview: %w", err)
	}
	board := &Board{
		RepoRoot: root, Config: cfg, Profile: profile,
		overviewBytes: body, overviewLines: strings.Split(string(body), "\n"),
		sectionLines: map[State]int{}, tasksByID: map[string]*Task{},
		tasksByName: map[string]*Task{}, tasksByPath: map[string]*Task{}, doneIDs: map[string]bool{},
		doneTargetDir: filepath.ToSlash(doneTargetDir),
	}
	var issues []Issue
	switch profile {
	case ProfileSections:
		issues = board.parseSectionsOverview()
	case ProfileLogbook:
		issues = board.parseLogbookOverview()
	}
	issues = append(issues, board.loadAndValidateDetails()...)
	if board.Profile == ProfileLogbook {
		board.normalizeLogbookBuckets()
	}
	issues = append(issues, board.validateInvariants()...)
	issues = append(issues, concurrentReadIssues(root, cfg, overviewPath, body)...)
	if len(issues) > 0 {
		sortIssues(issues)
		return nil, &ValidationError{Issues: issues}
	}
	return board, nil
}

func concurrentReadIssues(root string, cfg Config, overviewPath string, body []byte) []Issue {
	latestOverview, rereadErr := os.ReadFile(overviewPath)
	if transactionExists(root) || rereadErr != nil || !bytes.Equal(body, latestOverview) {
		return []Issue{issue("task/read/concurrent-mutation", cfg.OverviewPath, 0, "TASK state changed while it was being read", "retry the read; if a transaction remains, run `reconc task recover`")}
	}
	return nil
}

// Load is Inspect with a fail-closed missing-control-plane error for explicit
// task commands.
func Load(repoRoot string) (*Board, error) {
	board, err := Inspect(repoRoot)
	if err != nil {
		return nil, err
	}
	if board == nil {
		return nil, &ValidationError{Issues: []Issue{{
			ID: "task/overview/missing", Path: defaultOverviewPath,
			Message:     "no enabled TASK control plane was found",
			Remediation: "configure task_lifecycle in .reconc.yml or create docs/tasks.md",
		}}}
	}
	return board, nil
}

func detectProfile(configured Profile, content string) (Profile, error) {
	if configured != ProfileAuto {
		return configured, nil
	}
	hasSections := strings.Contains(content, "\n## Active\n") && strings.Contains(content, "\n## Queue\n")
	currentLine := firstMatchingLine(content, "Current:")
	hasLogbook := logbookCurRE.MatchString(currentLine) || logbookNoneRE.MatchString(currentLine)
	switch {
	case hasSections && hasLogbook:
		return "", &ValidationError{Issues: []Issue{{
			ID: "task/profile/ambiguous", Path: defaultOverviewPath,
			Message:     "overview matches both built-in TASK profiles",
			Remediation: "set task_lifecycle.profile explicitly in .reconc.yml",
		}}}
	case hasSections:
		return ProfileSections, nil
	case hasLogbook:
		return ProfileLogbook, nil
	default:
		return "", &ValidationError{Issues: []Issue{{
			ID: "task/profile/unknown", Path: defaultOverviewPath,
			Message:     "overview does not match sections-v1 or logbook-v1",
			Remediation: "set task_lifecycle.profile and use its exact overview grammar",
		}}}
	}
}

func (board *Board) parseSectionsOverview() []Issue {
	headingStates := map[string]State{"Active": StateActive, "Queue": StateQueued, "Blocked": StateBlocked, "Done": StateDone}
	seenHeadings := map[string]bool{}
	current := State("")
	var issues []Issue
	for index, line := range board.overviewLines {
		lineNo := index + 1
		if state, ok := headingStates[strings.TrimPrefix(line, "## ")]; ok && strings.HasPrefix(line, "## ") {
			heading := strings.TrimPrefix(line, "## ")
			if seenHeadings[heading] {
				issues = append(issues, issue("task/overview/duplicate-section", board.Config.OverviewPath, lineNo, "duplicate ## "+heading, "keep exactly one lifecycle section"))
			}
			seenHeadings[heading] = true
			current = state
			board.sectionLines[state] = index
			continue
		}
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		match := sectionsRowRE.FindStringSubmatch(line)
		if match == nil || current == "" {
			issues = append(issues, issue("task/overview/invalid-row", board.Config.OverviewPath, lineNo, "invalid sectioned TASK row", "use `- [icon] NNN Title -> tasks/NNN-slug.md` under a lifecycle section"))
			continue
		}
		iconState := stateForSectionIcon(match[1])
		if iconState != current {
			issues = append(issues, issue("task/overview/state-mismatch", board.Config.OverviewPath, lineNo, "row icon does not match its section", "use [~] in Active, [ ] in Queue, [!] in Blocked, and [x] in Done"))
		}
		task := &Task{ID: match[2], Name: match[2], Title: strings.TrimSpace(match[3]), State: current, Path: filepath.ToSlash(match[4]), OverviewLine: lineNo}
		issues = append(issues, board.appendTask(task)...)
	}
	for _, heading := range []string{"Active", "Queue", "Blocked", "Done"} {
		if !seenHeadings[heading] {
			issues = append(issues, issue("task/overview/missing-section", board.Config.OverviewPath, 0, "missing ## "+heading, "add the canonical lifecycle section"))
		}
	}
	return issues
}

func (board *Board) parseLogbookOverview() []Issue {
	scan := board.scanLogbookOverview()
	if !scan.currentSeen {
		scan.issues = append(scan.issues, issue("task/overview/missing-current", board.Config.OverviewPath, 0, "missing Current line", "add exactly one Current line for the active TASK"))
	}
	for _, task := range scan.rows {
		if task.Name == scan.currentName {
			if task.Path != scan.currentTarget {
				scan.issues = append(scan.issues, issue("task/overview/current-path-mismatch", board.Config.OverviewPath, scan.currentLine, "Current target differs from its TASK row", "make both paths byte-identical"))
			}
			task.State = StateActive
		}
		scan.issues = append(scan.issues, board.appendTask(task)...)
	}
	if scan.currentName != "" && board.Active == nil {
		scan.issues = append(scan.issues, issue("task/overview/current-without-row", board.Config.OverviewPath, scan.currentLine, "Current TASK has no matching row", "add or correct the matching TASK row"))
	}
	return scan.issues
}

type logbookOverviewScan struct {
	currentSeen   bool
	currentName   string
	currentTarget string
	currentLine   int
	rows          []*Task
	issues        []Issue
}

func (board *Board) scanLogbookOverview() logbookOverviewScan {
	var scan logbookOverviewScan
	for index, line := range board.overviewLines {
		lineNo := index + 1
		if strings.HasPrefix(line, "Current:") {
			if scan.currentSeen {
				scan.issues = append(scan.issues, issue("task/overview/invalid-current", board.Config.OverviewPath, lineNo, "duplicate Current line", "keep exactly one Current line"))
				continue
			}
			scan.currentSeen = true
			if logbookNoneRE.MatchString(line) {
				scan.currentLine = lineNo
				continue
			}
			match := logbookCurRE.FindStringSubmatch(line)
			if match == nil {
				scan.issues = append(scan.issues, issue("task/overview/invalid-current", board.Config.OverviewPath, lineNo, "invalid Current line", "use `Current: TASK-NNNN-Name -> tasks/TASK-NNNN-Name.md` or `Current: none`"))
				continue
			}
			scan.currentName = match[1]
			scan.currentTarget = filepath.ToSlash(match[2])
			scan.currentLine = lineNo
			continue
		}
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		if strings.HasPrefix(line, "- [x] ") {
			id, name, target, parseErr := parseArchivedLogbookRow(line)
			if parseErr != nil {
				scan.issues = append(scan.issues, issue("task/overview/invalid-row", board.Config.OverviewPath, lineNo, parseErr.Error(), "use the exact TASK-NNNN logbook row grammar"))
				continue
			}
			scan.issues = append(scan.issues, board.appendArchivedIdentity(id, name, target, lineNo)...)
			continue
		}
		match := logbookRowRE.FindStringSubmatch(line)
		if match == nil {
			scan.issues = append(scan.issues, issue("task/overview/invalid-row", board.Config.OverviewPath, lineNo, "invalid logbook TASK row", "use the exact TASK-NNNN logbook row grammar"))
			continue
		}
		scan.rows = append(scan.rows, &Task{ID: taskNumber(match[2]), Name: match[2], Title: strings.TrimSpace(match[3]), State: StateQueued, Path: filepath.ToSlash(match[4]), OverviewLine: lineNo})
	}
	return scan
}

func (board *Board) appendTask(task *Task) []Issue {
	if _, exists := board.tasksByID[task.ID]; exists {
		return []Issue{issue("task/overview/duplicate-id", board.Config.OverviewPath, task.OverviewLine, "duplicate TASK identifier "+task.Name, "keep one overview row per TASK")}
	}
	if _, exists := board.tasksByName[task.Name]; exists {
		return []Issue{issue("task/overview/duplicate-id", board.Config.OverviewPath, task.OverviewLine, "duplicate TASK name "+task.Name, "keep one overview row per TASK")}
	}
	if _, exists := board.tasksByPath[task.Path]; exists {
		return []Issue{issue("task/overview/duplicate-path", board.Config.OverviewPath, task.OverviewLine, "multiple TASKs point to "+task.Path, "use one distinct detail file per TASK")}
	}
	board.tasksByID[task.ID] = task
	board.tasksByName[task.Name] = task
	board.tasksByPath[task.Path] = task
	if task.State == StateDone {
		board.doneIDs[task.ID] = true
		board.doneIDs[task.Name] = true
		board.doneCount++
	}
	switch task.State {
	case StateActive:
		if board.Active != nil {
			return []Issue{issue("task/overview/multiple-active", board.Config.OverviewPath, task.OverviewLine, "more than one TASK is active", "leave exactly one active TASK")}
		}
		board.Active = task
	case StateQueued:
		board.Queue = append(board.Queue, task)
	case StateBlocked, StatePaused:
		board.Blocked = append(board.Blocked, task)
	case StateDone:
		board.Done = append(board.Done, task)
	}
	return nil
}

func (board *Board) appendArchivedIdentity(id, name, path string, line int) []Issue {
	if err := board.validateArchivedTarget(path); err != nil {
		return []Issue{issue("task/detail/unsafe-path", board.Config.OverviewPath, line, err.Error(), "point the archived row inside the configured done directory")}
	}
	if _, exists := board.tasksByID[id]; exists {
		return []Issue{issue("task/overview/duplicate-id", board.Config.OverviewPath, line, "duplicate TASK identifier "+name, "keep one overview row per TASK")}
	}
	if _, exists := board.tasksByName[name]; exists {
		return []Issue{issue("task/overview/duplicate-id", board.Config.OverviewPath, line, "duplicate TASK name "+name, "keep one overview row per TASK")}
	}
	if _, exists := board.tasksByPath[path]; exists {
		return []Issue{issue("task/overview/duplicate-path", board.Config.OverviewPath, line, "multiple TASKs point to "+path, "use one distinct detail file per TASK")}
	}
	board.tasksByID[id] = nil
	board.tasksByName[name] = nil
	board.tasksByPath[path] = nil
	board.doneIDs[id] = true
	board.doneIDs[name] = true
	board.doneCount++
	return nil
}

func (board *Board) validateArchivedTarget(target string) error {
	if strings.HasPrefix(target, "/") || strings.Contains(target, `\`) || (len(target) >= 2 && target[1] == ':') {
		return fmt.Errorf("archived TASK target %s is absolute or platform-ambiguous", target)
	}
	prefix := ""
	if board.doneTargetDir != "." {
		prefix = strings.TrimSuffix(board.doneTargetDir, "/") + "/"
	}
	if !strings.HasPrefix(target, prefix) {
		return fmt.Errorf("archived TASK target %s is outside configured done_dir", target)
	}
	if suffix := strings.TrimPrefix(target, prefix); suffix == "" || hasUnsafePathSegment(suffix) {
		return fmt.Errorf("archived TASK target %s has an unsafe relative suffix", target)
	}
	return nil
}

func hasUnsafePathSegment(path string) bool {
	for path != "" {
		segment := path
		if slash := strings.IndexByte(path, '/'); slash >= 0 {
			segment, path = path[:slash], path[slash+1:]
		} else {
			path = ""
		}
		if segment == "" || segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func (board *Board) loadAndValidateDetails() []Issue {
	var issues []Issue
	for _, task := range board.allTasks() {
		// Archived detail history was validated at the promotion boundary. The
		// live path trusts the typed overview row and never re-reads an archive
		// whose size may grow for years.
		if task.State == StateDone && board.Profile == ProfileLogbook {
			continue
		}
		path, err := board.resolveTaskTarget(task.Path, task.State)
		if err != nil {
			issues = append(issues, issue("task/detail/unsafe-path", board.Config.OverviewPath, task.OverviewLine, err.Error(), "point the row inside the configured detail or done directory"))
			continue
		}
		if task.State == StateDone {
			if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
				issues = append(issues, issue("task/detail/unreadable", task.Path, 0, "archived detail is missing or not a regular file", "restore the linked archived TASK detail"))
			}
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, issue("task/detail/unreadable", task.Path, 0, err.Error(), "restore the linked TASK detail file"))
			continue
		}
		task.rawDetail = body
		issues = append(issues, board.parseDetail(task)...)
	}
	return issues
}

func (board *Board) parseDetail(task *Task) []Issue {
	content := string(task.rawDetail)
	lines := strings.Split(content, "\n")
	expectedH1 := "# TASK " + task.ID + ": " + task.Title
	if board.Profile == ProfileLogbook {
		expectedH1 = "# " + task.Name
	}
	var issues []Issue
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != expectedH1 {
		issues = append(issues, issue("task/detail/h1-mismatch", task.Path, 1, "H1 does not match the overview row", "set the first line to `"+expectedH1+"`"))
	}
	sections, sectionLines, duplicates := parseH2Sections(lines)
	for _, duplicate := range duplicates {
		issues = append(issues, issue("task/detail/duplicate-section", task.Path, sectionLines[duplicate]+1, "duplicate ## "+duplicate, "keep each TASK section exactly once"))
	}
	required := append([]string{}, sectionsForProfile[board.Profile]...)
	required = append(required, board.Config.Completion.RequiredSections...)
	for _, name := range cleanUnique(required) {
		if _, ok := sections[name]; !ok {
			issues = append(issues, issue("task/detail/missing-section", task.Path, 0, "missing ## "+name, "add the required section"))
		}
	}
	task.SubTasks, issues = parseSubTasks(task.Path, sections["Sub-Tasks"], sectionLines["Sub-Tasks"], issues)
	task.Blocker = parseBlocker(sections)
	task.EvidenceFields = parseEvidenceFields(sections)
	if board.Profile == ProfileLogbook {
		issues = append(issues, board.parseLogbookDetailState(task, sections)...)
		task.Dependencies = parseDependencies(sections["Scheduling"])
	}
	return issues
}

func (board *Board) parseLogbookDetailState(task *Task, sections map[string][]string) []Issue {
	fields := parseFields(sections["Status"])
	rawState := strings.ToLower(strings.TrimSpace(fields["State"]))
	detailState := map[string]State{"active": StateActive, "queued": StateQueued, "blocked": StateBlocked, "paused": StatePaused, "done": StateDone}[rawState]
	if detailState == "" {
		return []Issue{issue("task/detail/invalid-state", task.Path, 0, "Status has an invalid or missing State field", "use Active, Queued, Blocked, Paused, or Done")}
	}
	if board.Active == task && detailState != StateActive {
		return []Issue{issue("task/detail/state-mismatch", task.Path, 0, fmt.Sprintf("Current overview state is active but detail State is %s", detailState), "set the Current TASK State to Active")}
	}
	if board.Active != task && task.State == StateDone && detailState != StateDone {
		return []Issue{issue("task/detail/state-mismatch", task.Path, 0, fmt.Sprintf("checked overview row is done but detail State is %s", detailState), "set the archived TASK State to Done")}
	}
	if board.Active != task && task.State != StateDone && (detailState == StateActive || detailState == StateDone) {
		return []Issue{issue("task/detail/state-mismatch", task.Path, 0, fmt.Sprintf("open non-Current overview row has detail State %s", detailState), "use Queued, Blocked, or Paused, or update Current/the row icon")}
	}
	task.State = detailState
	if task.Blocker == "" {
		task.Blocker = strings.TrimSpace(fields["Blocker"])
	}
	return nil
}

func (board *Board) normalizeLogbookBuckets() {
	all := board.allTasks()
	board.Active = nil
	board.Queue = nil
	board.Blocked = nil
	board.Done = nil
	for _, task := range all {
		switch task.State {
		case StateActive:
			board.Active = task
		case StateQueued:
			board.Queue = append(board.Queue, task)
		case StateBlocked, StatePaused:
			board.Blocked = append(board.Blocked, task)
		case StateDone:
			board.Done = append(board.Done, task)
		}
	}
}

func (board *Board) validateInvariants() []Issue {
	var issues []Issue
	if len(board.Done) > board.Config.DoneVisible && board.Profile == ProfileSections {
		issues = append(issues, issue("task/board/done-window", board.Config.OverviewPath, 0, fmt.Sprintf("Done lists %d TASKs; configured maximum is %d", len(board.Done), board.Config.DoneVisible), "keep only the newest configured number of archived rows visible"))
	}
	for _, task := range board.allTasks() {
		activeSubs := 0
		for _, sub := range task.SubTasks {
			if sub.State == StateActive {
				activeSubs++
			}
		}
		if activeSubs > 1 {
			issues = append(issues, issue("task/detail/multiple-active-subtasks", task.Path, 0, "more than one Sub-Task is active", "leave at most one [~] Sub-Task"))
		}
		if task.State == StateActive && activeSubs == 0 && !taskCompletionReady(task, board.Config) {
			issues = append(issues, issue("task/detail/no-active-subtask", task.Path, 0, "active TASK has no active Sub-Task", "mark the current Sub-Task [~]"))
		}
		if task.State == StateQueued && activeSubs > 0 {
			issues = append(issues, issue("task/detail/queued-active-subtask", task.Path, 0, "queued TASK already has an active Sub-Task", "keep queued Sub-Tasks [ ] until the TASK is claimed"))
		}
		if _, unknown := splitDependencies(board, task); len(unknown) > 0 {
			issues = append(issues, issue("task/detail/unknown-dependency", task.Path, 0, "Depends On references no TASK on the board: "+strings.Join(unknown, ", "), "fix the dependency id or remove the stale entry; an unknown id keeps this TASK unclaimable forever"))
		}
	}
	return issues
}

func (board *Board) resolveOverviewTarget(target string) (string, error) {
	overviewDir := filepath.Dir(filepath.Join(board.RepoRoot, filepath.FromSlash(board.Config.OverviewPath)))
	abs := filepath.Clean(filepath.Join(overviewDir, filepath.FromSlash(target)))
	rel, err := filepath.Rel(board.RepoRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("TASK target escapes the repository: %s", target)
	}
	if err := rejectSymlinkComponents(board.RepoRoot, abs); err != nil {
		return "", fmt.Errorf("unsafe TASK target %s: %w", target, err)
	}
	allowed := []string{filepath.Clean(board.Config.DetailDir), filepath.Clean(board.Config.DoneDir)}
	rel = filepath.Clean(rel)
	for _, dir := range allowed {
		if rel == dir || strings.HasPrefix(rel, dir+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("TASK target %s is outside configured detail directories", target)
}

func (board *Board) resolveTaskTarget(target string, state State) (string, error) {
	abs, err := board.resolveOverviewTarget(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(board.RepoRoot, abs)
	if err != nil {
		return "", fmt.Errorf("resolve TASK target %s: %w", target, err)
	}
	rel = filepath.Clean(rel)
	detailDir := filepath.Clean(filepath.FromSlash(board.Config.DetailDir))
	doneDir := filepath.Clean(filepath.FromSlash(board.Config.DoneDir))
	insideDetail := pathWithinDir(rel, detailDir)
	insideDone := pathWithinDir(rel, doneDir)
	if state == StateDone && !insideDone {
		return "", fmt.Errorf("archived TASK target %s is outside configured done_dir", target)
	}
	if state != StateDone && (!insideDetail || insideDone) {
		return "", fmt.Errorf("live TASK target %s is outside detail_dir or already inside done_dir", target)
	}
	return abs, nil
}

func pathWithinDir(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func parseH2Sections(lines []string) (map[string][]string, map[string]int, []string) {
	sections := map[string][]string{}
	sectionLines := map[string]int{}
	var duplicates []string
	name := ""
	for index, line := range lines {
		if strings.HasPrefix(line, "## ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if _, exists := sections[name]; exists {
				duplicates = append(duplicates, name)
			}
			sections[name] = []string{}
			sectionLines[name] = index
			continue
		}
		if name != "" {
			sections[name] = append(sections[name], line)
		}
	}
	return sections, sectionLines, duplicates
}

func parseSubTasks(path string, lines []string, sectionLine int, issues []Issue) ([]SubTask, []Issue) {
	var out []SubTask
	for index, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "- [") {
			continue
		}
		match := subTaskRE.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			issues = append(issues, issue("task/detail/invalid-subtask", path, sectionLine+index+2, "invalid Sub-Task row", "use - [ ], - [~], or - [x]"))
			continue
		}
		out = append(out, SubTask{State: stateForSubTaskIcon(match[1]), Text: strings.TrimSpace(match[2]), Line: sectionLine + index + 2})
	}
	if len(out) == 0 {
		issues = append(issues, issue("task/detail/no-subtasks", path, sectionLine+1, "Sub-Tasks contains no checklist item", "add at least one real Sub-Task"))
	}
	return out, issues
}

func completionIssues(task *Task, cfg Config) []Issue {
	var issues []Issue
	done := 0
	for _, sub := range task.SubTasks {
		if sub.State != StateDone {
			issues = append(issues, issue("task/completion/open-subtask", task.Path, sub.Line, "Sub-Task is not done: "+sub.Text, "finish it and mark it [x] before completion"))
		} else {
			done++
		}
	}
	if done == 0 {
		issues = append(issues, issue("task/completion/no-done-subtask", task.Path, 0, "no completed Sub-Task proves real work", "record at least one completed Sub-Task"))
	}
	for _, field := range cfg.Completion.RequiredEvidenceFields {
		if strings.TrimSpace(task.EvidenceFields[field]) == "" {
			issues = append(issues, issue("task/completion/missing-evidence", task.Path, 0, "missing required evidence field "+field, "add `- "+field+": <proof>` under ## Evidence"))
		}
	}
	return issues
}

func taskCompletionReady(task *Task, cfg Config) bool {
	return len(completionIssues(task, cfg)) == 0
}

func parseBlocker(sections map[string][]string) string {
	if lines, ok := sections["Blocker"]; ok {
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return strings.TrimSpace(parseFields(sections["Status"])["Blocker"])
}

func parseEvidenceFields(sections map[string][]string) map[string]string {
	fields := parseFields(sections["Evidence"])
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func parseDependencies(lines []string) []string {
	raw := strings.TrimSpace(parseFields(lines)["Depends On"])
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func parseFields(lines []string) map[string]string {
	out := map[string]string{}
	for _, line := range lines {
		match := bulletFieldRE.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		out[strings.TrimSpace(match[1])] = strings.TrimSpace(match[2])
	}
	return out
}

func stateForSectionIcon(icon string) State {
	return map[string]State{" ": StateQueued, "~": StateActive, "!": StateBlocked, "x": StateDone}[icon]
}

func stateForSubTaskIcon(icon string) State {
	return map[string]State{" ": StateQueued, "~": StateActive, "x": StateDone}[icon]
}

func taskNumber(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) > 1 {
		id := parts[1]
		if len(parts) > 2 && len(parts[2]) == 1 && parts[2][0] >= 'A' && parts[2][0] <= 'Z' {
			id += "-" + parts[2]
		}
		return id
	}
	return name
}

func parseArchivedLogbookRow(line string) (string, string, string, error) {
	rest := strings.TrimPrefix(line, "- [x] ")
	nameEnd := strings.Index(rest, " - ")
	targetStart := strings.LastIndex(rest, " -> ")
	if nameEnd <= 0 || targetStart <= nameEnd+3 {
		return "", "", "", fmt.Errorf("invalid archived logbook TASK row")
	}
	name := rest[:nameEnd]
	target := filepath.ToSlash(strings.TrimSpace(rest[targetStart+4:]))
	if !logbookNameRE.MatchString(name) || !strings.HasSuffix(target, ".md") {
		return "", "", "", fmt.Errorf("invalid archived logbook TASK identifier or target")
	}
	return taskNumber(name), name, target, nil
}

func firstMatchingLine(content, prefix string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func issue(id, path string, line int, message, remediation string) Issue {
	return Issue{ID: id, Path: path, Line: line, Message: message, Remediation: remediation}
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		if issues[i].Line != issues[j].Line {
			return issues[i].Line < issues[j].Line
		}
		return issues[i].ID < issues[j].ID
	})
}
