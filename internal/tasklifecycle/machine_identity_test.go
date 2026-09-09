package tasklifecycle

import (
	"strings"
	"testing"
)

func TestBriefingPreservesMachineIdentifiersAndBoundsDisplay(t *testing.T) {
	path := "tasks/" + strings.Repeat("long  segment/", 24) + "task.md"
	field := "Evidence  " + strings.Repeat("field/", 30)
	active := &Task{
		ID: "001", Title: "Long path", Path: path, State: StateActive,
		SubTasks:       []SubTask{{State: StateActive, Text: "Work"}},
		EvidenceFields: map[string]string{},
	}
	board := decisionBoard(active, nil, nil)
	board.Config.Completion.RequiredEvidenceFields = []string{field}
	briefing := BuildBriefing(board)
	if briefing.Current == nil || briefing.Current.Path != path {
		t.Fatalf("briefing path = %q, want exact %q", briefing.Current.Path, path)
	}
	if briefing.Current.DisplayPath == path || len([]rune(briefing.Current.DisplayPath)) > maxBriefingTextRunes {
		t.Fatalf("briefing display path = %q, want bounded display distinct from exact path", briefing.Current.DisplayPath)
	}
	if len(briefing.RequiredEvidence) != 1 || briefing.RequiredEvidence[0] != field {
		t.Fatalf("briefing evidence = %#v, want exact %q", briefing.RequiredEvidence, field)
	}
	if len(briefing.RequiredEvidenceDisplay) != 1 || briefing.RequiredEvidenceDisplay[0] == field {
		t.Fatalf("briefing evidence display = %#v, want bounded display distinct from exact %q", briefing.RequiredEvidenceDisplay, field)
	}
}
