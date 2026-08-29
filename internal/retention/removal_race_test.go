package retention

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveCandidateRejectsReplacementBeforeDeletion(t *testing.T) {
	for _, test := range []struct {
		name    string
		symlink bool
	}{
		{name: "regular replacement"},
		{name: "symlink substitution", symlink: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "stale.json")
			if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			replacement := filepath.Join(root, "replacement.json")
			if err := os.WriteFile(replacement, []byte("replacement\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			item := candidate{path: path, name: filepath.Base(path), info: info}
			report := Report{}
			removed := removeCandidateWithHooks(item, false, &report, candidateRemovalHooks{
				afterValidation: func(item candidate) error {
					if err := os.Rename(item.path, item.path+".moved"); err != nil {
						return err
					}
					if test.symlink {
						return os.Symlink(replacement, item.path)
					}
					return os.WriteFile(item.path, []byte("replacement\n"), 0o600)
				},
			})
			if removed {
				t.Fatal("replacement was deleted")
			}
			if len(report.Errors) == 0 {
				t.Fatal("replacement race was not reported")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("replacement path was removed: %v", err)
			}
			if _, err := os.Lstat(path + ".moved"); err != nil {
				t.Fatalf("original candidate was not preserved: %v", err)
			}
		})
	}
}

func TestRemoveCandidateRejectsRepopulatedDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stale-root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "original"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	item := candidate{path: path, name: filepath.Base(path), info: info, dir: true}
	report := Report{}
	removed := removeCandidateWithHooks(item, false, &report, candidateRemovalHooks{
		afterValidation: func(item candidate) error {
			moved := item.path + ".moved"
			if err := os.Rename(item.path, moved); err != nil {
				return err
			}
			if err := os.Mkdir(item.path, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(item.path, "replacement"), []byte("replacement\n"), 0o600)
		},
	})
	if removed {
		t.Fatal("repopulated directory was deleted")
	}
	if len(report.Errors) == 0 {
		t.Fatal("directory replacement was not reported")
	}
	for _, path := range []string{path, path + ".moved", filepath.Join(path, "replacement"), filepath.Join(path+".moved", "original")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("repopulation fixture changed unexpectedly at %s: %v", path, err)
		}
	}
}

func TestRemoveCandidateRejectsDirectorySymlinkSubstitution(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stale-root")
	foreign := filepath.Join(root, "foreign-root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "original"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "foreign"), []byte("foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	item := candidate{path: path, name: filepath.Base(path), info: info, dir: true}
	report := Report{}
	removed := removeCandidateWithHooks(item, false, &report, candidateRemovalHooks{
		afterValidation: func(item candidate) error {
			if err := os.Rename(item.path, item.path+".moved"); err != nil {
				return err
			}
			if err := os.Symlink(foreign, item.path); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			return nil
		},
	})
	if removed {
		t.Fatal("directory symlink replacement was deleted")
	}
	if len(report.Errors) == 0 {
		t.Fatal("directory symlink replacement was not reported")
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("directory symlink replacement changed: info=%v err=%v", info, err)
	}
	for _, path := range []string{path + ".moved", filepath.Join(path+".moved", "original"), filepath.Join(foreign, "foreign")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("directory symlink fixture changed unexpectedly at %s: %v", path, err)
		}
	}
}

func TestRemoveCandidateDirectoryDoesNotFollowChildSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stale-root")
	foreign := filepath.Join(root, "foreign-root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignFile := filepath.Join(foreign, "must-survive")
	if err := os.WriteFile(foreignFile, []byte("foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(path, "escape")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	report := Report{}
	if !removeCandidate(candidate{path: path, name: filepath.Base(path), info: info, dir: true}, false, &report) {
		t.Fatalf("safe directory removal failed: errors=%v", report.Errors)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("candidate directory survived: %v", err)
	}
	if body, err := os.ReadFile(foreignFile); err != nil || string(body) != "foreign\n" {
		t.Fatalf("child symlink target was removed: body=%q err=%v", body, err)
	}
}

func TestRemoveCandidateRequiresIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stale.json")
	if err := os.WriteFile(path, []byte("state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Report{}
	if removeCandidate(candidate{path: path}, false, &report) {
		t.Fatal("candidate without an identity was deleted")
	}
	if len(report.Errors) == 0 {
		t.Fatal("identity-less candidate was not reported")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("identity-less candidate changed target: %v", err)
	}
}
