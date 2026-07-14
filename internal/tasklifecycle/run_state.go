package tasklifecycle

import "strings"

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
	board, err := Inspect(repoRoot)
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
