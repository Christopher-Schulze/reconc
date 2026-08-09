// Package main implements promote-task-done: an atomic mutator that takes a
// finished TASK from docs/tasks/<NAME>.md to docs/tasks/done/<NAME>.md while
// keeping docs/tasks.md, the Current header and the next executable TASK in
// sync. All validation is delegated to
// project/tools/reconc/harness/template/audits/lib/donecheck so the tool and the workflow
// audit cannot drift apart.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"reconc-harness/template/audits/lib/donecheck"
)

const (
	tasksRel        = "docs/tasks.md"
	stateActive     = "Active"
	stateQueued     = "Queued"
	stateDone       = "Done"
	currentPrefix   = "Current: "
	openIcon        = " "
	doneIcon        = "x"
	subTaskOpen     = "- [ ] "
	subTaskActive   = "- [~] "
	stateLine       = "State: "
	dependsOnLabel  = "Depends On"
	headerStatus    = "## Status"
	headerSubTasks  = "## Sub-Tasks"
	headerScheduled = "## Scheduling"
	lockRel         = ".reconc/promote-task-done.lock"
	auditRunner     = "tools/reconc/harness/template/audits/run-workflow-audit"
	schemaRel       = "tools/reconc/harness/template/config/workflow/task-schema.yaml"
	auditTimeout    = 2 * time.Minute
	maxInputBytes   = 4 << 20
	maxAuditOutput  = 1 << 20
)

// loadedSchema is the workflow Schema used by every donecheck call.
var loadedSchema = donecheck.DefaultSchema()

var (
	rowRe = regexp.MustCompile(
		`^- \[([ x])\] (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) - (.+) -> (tasks(?:/done)?/TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*\.md)$`,
	)
	currentRe = regexp.MustCompile(
		`^Current: (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) -> (tasks(?:/done)?/TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*\.md)$`,
	)
)

type taskRow struct {
	icon        string
	name        string
	description string
	target      string
	line        int
}

type taskIndex struct {
	currentName   string
	currentTarget string
	currentLine   int
	rows          []taskRow
	rawLines      []string
}

type detailInfo struct {
	state        string
	dependencies []string
}

type options struct {
	dryRun            bool
	verify            bool
	allowEmptyCurrent bool
	taskName          string
}

func main() {
	dryRun := flag.Bool("dry-run", false, "validate and print plan without writing changes")
	verify := flag.Bool("verify", false, "after a successful mutation run `run-workflow-audit task-state`; rollback on failure")
	allowEmpty := flag.Bool("allow-empty-current", false, "permit promotion when no next executable TASK exists; otherwise refuse to leave Current pointing at a checked row")
	repoRoot := flag.String("repo-root", "", "repository root; the root launcher supplies this for the nested Go module")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: promote-task-done [--dry-run] [--verify] [--allow-empty-current] [--repo-root PATH] [TASK-NNNN-Name]\n\n"+
				"Atomically promotes a TASK from docs/tasks/ to docs/tasks/done/.\n"+
				"Without an explicit TASK name the Current TASK is promoted.\n"+
				"Validation is shared with run-workflow-audit via the donecheck library.\n")
	}
	flag.Parse()
	root, err := resolveCommandRoot(*repoRoot)
	if err != nil {
		fail("resolve repository root: %v", err)
	}
	schemaPath := filepath.Join(root, filepath.FromSlash(schemaRel))
	if err := validateRepoPath(root, schemaPath, false); err != nil {
		fail("validate workflow schema path: %v", err)
	}
	schema, schemaErr := donecheck.LoadSchema(schemaPath)
	if schemaErr != nil {
		fail("load workflow schema: %v", schemaErr)
	}
	loadedSchema = schema
	opts := options{
		dryRun:            *dryRun,
		verify:            *verify,
		allowEmptyCurrent: *allowEmpty,
		taskName:          flag.Arg(0),
	}
	if err := runWithLock(root, opts); err != nil {
		fail("%v", err)
	}
}

func resolveCommandRoot(explicit string) (string, error) {
	var root string
	var err error
	if explicit == "" {
		root, err = os.Getwd()
	} else {
		root, err = filepath.Abs(explicit)
	}
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}

func runWithLock(root string, opts options) (runErr error) {
	if opts.dryRun {
		return promote(root, opts)
	}
	lockPath := filepath.Join(root, filepath.FromSlash(lockRel))
	lockDir := filepath.Dir(lockPath)
	if err := validateRepoPath(root, lockDir, true); err != nil {
		return fmt.Errorf("validate lock directory path: %w", err)
	}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	if err := validateRepoPath(root, lockDir, false); err != nil {
		return fmt.Errorf("validate lock directory: %w", err)
	}
	if err := validateRepoPath(root, lockPath, true); err != nil {
		return fmt.Errorf("validate lock path: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	unlock, err := tryPromoteLock(lockFile)
	if err != nil {
		return errors.Join(fmt.Errorf("another promote-task-done holds %s; refusing to race", lockRel), lockFile.Close())
	}
	defer func() {
		runErr = errors.Join(runErr, unlock(), lockFile.Close())
	}()
	return promote(root, opts)
}

func promote(root string, opts options) error {
	tasksPath := filepath.Join(root, filepath.FromSlash(tasksRel))
	if err := validateRepoPath(root, tasksPath, false); err != nil {
		return err
	}
	content, err := readRegularFile(tasksPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", tasksRel, err)
	}
	index, err := parseTasks(string(content))
	if err != nil {
		return err
	}
	taskName := opts.taskName
	if taskName == "" {
		taskName = index.currentName
	}
	if taskName == "" {
		return fmt.Errorf("no TASK name given and tasks.md has no Current header")
	}
	if opts.taskName != "" && opts.taskName != index.currentName {
		return fmt.Errorf("TASK %s is not the Current TASK (Current=%s); promote-task-done only handles the active TASK to keep state transitions atomic", opts.taskName, index.currentName)
	}
	row, ok := findRow(index, taskName)
	if !ok {
		return fmt.Errorf("tasks.md has no row for %s", taskName)
	}
	if row.icon != openIcon {
		return fmt.Errorf("tasks.md row for %s is already checked", taskName)
	}
	if !strings.HasPrefix(row.target, "tasks/") || strings.HasPrefix(row.target, "tasks/done/") {
		return fmt.Errorf("tasks.md row for %s must point to tasks/<name>.md before promotion, got %s", taskName, row.target)
	}
	srcRel := filepath.ToSlash(filepath.Join("docs", row.target))
	dstRel := filepath.ToSlash(filepath.Join("docs", "tasks", "done", filepath.Base(row.target)))
	srcAbs := filepath.Join(root, filepath.FromSlash(srcRel))
	dstAbs := filepath.Join(root, filepath.FromSlash(dstRel))
	if err := validateRepoPath(root, srcAbs, false); err != nil {
		return err
	}
	if err := validateRepoPath(root, dstAbs, true); err != nil {
		return err
	}
	if !exists(srcAbs) {
		return fmt.Errorf("detail file %s does not exist", srcRel)
	}
	if exists(dstAbs) {
		return fmt.Errorf("destination %s already exists; resolve the duplicate before promoting", dstRel)
	}
	detailBytes, err := readRegularFile(srcAbs)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcRel, err)
	}
	if errs := donecheck.CheckDonePromotion(string(detailBytes), taskName, loadedSchema); len(errs) > 0 {
		return fmt.Errorf("%s is not promotable:\n  - %s", srcRel, strings.Join(errs, "\n  - "))
	}
	doneSet := collectDoneSet(index.rows)
	doneSet[taskName] = true
	nextName, nextRow, nextInfo, err := pickNextExecutable(index, root, doneSet, taskName)
	if err != nil {
		return err
	}
	if nextName == "" && !opts.allowEmptyCurrent {
		return fmt.Errorf("no next executable [ ] TASK after promoting %s; refusing to leave Current pointing at a checked row. Either:\n  - add a new TASK row + detail file, or\n  - report zero-finding Terminal Gate status from workflow-complete-loop.md, or\n  - re-run with --allow-empty-current to accept the resulting audit failure", taskName)
	}
	newTasks := computeTasksMd(index, taskName, nextName)
	plan := buildPlan(srcRel, dstRel, taskName, nextName, nextInfo.state)
	if opts.dryRun {
		fmt.Print(plan)
		return nil
	}
	rollback, err := applyChanges(root, tasksPath, content, newTasks, srcAbs, detailBytes, dstAbs, nextName, nextRow)
	if err != nil {
		return err
	}
	if opts.verify {
		if vErr := runAuditTaskState(root); vErr != nil {
			rollbackErr := rollback()
			if rollbackErr != nil {
				return errors.Join(fmt.Errorf("post-mutation verify failed: %w", vErr), fmt.Errorf("rollback failed: %w", rollbackErr))
			}
			return fmt.Errorf("post-mutation verify failed; rolled back: %w", vErr)
		}
	}
	fmt.Print(plan)
	fmt.Printf("promoted %s -> %s\n", srcRel, dstRel)
	if nextName == "" {
		fmt.Println("no executable [ ] TASK remains; report zero-finding Terminal Gate status from workflow-complete-loop.md instead of leaving an empty board")
	} else {
		fmt.Printf("Current advanced to %s\n", nextName)
	}
	if opts.verify {
		fmt.Println("verify: run-workflow-audit task-state passed")
	}
	return nil
}

func parseTasks(content string) (taskIndex, error) {
	var index taskIndex
	index.rawLines = strings.Split(content, "\n")
	seenCurrent := false
	for i, line := range index.rawLines {
		lineNo := i + 1
		if strings.HasPrefix(line, currentPrefix) {
			if seenCurrent {
				return index, fmt.Errorf("tasks.md line %d: duplicate Current header", lineNo)
			}
			seenCurrent = true
			match := currentRe.FindStringSubmatch(line)
			if match == nil {
				return index, fmt.Errorf("tasks.md line %d: invalid Current header", lineNo)
			}
			index.currentName = match[1]
			index.currentTarget = match[2]
			index.currentLine = lineNo
			continue
		}
		match := rowRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		index.rows = append(index.rows, taskRow{icon: match[1], name: match[2], description: match[3], target: match[4], line: lineNo})
	}
	if !seenCurrent {
		return index, fmt.Errorf("tasks.md missing Current header")
	}
	if len(index.rows) == 0 {
		return index, fmt.Errorf("tasks.md has no TASK rows")
	}
	return index, nil
}

func findRow(index taskIndex, name string) (taskRow, bool) {
	for _, row := range index.rows {
		if row.name == name {
			return row, true
		}
	}
	return taskRow{}, false
}

func collectDoneSet(rows []taskRow) map[string]bool {
	done := map[string]bool{}
	for _, row := range rows {
		if row.icon == doneIcon {
			done[row.name] = true
		}
	}
	return done
}

// pickNextExecutable returns the first non-done, non-promoting TASK whose
// detail State is Active|Queued and whose dependencies are all in doneSet.
// On a hit it returns (name, row, info). On a miss it returns ("", taskRow{},
// detailInfo{}) -- callers gate on the empty name to avoid relying on the
// info value, so no pointer is exposed and no nil-deref class exists.
func pickNextExecutable(index taskIndex, root string, doneSet map[string]bool, promoting string) (string, taskRow, detailInfo, error) {
	for _, row := range index.rows {
		if row.icon == doneIcon || row.name == promoting {
			continue
		}
		info, err := readDetailInfo(root, row.target)
		if err != nil {
			return "", taskRow{}, detailInfo{}, fmt.Errorf("read candidate TASK %s: %w", row.name, err)
		}
		if info.state != stateActive && info.state != stateQueued {
			continue
		}
		ready := true
		for _, dep := range info.dependencies {
			if !doneSet[dep] {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		return row.name, row, info, nil
	}
	return "", taskRow{}, detailInfo{}, nil
}

func readDetailInfo(root string, target string) (detailInfo, error) {
	path := filepath.Join(root, "docs", filepath.FromSlash(target))
	if err := validateRepoPath(root, path, false); err != nil {
		return detailInfo{}, err
	}
	bytes, err := readRegularFile(path)
	if err != nil {
		return detailInfo{}, err
	}
	content := string(bytes)
	state := donecheck.ParseState(content)
	deps := parseDependencies(donecheck.ExtractSection(content, headerScheduled))
	return detailInfo{state: state, dependencies: deps}, nil
}

func parseDependencies(section string) []string {
	fields := donecheck.ParseBulletFields(section)
	raw := strings.TrimSpace(fields[dependsOnLabel])
	if raw == "" || raw == "none" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func computeTasksMd(index taskIndex, promoting string, nextName string) string {
	lines := append([]string(nil), index.rawLines...)
	for _, row := range index.rows {
		if row.name != promoting {
			continue
		}
		newTarget := "tasks/done/" + filepath.Base(row.target)
		lines[row.line-1] = fmt.Sprintf("- [x] %s - %s -> %s", row.name, row.description, newTarget)
	}
	if nextName != "" {
		var nextRow taskRow
		for _, row := range index.rows {
			if row.name == nextName {
				nextRow = row
				break
			}
		}
		lines[index.currentLine-1] = fmt.Sprintf("Current: %s -> %s", nextRow.name, nextRow.target)
	}
	return strings.Join(lines, "\n")
}

func mutateNextDetail(content string) (string, error) {
	lines := strings.Split(content, "\n")
	statusStart := -1
	statusEnd := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == headerStatus {
			statusStart = i + 1
			continue
		}
		if statusStart >= 0 && i > statusStart && strings.HasPrefix(line, "## ") {
			statusEnd = i
			break
		}
	}
	if statusStart < 0 {
		return "", fmt.Errorf("next detail file missing %s", headerStatus)
	}
	stateChanged := false
	for i := statusStart; i < statusEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, stateLine) {
			lines[i] = stateLine + stateActive
			stateChanged = true
			break
		}
	}
	if !stateChanged {
		return "", fmt.Errorf("next detail file missing 'State:' line")
	}
	subStart := -1
	subEnd := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == headerSubTasks {
			subStart = i + 1
			continue
		}
		if subStart >= 0 && i > subStart && strings.HasPrefix(line, "## ") {
			subEnd = i
			break
		}
	}
	if subStart < 0 {
		return "", fmt.Errorf("next detail file missing %s", headerSubTasks)
	}
	for i := subStart; i < subEnd; i++ {
		if strings.HasPrefix(lines[i], subTaskActive) {
			return strings.Join(lines, "\n"), nil
		}
	}
	for i := subStart; i < subEnd; i++ {
		if strings.HasPrefix(lines[i], subTaskOpen) {
			lines[i] = subTaskActive + strings.TrimPrefix(lines[i], subTaskOpen)
			return strings.Join(lines, "\n"), nil
		}
	}
	return "", fmt.Errorf("next detail file has no [ ] Sub-Task to mark active")
}

type rollbackFunc func() error

func applyChanges(root string, tasksPath string, expectedTasks []byte, newTasks string, srcAbs string, expectedDetail []byte, dstAbs string, nextName string, nextRow taskRow) (rollbackFunc, error) {
	var nextPath string
	var newNextContent string
	var origNextContent []byte
	if nextName != "" {
		nextPath = filepath.Join(root, "docs", filepath.FromSlash(nextRow.target))
		if err := validateRepoPath(root, nextPath, false); err != nil {
			return nil, fmt.Errorf("validate next detail %s: %w", nextRow.target, err)
		}
		original, err := readRegularFile(nextPath)
		if err != nil {
			return nil, fmt.Errorf("read next detail %s: %w", nextRow.target, err)
		}
		origNextContent = original
		mutated, err := mutateNextDetail(string(original))
		if err != nil {
			return nil, fmt.Errorf("mutate next detail %s: %w", nextRow.target, err)
		}
		newNextContent = mutated
	}
	origTasks, err := readRegularFile(tasksPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", tasksRel, err)
	}
	if !bytes.Equal(origTasks, expectedTasks) {
		return nil, fmt.Errorf("%s changed after planning; retry", tasksRel)
	}
	currentDetail, err := readRegularFile(srcAbs)
	if err != nil {
		return nil, fmt.Errorf("re-read promoted detail: %w", err)
	}
	if !bytes.Equal(currentDetail, expectedDetail) {
		return nil, fmt.Errorf("promoted detail changed after planning; retry")
	}
	if exists(dstAbs) {
		return nil, fmt.Errorf("destination appeared after planning; retry")
	}
	if err := writeAtomic(root, tasksPath, []byte(newTasks), origTasks); err != nil {
		return nil, fmt.Errorf("write %s: %w", tasksRel, err)
	}
	if err := moveNoClobber(srcAbs, dstAbs, expectedDetail); err != nil {
		rollbackErr := writeAtomic(root, tasksPath, origTasks, []byte(newTasks))
		return nil, errors.Join(fmt.Errorf("move detail file: %w", err), wrapRollbackError(rollbackErr))
	}
	if nextName != "" {
		if err := writeAtomic(root, nextPath, []byte(newNextContent), origNextContent); err != nil {
			rollbackErr := errors.Join(moveNoClobber(dstAbs, srcAbs, expectedDetail), writeAtomic(root, tasksPath, origTasks, []byte(newTasks)))
			return nil, errors.Join(fmt.Errorf("write next detail %s: %w", nextRow.target, err), wrapRollbackError(rollbackErr))
		}
	}
	rollback := func() error {
		var rollbackErr error
		if nextName != "" {
			rollbackErr = errors.Join(rollbackErr, writeAtomic(root, nextPath, origNextContent, []byte(newNextContent)))
		}
		rollbackErr = errors.Join(rollbackErr, moveNoClobber(dstAbs, srcAbs, expectedDetail), writeAtomic(root, tasksPath, origTasks, []byte(newTasks)))
		return rollbackErr
	}
	return rollback, nil
}

func wrapRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback failed: %w", err)
}

func writeAtomic(root string, path string, content, expected []byte) error {
	if err := validateRepoPath(root, path, false); err != nil {
		return err
	}
	beforeBody, err := readRegularFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(beforeBody, expected) {
		return fmt.Errorf("atomic target differs from the expected transaction image: %s", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".promote-task-done-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	mode := os.FileMode(0o644)
	if info, statErr := os.Lstat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return statErr
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		return errors.Join(err, tmp.Close(), os.Remove(tmpName))
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(err, tmp.Close(), os.Remove(tmpName))
	}
	if err := tmp.Close(); err != nil {
		return errors.Join(err, os.Remove(tmpName))
	}
	if err := validateRepoPath(root, path, false); err != nil {
		return errors.Join(err, os.Remove(tmpName))
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return errors.Join(fmt.Errorf("atomic target must remain a non-symlink regular file: %s", path), err, os.Remove(tmpName))
	}
	currentBody, err := readRegularFile(path)
	if err != nil || !bytes.Equal(beforeBody, currentBody) {
		return errors.Join(fmt.Errorf("atomic target changed before publication: %s", path), err, os.Remove(tmpName))
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errors.Join(err, os.Remove(tmpName))
	}
	return nil
}

func moveNoClobber(source, destination string, expected []byte) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("move source must be a non-symlink regular file: %s", source)
	}
	sourceBody, err := readRegularFile(source)
	if err != nil {
		return fmt.Errorf("read move source: %w", err)
	}
	if !bytes.Equal(sourceBody, expected) {
		return fmt.Errorf("move source differs from the expected transaction image: %s", source)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("move destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(source, destination); err != nil {
		return err
	}
	destinationInfo, err := os.Lstat(destination)
	if err != nil || destinationInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(sourceInfo, destinationInfo) {
		return errors.Join(fmt.Errorf("move destination identity mismatch: %s", destination), err, os.Remove(destination))
	}
	destinationBody, err := readRegularFile(destination)
	if err != nil || !bytes.Equal(destinationBody, expected) {
		return errors.Join(fmt.Errorf("move destination content mismatch: %s", destination), err, os.Remove(destination))
	}
	if err := os.Remove(source); err != nil {
		return errors.Join(err, os.Remove(destination))
	}
	return nil
}

func buildPlan(srcRel string, dstRel string, promoting string, nextName string, nextState string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "promote-task-done plan:\n")
	fmt.Fprintf(&b, "  move:    %s -> %s\n", srcRel, dstRel)
	fmt.Fprintf(&b, "  row:     [ ] %s -> [x] %s (target tasks/ -> tasks/done/)\n", promoting, promoting)
	if nextName == "" {
		fmt.Fprintf(&b, "  next:    none executable; zero-finding Terminal Gate path\n")
	} else {
		fmt.Fprintf(&b, "  current: -> %s\n", nextName)
		fmt.Fprintf(&b, "  state:   %s State:%s -> Active, first [ ] sub-task -> [~]\n", nextName, nextState)
	}
	return b.String()
}

func runAuditTaskState(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	cmd := auditTaskStateCommand(ctx, filepath.Join(root, filepath.FromSlash(auditRunner)))
	cmd.Dir = root
	cmd.WaitDelay = 2 * time.Second
	var stdout boundedCommandOutput
	var stderr boundedCommandOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("audit task-state timed out after %s", auditTimeout)
	}
	if stdout.truncated || stderr.truncated {
		return fmt.Errorf("audit task-state output exceeded %d bytes per stream", maxAuditOutput)
	}
	if err != nil {
		trimmed := strings.TrimSpace(stdout.String() + stderr.String())
		if trimmed == "" {
			return fmt.Errorf("audit task-state failed: %w", err)
		}
		return fmt.Errorf("audit task-state failed:\n%s", trimmed)
	}
	return nil
}

type boundedCommandOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (output *boundedCommandOutput) Write(value []byte) (int, error) {
	remaining := maxAuditOutput - output.buffer.Len()
	if remaining > len(value) {
		remaining = len(value)
	}
	if remaining > 0 {
		_, _ = output.buffer.Write(value[:remaining])
	}
	if remaining < len(value) {
		output.truncated = true
	}
	return len(value), nil
}

func (output *boundedCommandOutput) String() string {
	return output.buffer.String()
}

func readRegularFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("must be a non-symlink regular file")
	}
	if before.Size() > maxInputBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxInputBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxInputBytes+1))
	afterFile, statErr := file.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, lstatErr, closeErr); err != nil {
		return nil, err
	}
	if len(body) > maxInputBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxInputBytes)
	}
	if !os.SameFile(before, afterFile) || !os.SameFile(afterFile, afterPath) ||
		before.Mode() != afterFile.Mode() || before.Size() != afterFile.Size() ||
		!before.ModTime().Equal(afterFile.ModTime()) {
		return nil, fmt.Errorf("changed while reading")
	}
	return body, nil
}

func validateRepoPath(root, path string, allowMissingLeaf bool) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s escapes repository root", path)
	}
	current := filepath.Clean(root)
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && allowMissingLeaf && index == len(components)-1 {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("repository path component must not be a symlink: %s", current)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("repository path component must be a directory: %s", current)
		}
		if index == len(components)-1 && !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("repository path leaf must be regular: %s", current)
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "promote-task-done: "+format+"\n", args...)
	os.Exit(2)
}
