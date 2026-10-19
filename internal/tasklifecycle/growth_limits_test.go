package tasklifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateKeepsWorkingWithAThousandsDeepArchive(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{{id: "001", title: "Current", state: StateActive, subTasks: "- [~] Work"}})
	done := filepath.Join(repo, "docs", "tasks", "done")
	if err := os.MkdirAll(done, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4200; index++ {
		if err := os.WriteFile(filepath.Join(done, fmt.Sprintf("%03d-old-%d.md", index%900+2, index)), []byte("archive\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(repo, "Still creatable", ""); err != nil {
		t.Fatalf("a large archive blocked TASK creation: %v", err)
	}
}

func TestPromoteHandlesMultiMegabyteControlFiles(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Finished", state: StateActive, subTasks: "- [x] Complete"},
		{id: "002", title: "Next", state: StateQueued, subTasks: "- [ ] Continue"},
	})
	notes := strings.Repeat("Long accumulated note line for growth testing.\n", 70000)
	for _, name := range []string{"001-finished.md", "002-next.md"} {
		path := filepath.Join(repo, "docs", "tasks", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		grown := strings.Replace(string(body), "## Notes\n\n", "## Notes\n\n"+notes, 1)
		if grown == string(body) {
			t.Fatalf("fixture %s has no Notes section", name)
		}
		if err := os.WriteFile(path, []byte(grown), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Promote(repo, ""); err != nil {
		t.Fatalf("multi-megabyte control files blocked promotion: %v", err)
	}
}
