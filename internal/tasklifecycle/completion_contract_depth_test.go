package tasklifecycle

import (
	"reflect"
	"strings"
	"testing"
)

func TestDirtyCompletionPathsMatchesOnlyTaskControlPlane(t *testing.T) {
	defaultCfg := Config{OverviewPath: "docs/tasks.md", DetailDir: "docs/tasks"}
	splitCfg := Config{OverviewPath: "ops/board.md", DetailDir: "work/tasks"}
	nestedCfg := Config{OverviewPath: "team/alpha/tasks.md", DetailDir: "team/alpha/tasks"}
	rootCfg := Config{OverviewPath: "tasks.md", DetailDir: "tasks"}
	dottedCfg := Config{OverviewPath: "docs.v2/tasks.md", DetailDir: "docs.v2/tasks"}

	tests := []struct {
		name  string
		cfg   Config
		dirty []string
		want  []string
	}{
		{
			name: "default layout preserves git order and displayed paths",
			cfg:  defaultCfg,
			dirty: []string{
				"README.md",
				"docs/tasks.md",
				"docs/tasks",
				"docs/tasks/",
				"docs/tasks/092-coverage.md",
				"docs/tasks/done/091-bootstrap.md",
				"docs/",
				"docs",
				"internal/runtime/events.go",
			},
			want: []string{
				"docs/tasks.md",
				"docs/tasks",
				"docs/tasks/",
				"docs/tasks/092-coverage.md",
				"docs/tasks/done/091-bootstrap.md",
				"docs/",
				"docs",
			},
		},
		{
			name: "prefix collisions stay outside the control plane",
			cfg:  defaultCfg,
			dirty: []string{
				"doc",
				"docs2",
				"docs-old",
				"documentation",
				"docs/task",
				"docs/taskset",
				"docs/tasks.md.bak",
				"docs/tasks.md",
				"docs/tasks",
			},
			want: []string{"docs/tasks.md", "docs/tasks"},
		},
		{
			name:  "redundant separators and slashes canonicalize for matching only",
			cfg:   defaultCfg,
			dirty: []string{"./docs//tasks.md", "docs/./tasks/092.md", "docs//", `docs\tasks.md`},
			want:  []string{"./docs//tasks.md", "docs/./tasks/092.md", "docs//", `docs\tasks.md`},
		},
		{
			name:  "independent overview and detail ancestors",
			cfg:   splitCfg,
			dirty: []string{"ops", "work", "ops/", "work/tasks/001.md", "docs", "README.md"},
			want:  []string{"ops", "work", "ops/", "work/tasks/001.md"},
		},
		{
			name:  "shared nested ancestors",
			cfg:   nestedCfg,
			dirty: []string{"team", "team/alpha", "team/beta", "team/alpha/tasks.md", "team/alpha/tasks/001.md"},
			want:  []string{"team", "team/alpha", "team/alpha/tasks.md", "team/alpha/tasks/001.md"},
		},
		{
			name:  "root-level overview and detail have no extra ancestors",
			cfg:   rootCfg,
			dirty: []string{"docs", "tasks.md", "tasks", "tasks/001.md", "tasks.md.bak"},
			want:  []string{"tasks.md", "tasks", "tasks/001.md"},
		},
		{
			name:  "dots inside segment names stay exact",
			cfg:   dottedCfg,
			dirty: []string{"docs", "docs.v2", "docs.v2/tasks.md", "docs.v2/tasks/001.md"},
			want:  []string{"docs.v2", "docs.v2/tasks.md", "docs.v2/tasks/001.md"},
		},
		{
			name:  "escapes and empty names are not owned",
			cfg:   defaultCfg,
			dirty: []string{"", ".", "..", "../docs", "/docs", "docs/../secret"},
			want:  []string{},
		},
		{
			name:  "duplicate git strings stay unique in input order",
			cfg:   defaultCfg,
			dirty: []string{"docs", "docs/tasks.md", "docs", "docs/tasks.md"},
			want:  []string{"docs", "docs/tasks.md"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DirtyCompletionPaths(test.cfg, test.dirty)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DirtyCompletionPaths = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCheckCompletionResolvesDefaultAndExplicitTask(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Active", state: StateActive, subTasks: "- [x] Complete"},
		{id: "002", title: "Queued", state: StateQueued, subTasks: "- [ ] Waiting"},
	})

	id, issues, err := CheckCompletion(repo, "")
	if err != nil {
		t.Fatalf("CheckCompletion(active): %v", err)
	}
	if id != "001" || len(issues) != 0 {
		t.Fatalf("active completion = id %q, issues %+v", id, issues)
	}

	id, issues, err = CheckCompletion(repo, "002")
	if err != nil {
		t.Fatalf("CheckCompletion(explicit): %v", err)
	}
	if id != "002" || len(issues) == 0 {
		t.Fatalf("queued completion = id %q, issues %+v", id, issues)
	}
}

func TestCheckCompletionReportsMissingTaskAndInvalidBoard(t *testing.T) {
	repo := sectionedRepo(t, "", []testTask{
		{id: "001", title: "Active", state: StateActive, subTasks: "- [x] Complete"},
	})
	if _, _, err := CheckCompletion(repo, "999"); err == nil || !strings.Contains(err.Error(), `TASK "999" was not found`) {
		t.Fatalf("expected missing-task error, got %v", err)
	}

	invalid := t.TempDir()
	if _, _, err := CheckCompletion(invalid, ""); err == nil {
		t.Fatal("invalid TASK board was accepted")
	}
}
