package tasklifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type taskIndexFixture struct {
	File    string `json:"file"`
	Valid   bool   `json:"valid"`
	Current string `json:"current"`
	Entries int    `json:"entries"`
}

func TestLogbookParserMatchesTemplateAuditFixtures(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate parser parity test")
	}
	fixtureRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "harness", "template", "audits", "testdata", "task-index"))
	casesData, err := os.ReadFile(filepath.Join(fixtureRoot, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []taskIndexFixture
	if err := json.Unmarshal(casesData, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.File, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(fixtureRoot, fixture.File))
			if err != nil {
				t.Fatal(err)
			}
			profile, err := detectProfile(ProfileAuto, string(content))
			if err != nil {
				if fixture.Valid {
					t.Fatalf("valid shared fixture rejected during profile detection: %v", err)
				}
				return
			}
			board := &Board{
				Config: Config{OverviewPath: defaultOverviewPath}, overviewLines: strings.Split(string(content), "\n"),
				doneTargetDir: "tasks/done", tasksByID: map[string]*Task{}, tasksByName: map[string]*Task{},
				tasksByPath: map[string]*Task{}, doneIDs: map[string]bool{},
			}
			scan := board.scanLogbookOverview()
			valid := profile == ProfileLogbook && len(scan.issues) == 0
			if valid != fixture.Valid {
				t.Fatalf("valid=%t, want %t; issues=%#v", valid, fixture.Valid, scan.issues)
			}
			entryCount := len(scan.rows) + board.doneCount
			if fixture.Valid && (scan.currentName != fixture.Current || entryCount != fixture.Entries) {
				t.Fatalf("parsed current=%q entries=%d, want %q/%d", scan.currentName, entryCount, fixture.Current, fixture.Entries)
			}
		})
	}
}
