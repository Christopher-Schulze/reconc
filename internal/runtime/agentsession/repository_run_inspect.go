package agentsession

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/jsonl"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/repositorycontrol"
	"reconc.dev/reconc/internal/tasklifecycle"
)

// RepositoryRunStatus is the read-only snapshot rendered by `reconc run status`.
type RepositoryRunStatus struct {
	Enabled              bool   `json:"enabled"`
	DisabledReason       string `json:"disabled_reason,omitempty"`
	AwaitingContinuation bool   `json:"awaiting_continuation"`
	NoProgressNudges     int    `json:"no_progress_nudges"`
	TaskDisposition      string `json:"task_disposition"`
	TaskID               string `json:"task_id,omitempty"`
	CurrentSubTask       string `json:"current_sub_task,omitempty"`
	Blocker              string `json:"blocker,omitempty"`
	OpenTasks            int    `json:"open_tasks"`
}

// RunDecisionLogPath returns the repo-local decisions.jsonl path used by
// the repository run observability commands.
func RunDecisionLogPath(repoRoot string) (string, error) {
	return runDecisionLogPath(repoRoot)
}

// ReadRepositoryRunStatus loads repository run state for display. A missing
// state file is not an error: it returns a zero (disabled) snapshot.
func ReadRepositoryRunStatus(repoRoot string) (RepositoryRunStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	return readRepositoryRunStatusResolved(root)
}

func readRepositoryRunStatusResolved(root string) (RepositoryRunStatus, error) {
	state, err := loadRepositoryRunStateResolved(root)
	if err != nil {
		return RepositoryRunStatus{}, err
	}
	taskState, err := tasklifecycle.InspectRunStateResolved(root)
	if err != nil {
		taskState = tasklifecycle.RunState{
			Disposition: tasklifecycle.RunInvalid,
			Blocker:     truncateBytes(err.Error(), 512),
		}
	}
	return RepositoryRunStatus{
		Enabled:              state.Enabled,
		DisabledReason:       state.DisabledReason.String(),
		AwaitingContinuation: state.AwaitingContinuation,
		NoProgressNudges:     state.NoProgressNudges,
		TaskDisposition:      string(taskState.Disposition),
		TaskID:               taskState.TaskID,
		CurrentSubTask:       taskState.SubTask,
		Blocker:              taskState.Blocker,
		OpenTasks:            taskState.OpenTasks,
	}, nil
}

// ReadRunDecisions returns repository run decision records from
// .reconc/run/decisions.jsonl in chronological (append) order. When
// limit > 0 only the last limit records are returned. A missing log is not an
// error (returns nil). The JSONL writer lock is held while taking the snapshot,
// and malformed or truncated records fail closed rather than disappearing.
func ReadRunDecisions(repoRoot string, limit int) ([]RunDecision, error) {
	if limit < 0 {
		return nil, fmt.Errorf("run decision limit must be non-negative")
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	path := runDecisionLogPathResolved(root)
	if err := validateRepositoryRunStatePath(root, path); err != nil {
		return nil, err
	}
	collector := runDecisionCollector{limit: limit}
	err = withRunDecisionLogLock(path, func() error {
		sources, sourcesErr := jsonl.PathsOldestFirst(path, runDecisionMaxArchives)
		if sourcesErr != nil {
			return sourcesErr
		}
		for _, source := range sources {
			if readErr := readRunDecisionFile(source, collector.add); readErr != nil {
				return readErr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return collector.values(), nil
}

// RunDecisionFollower owns a lock-consistent cursor over the bounded decision
// ring. Poll reuses unchanged members and decodes only appended suffixes or
// members whose identity or metadata changed.
type RunDecisionFollower struct {
	path    string
	members []runDecisionLogMember
	cursor  runDecisionOccurrence
}

// NewRunDecisionFollower snapshots the complete bounded decision ring and
// returns its current decisions together with an occurrence-bound follower.
func NewRunDecisionFollower(repoRoot string) (*RunDecisionFollower, []RunDecision, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	path := runDecisionLogPathResolved(root)
	if err := validateRepositoryRunStatePath(root, path); err != nil {
		return nil, nil, err
	}
	follower := &RunDecisionFollower{path: path}
	members, err := follower.snapshot(nil)
	if err != nil {
		return nil, nil, err
	}
	follower.members = members
	follower.cursor = lastRunDecisionOccurrence(members)
	return follower, runDecisionValues(members), nil
}

// Poll returns decisions appended after the last successful snapshot. If the
// exact cursor occurrence left the bounded ring, Poll fails closed.
func (follower *RunDecisionFollower) Poll() ([]RunDecision, error) {
	if follower == nil || follower.path == "" {
		return nil, errors.New("run decision follower is not initialized")
	}
	members, err := follower.snapshot(follower.members)
	if err != nil {
		return nil, err
	}
	values, err := runDecisionValuesAfter(members, follower.cursor)
	if err != nil {
		return nil, err
	}
	follower.members = members
	follower.cursor = lastRunDecisionOccurrence(members)
	return values, nil
}

func (follower *RunDecisionFollower) snapshot(previous []runDecisionLogMember) ([]runDecisionLogMember, error) {
	var members []runDecisionLogMember
	err := withRunDecisionLogLock(follower.path, func() error {
		sources, sourcesErr := jsonl.PathsOldestFirst(follower.path, runDecisionMaxArchives)
		if sourcesErr != nil {
			return sourcesErr
		}
		members = make([]runDecisionLogMember, 0, len(sources))
		for _, source := range sources {
			member, readErr := readRunDecisionMember(source, previous)
			if readErr != nil {
				return readErr
			}
			for _, existing := range members {
				if os.SameFile(existing.info, member.info) {
					return fmt.Errorf("repository run log ring contains duplicate file identity: %s", source)
				}
			}
			members = append(members, member)
		}
		return nil
	})
	return members, err
}

func withRunDecisionLogLock(path string, use func() error) error {
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return fmt.Errorf("repository run log parent must be a non-symlink directory")
	}
	if err := repositorycontrol.ValidateRunDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lockInfo, lstatErr := os.Lstat(lockPath)
	if lstatErr == nil && (lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.Mode().IsRegular()) {
		return fmt.Errorf("repository run log lock must be a non-symlink regular file")
	} else if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return lstatErr
	}
	var lock *os.File
	if errors.Is(lstatErr, os.ErrNotExist) {
		lock, err = privatefs.OpenLock(lockPath)
	} else {
		lock, err = os.OpenFile(lockPath, os.O_RDWR, 0)
		if err == nil {
			opened, statErr := lock.Stat()
			current, currentErr := os.Lstat(lockPath)
			if statErr != nil || currentErr != nil || !os.SameFile(lockInfo, opened) ||
				!os.SameFile(opened, current) {
				err = errors.Join(fmt.Errorf("repository run log lock changed identity while opening"), statErr, currentErr)
			} else {
				err = privatefs.ValidateFile(lock, opened)
			}
		}
	}
	if err != nil {
		if lock != nil {
			err = errors.Join(err, lock.Close())
		}
		return err
	}
	unlock, err := filelock.LockContext(context.Background(), lock, agentSessionLockTimeout)
	if err != nil {
		return errors.Join(err, lock.Close())
	}
	err = use()
	unlockErr := unlock()
	if err != nil {
		return errors.Join(err, unlockErr, lock.Close())
	}
	if unlockErr != nil {
		return errors.Join(fmt.Errorf("unlock run decision log: %w", unlockErr), lock.Close())
	}
	return lock.Close()
}

const runDecisionMaxRecordBytes = 32 * 1024

type runDecisionCollector struct {
	limit   int
	items   []RunDecision
	next    int
	wrapped bool
}

func (collector *runDecisionCollector) add(decision RunDecision) {
	if collector.limit == 0 || len(collector.items) < collector.limit {
		collector.items = append(collector.items, decision)
		return
	}
	collector.items[collector.next] = decision
	collector.next = (collector.next + 1) % collector.limit
	collector.wrapped = true
}

func (collector *runDecisionCollector) values() []RunDecision {
	if !collector.wrapped || collector.next == 0 {
		return collector.items
	}
	out := make([]RunDecision, 0, len(collector.items))
	out = append(out, collector.items[collector.next:]...)
	out = append(out, collector.items[:collector.next]...)
	return out
}

func readRunDecisionFile(path string, emit func(RunDecision)) error {
	member, err := readRunDecisionMember(path, nil)
	if err != nil {
		return err
	}
	for _, record := range member.records {
		emit(record.decision)
	}
	return nil
}

type runDecisionLogMember struct {
	info      os.FileInfo
	records   []runDecisionRecord
	lineCount int
}

type runDecisionRecord struct {
	decision RunDecision
	file     os.FileInfo
	start    int64
	end      int64
	digest   [sha256.Size]byte
}

type runDecisionOccurrence struct {
	file   os.FileInfo
	start  int64
	end    int64
	digest [sha256.Size]byte
	set    bool
}

func readRunDecisionMember(path string, previous []runDecisionLogMember) (runDecisionLogMember, error) {
	var member runDecisionLogMember
	err := boundedio.WithRegularFileSnapshot(path, runDecisionMaxBytes, func(file *os.File, info os.FileInfo) error {
		if err := privatefs.ValidateFileAllowLinks(file, info); err != nil {
			return fmt.Errorf("validate repository run decision security: %w", err)
		}
		member.info = info
		prior := matchingRunDecisionMember(previous, info)
		if prior != nil && sameRunDecisionMetadata(prior.info, info) {
			if err := validateRunDecisionPrefix(file, *prior); err != nil {
				return err
			}
			member.records = prior.records
			member.lineCount = prior.lineCount
			return nil
		}
		start, lines := int64(0), 0
		if prior != nil && prior.info.Mode() == info.Mode() && prior.info.Size() < info.Size() {
			if err := validateRunDecisionPrefix(file, *prior); err != nil {
				return err
			}
			start, lines = prior.info.Size(), prior.lineCount
			member.records = append(member.records, prior.records...)
		}
		records, lineCount, err := scanRunDecisionRecords(file, path, info, start, lines)
		if err != nil {
			return err
		}
		member.records = append(member.records, records...)
		member.lineCount = lineCount
		return nil
	})
	return member, err
}

func matchingRunDecisionMember(members []runDecisionLogMember, info os.FileInfo) *runDecisionLogMember {
	for index := range members {
		if os.SameFile(members[index].info, info) {
			return &members[index]
		}
	}
	return nil
}

func sameRunDecisionMetadata(left, right os.FileInfo) bool {
	return os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func validateRunDecisionPrefix(file *os.File, previous runDecisionLogMember) error {
	if previous.info.Size() == 0 {
		return nil
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, previous.info.Size()-1); err != nil {
		return err
	}
	if last[0] != '\n' {
		return errors.New("run decision append does not start at a record boundary")
	}
	if len(previous.records) == 0 {
		return nil
	}
	record := previous.records[len(previous.records)-1]
	body := make([]byte, record.end-record.start-1)
	if _, err := file.ReadAt(body, record.start); err != nil {
		return err
	}
	if sha256.Sum256(body) != record.digest {
		return errors.New("run decision log prefix changed before append")
	}
	return nil
}

func scanRunDecisionRecords(file *os.File, path string, info os.FileInfo, start int64, lineBase int) ([]runDecisionRecord, int, error) {
	if info.Size() == 0 {
		return nil, lineBase, nil
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return nil, lineBase, err
	}
	if last[0] != '\n' {
		return nil, lineBase, fmt.Errorf("%s: truncated JSONL record: missing final newline", path)
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, lineBase, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), runDecisionMaxRecordBytes)
	records := make([]runDecisionRecord, 0)
	offset, lineNumber := start, lineBase
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		nextOffset := offset + int64(len(line)) + 1
		if len(bytes.TrimSpace(line)) > 0 {
			decision, err := decodeRunDecisionLine(line)
			if err != nil {
				return nil, lineBase, fmt.Errorf("%s:%d: malformed run decision: %w", path, lineNumber, err)
			}
			records = append(records, runDecisionRecord{
				decision: decision,
				file:     info,
				start:    offset,
				end:      nextOffset,
				digest:   sha256.Sum256(line),
			})
		}
		offset = nextOffset
	}
	if err := scanner.Err(); err != nil {
		return nil, lineBase, fmt.Errorf("%s: read run decisions: %w", path, err)
	}
	if offset != info.Size() {
		return nil, lineBase, fmt.Errorf("%s changed while reading", path)
	}
	return records, lineNumber, nil
}

func runDecisionValues(members []runDecisionLogMember) []RunDecision {
	total := 0
	for _, member := range members {
		total += len(member.records)
	}
	values := make([]RunDecision, 0, total)
	for _, member := range members {
		for _, record := range member.records {
			values = append(values, record.decision)
		}
	}
	return values
}

func lastRunDecisionOccurrence(members []runDecisionLogMember) runDecisionOccurrence {
	for memberIndex := len(members) - 1; memberIndex >= 0; memberIndex-- {
		records := members[memberIndex].records
		if len(records) == 0 {
			continue
		}
		record := records[len(records)-1]
		return runDecisionOccurrence{file: record.file, start: record.start, end: record.end, digest: record.digest, set: true}
	}
	return runDecisionOccurrence{}
}

func runDecisionValuesAfter(members []runDecisionLogMember, cursor runDecisionOccurrence) ([]RunDecision, error) {
	if !cursor.set {
		return runDecisionValues(members), nil
	}
	found := false
	var values []RunDecision
	for _, member := range members {
		for _, record := range member.records {
			if found {
				values = append(values, record.decision)
				continue
			}
			if os.SameFile(record.file, cursor.file) && record.start == cursor.start &&
				record.end == cursor.end && record.digest == cursor.digest {
				found = true
			}
		}
	}
	if !found {
		return nil, errors.New("follow cursor left the bounded decision-log window; restart `reconc run log --follow`")
	}
	return values, nil
}

func decodeRunDecisionLine(line []byte) (RunDecision, error) {
	var decision RunDecision
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return RunDecision{}, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return RunDecision{}, err
	}
	return decision, nil
}
