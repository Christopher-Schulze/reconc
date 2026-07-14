package tasklifecycle

import "fmt"

// State is the normalized state shared by both supported repository formats.
type State string

const (
	StateQueued  State = "queued"
	StateActive  State = "active"
	StateBlocked State = "blocked"
	StatePaused  State = "paused"
	StateDone    State = "done"
)

// SubTask is one checklist item from a TASK detail file.
type SubTask struct {
	State State  `json:"state"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

// Task is the normalized, typed view of one overview row plus its detail.
type Task struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Title          string            `json:"title"`
	State          State             `json:"state"`
	Path           string            `json:"path"`
	OverviewLine   int               `json:"overview_line"`
	SubTasks       []SubTask         `json:"sub_tasks,omitempty"`
	Blocker        string            `json:"blocker,omitempty"`
	Dependencies   []string          `json:"dependencies,omitempty"`
	EvidenceFields map[string]string `json:"evidence_fields,omitempty"`

	rawDetail []byte
}

// Board is the complete live control plane. Archived TASK details that are no
// longer linked from the overview are intentionally not parsed.
type Board struct {
	RepoRoot string  `json:"repo_root"`
	Config   Config  `json:"config"`
	Profile  Profile `json:"profile"`
	Active   *Task   `json:"active,omitempty"`
	Queue    []*Task `json:"queue"`
	Blocked  []*Task `json:"blocked"`
	Done     []*Task `json:"done"`

	overviewBytes []byte
	overviewLines []string
	sectionLines  map[State]int
	tasksByID     map[string]*Task
	tasksByName   map[string]*Task
	tasksByPath   map[string]*Task
	doneIDs       map[string]bool
	doneCount     int
	doneTargetDir string
}

// Issue is a stable, machine-addressable lifecycle validation failure.
type Issue struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Line        int    `json:"line,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
}

func (issue Issue) Error() string {
	location := issue.Path
	if issue.Line > 0 {
		location = fmt.Sprintf("%s:%d", issue.Path, issue.Line)
	}
	return fmt.Sprintf("%s [%s]: %s; %s", location, issue.ID, issue.Message, issue.Remediation)
}

// ValidationError preserves every independent finding instead of forcing an
// agent through one-error-per-turn repair loops.
type ValidationError struct {
	Issues []Issue `json:"issues"`
}

func (err *ValidationError) Error() string {
	if err == nil || len(err.Issues) == 0 {
		return "task lifecycle validation failed"
	}
	if len(err.Issues) == 1 {
		return err.Issues[0].Error()
	}
	return fmt.Sprintf("task lifecycle validation failed with %d issues (first: %s)", len(err.Issues), err.Issues[0].Error())
}

func (board *Board) allTasks() []*Task {
	count := len(board.Queue) + len(board.Blocked) + len(board.Done)
	if board.Active != nil {
		count++
	}
	out := make([]*Task, 0, count)
	if board.Active != nil {
		out = append(out, board.Active)
	}
	out = append(out, board.Queue...)
	out = append(out, board.Blocked...)
	out = append(out, board.Done...)
	return out
}

func (board *Board) findTask(id string) (*Task, bool) {
	if task, ok := board.tasksByID[id]; ok {
		return task, task != nil
	}
	task, ok := board.tasksByName[id]
	return task, ok && task != nil
}
