package retention

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedTempPrunesBeyondForeignDirectoryLimit(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry-run=%t", dryRun), func(t *testing.T) {
			root := t.TempDir()
			for index := 0; index <= maxRetentionDirectoryEntries; index++ {
				if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("foreign-%05d", index)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			old := filepath.Join(root, "reconc-proof-neg-expired")
			recent := filepath.Join(root, "reconc-proof-gocache-current")
			foreign := filepath.Join(root, "foreign-00000", "payload")
			for path, age := range map[string]time.Duration{filepath.Join(old, "payload"): 48 * time.Hour, filepath.Join(recent, "payload"): 0, foreign: 48 * time.Hour} {
				writeTimed(t, path, []byte("preserve ownership"), now.Add(-age))
				if err := os.Chtimes(filepath.Dir(path), now.Add(-age), now.Add(-age)); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(root, "reconc-proof-neg-foreign-link")
			if err := os.Symlink(filepath.Dir(foreign), link); err != nil {
				t.Fatal(err)
			}
			policy := DefaultPolicy()
			report := Report{}
			class := pruneOwnedTempRoots(Options{TempRoot: root, Now: now, Policy: policy, DryRun: dryRun}, &report)
			if len(report.Errors) != 0 || class.FilesDeleted != 1 || class.FilesKept != 1 {
				t.Fatalf("owned retention: %+v; errors=%v", class, report.Errors)
			}
			_, err := os.Stat(old)
			if dryRun && err != nil || !dryRun && !os.IsNotExist(err) {
				t.Fatalf("expired directory dry-run=%t: %v", dryRun, err)
			}
			body, err := os.ReadFile(foreign)
			if err != nil || string(body) != "preserve ownership" {
				t.Fatalf("foreign payload changed: %q %v", body, err)
			}
			entries, err := os.ReadDir(root)
			want := maxRetentionDirectoryEntries + 3
			if dryRun {
				want++
			}
			if err != nil || len(entries) != want {
				t.Fatalf("foreign/current entries removed: count=%d want=%d err=%v", len(entries), want, err)
			}
		})
	}
}

func TestOwnedTempScanBudgetsFailWithoutCandidates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"foreign-a", "foreign-b", "reconc-proof-neg-a", "reconc-proof-neg-b"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, failure  string
		scanned, owned int
	}{
		{"scan overflow", "scanned entries", 3, 2},
		{"owned overflow", "owned directories", 4, 1},
		{"invalid scan budget", "positive limits", 0, 2},
		{"invalid owned budget", "positive limits", 4, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries, err := readOwnedTempDirectories(root, test.scanned, test.owned)
			if err == nil || !strings.Contains(err.Error(), test.failure) || len(entries) != 0 {
				t.Fatalf("partial candidate set admitted: %v %v", entries, err)
			}
		})
	}
	entries, err := readOwnedTempDirectories(root, 4, 2)
	if err != nil || len(entries) != 2 {
		t.Fatalf("exact limits rejected or foreign names retained: %v %v", entries, err)
	}
}
