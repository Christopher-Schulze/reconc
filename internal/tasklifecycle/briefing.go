package tasklifecycle

import (
	"fmt"
	"strings"
)

const (
	maxBriefingBlockers  = 5
	maxBriefingEvidence  = 6
	maxBriefingTextRunes = 240
)

// Briefing is the compact TASK portion of session-briefing. Machine identity
// fields remain exact; paired display fields are bounded for text rendering.
// It never includes archive history, completed checklists, notes, or full
// acceptance prose.
type Briefing struct {
	Profile                 Profile           `json:"profile"`
	Current                 *BriefingTask     `json:"current,omitempty"`
	Blockers                []BriefingBlocker `json:"blockers,omitempty"`
	OmittedBlockers         int               `json:"omitted_blockers,omitempty"`
	RequiredEvidence        []string          `json:"required_evidence,omitempty"`
	RequiredEvidenceDisplay []string          `json:"required_evidence_display,omitempty"`
	OmittedEvidence         int               `json:"omitted_evidence,omitempty"`
	Remediation             string            `json:"remediation,omitempty"`
}

type BriefingTask struct {
	ID             string `json:"id"`
	DisplayID      string `json:"display_id,omitempty"`
	Title          string `json:"title"`
	Path           string `json:"path"`
	DisplayPath    string `json:"display_path,omitempty"`
	CurrentSubTask string `json:"current_sub_task,omitempty"`
}

type BriefingBlocker struct {
	ID            string `json:"id"`
	DisplayID     string `json:"display_id,omitempty"`
	Reason        string `json:"reason"`
	DisplayReason string `json:"display_reason,omitempty"`
}

// BuildBriefing produces a fixed-shape, archive-independent view for an AI
// session handoff.
func BuildBriefing(board *Board) Briefing {
	return buildBriefing(board, RunStateFromBoard(board))
}

// BuildBriefingWithRunState produces the same view while reusing a run
// decision already derived from the same validated board snapshot.
func BuildBriefingWithRunState(board *Board, state RunState) Briefing {
	return buildBriefing(board, state)
}

func buildBriefing(board *Board, state RunState) Briefing {
	briefing := Briefing{Profile: board.Profile}
	if board.Active != nil {
		briefing.Current = &BriefingTask{
			ID: board.Active.ID, DisplayID: truncateBriefing(board.Active.ID),
			Title: truncateBriefing(board.Active.Title),
			Path:  board.Active.Path, DisplayPath: truncateBriefing(board.Active.Path),
			CurrentSubTask: currentSubTask(board.Active),
		}
		for _, field := range board.Config.Completion.RequiredEvidenceFields {
			if strings.TrimSpace(board.Active.EvidenceFields[field]) == "" {
				if len(briefing.RequiredEvidence) == maxBriefingEvidence {
					briefing.OmittedEvidence++
					continue
				}
				briefing.RequiredEvidence = append(briefing.RequiredEvidence, field)
				briefing.RequiredEvidenceDisplay = append(briefing.RequiredEvidenceDisplay, truncateBriefing(field))
			}
		}
	}
	for _, task := range board.Blocked {
		if len(briefing.Blockers) == maxBriefingBlockers {
			briefing.OmittedBlockers++
			continue
		}
		reason := task.Blocker
		if reason == "" {
			reason = "blocked without a recorded reason"
		}
		briefing.Blockers = append(briefing.Blockers, BriefingBlocker{
			ID: task.ID, DisplayID: truncateBriefing(task.ID),
			Reason: reason, DisplayReason: truncateBriefing(reason),
		})
	}
	briefing.Remediation = remediationForRunState(state)
	return briefing
}

func remediationForRunState(state RunState) string {
	switch state.Disposition {
	case RunContinue:
		return "continue the current Sub-Task; run `reconc task check-done` before promotion"
	case RunClaim:
		return fmt.Sprintf("run `reconc task claim %s` for %s", state.TaskID, state.TaskPath)
	case RunBlocked:
		if state.Blocker == runDependencyBlocker {
			return fmt.Sprintf("resolve dependencies for TASK %s at %s, then run `reconc task claim %s`", state.TaskID, state.TaskPath, state.TaskID)
		}
		return fmt.Sprintf("resolve the blocker for TASK %s at %s, then run `reconc task resume %s`", state.TaskID, state.TaskPath, state.TaskID)
	case RunComplete:
		return "no open TASK remains"
	case RunInvalid:
		return "repair TASK state: " + truncateBriefing(state.Blocker)
	default:
		return "no TASK run state is available"
	}
}

func currentSubTask(task *Task) string {
	for _, subTask := range task.SubTasks {
		if subTask.State == StateActive {
			return truncateBriefing(subTask.Text)
		}
	}
	return ""
}

func truncateBriefing(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maxBriefingTextRunes {
		return value
	}
	return string(runes[:maxBriefingTextRunes-1]) + "…"
}
