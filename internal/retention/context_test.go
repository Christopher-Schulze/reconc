package retention

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetentionContextCancellationPreventsMutation(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(context.Context, Options) Report
	}{
		{name: "immediate", run: RunContext},
		{name: "due", run: RunIfDueContext},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateRoot := filepath.Join(t.TempDir(), "state")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			report := test.run(ctx, Options{RepoRoot: t.TempDir(), StateRoot: stateRoot})
			if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], context.Canceled.Error()) {
				t.Fatalf("canceled retention report = %+v", report)
			}
			if _, err := os.Lstat(stateRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("canceled retention created state root: %v", err)
			}
		})
	}
}
