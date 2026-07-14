package tasklifecycle

import "strings"

const (
	maxBriefingBlockers  = 5
	maxBriefingEvidence  = 6
	maxBriefingTextRunes = 240
)

// Briefing is the bounded TASK portion of session-briefing. It never includes
// archive history, completed checklists, notes, or full acceptance prose.
type Briefing struct {
	Profile          Profile           `json:"profile"`
	Current          *BriefingTask     `json:"current,omitempty"`
	Blockers         []BriefingBlocker `json:"blockers,omitempty"`
	OmittedBlockers  int               `json:"omitted_blockers,omitempty"`
	RequiredEvidence []string          `json:"required_evidence,omitempty"`
	OmittedEvidence  int               `json:"omitted_evidence,omitempty"`
	Remediation      string            `json:"remediation,omitempty"`
}

type BriefingTask struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Path           string `json:"path"`
	CurrentSubTask string `json:"current_sub_task,omitempty"`
}

type BriefingBlocker struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// BuildBriefing produces a fixed-shape, archive-independent view for an AI
// session handoff.
func BuildBriefing(board *Board) Briefing {
	briefing := Briefing{Profile: board.Profile}
	if board.Active != nil {
		briefing.Current = &BriefingTask{
			ID: board.Active.ID, Title: truncateBriefing(board.Active.Title),
			Path: truncateBriefing(board.Active.Path), CurrentSubTask: currentSubTask(board.Active),
		}
		for _, field := range board.Config.Completion.RequiredEvidenceFields {
			if strings.TrimSpace(board.Active.EvidenceFields[field]) == "" {
				if len(briefing.RequiredEvidence) == maxBriefingEvidence {
					briefing.OmittedEvidence++
					continue
				}
				briefing.RequiredEvidence = append(briefing.RequiredEvidence, truncateBriefing(field))
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
		briefing.Blockers = append(briefing.Blockers, BriefingBlocker{ID: task.ID, Reason: truncateBriefing(reason)})
	}
	switch {
	case board.Active != nil:
		briefing.Remediation = "continue the current Sub-Task; run `reconc task check-done` before promotion"
	case len(board.Blocked) > 0:
		briefing.Remediation = "resolve a blocker, then run `reconc task resume <id>`"
	case len(board.Queue) > 0:
		briefing.Remediation = "run `reconc task claim <id>`"
	default:
		briefing.Remediation = "no open TASK remains"
	}
	return briefing
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
