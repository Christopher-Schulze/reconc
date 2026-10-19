package tasklifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSectionsRunStateRetriesTransientPathMutation(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		permanent bool
	}{
		{name: "overview", target: "docs/tasks.md"},
		{name: "detail", target: "docs/tasks/001-current.md"},
		{name: "unstable overview", target: "docs/tasks.md", permanent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := writeParityBoard(t, "", nil)
			previous := lstatTaskPathComponent
			t.Cleanup(func() { lstatTaskPathComponent = previous })
			probes, changes := 0, 0
			lstatTaskPathComponent = func(path string) (os.FileInfo, error) {
				if strings.HasSuffix(path, filepath.FromSlash("/"+test.target)) {
					probes++
					if probes%2 == 0 && (test.permanent || changes == 0) {
						changes++
						stamp := time.Now().Add(time.Duration(changes) * time.Second)
						if err := os.Chtimes(path, stamp, stamp); err != nil {
							t.Fatal(err)
						}
					}
				}
				return previous(path)
			}
			state, err := InspectRunState(repo)
			if changes == 0 {
				t.Fatal("fixture did not mutate a path during the read")
			}
			if test.permanent {
				if !onlyConcurrentReadMutation(err) || changes != maxConcurrentReadAttempts {
					t.Fatalf("unstable read: changes=%d state=%+v err=%v", changes, state, err)
				}
				return
			}
			if err != nil || state.Disposition != RunContinue || changes != 1 {
				t.Fatalf("transient read: changes=%d state=%+v err=%v", changes, state, err)
			}
		})
	}
}
