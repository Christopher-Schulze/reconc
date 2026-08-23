package tasklifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublishTransactionRefusesConcurrentFileContentDrift(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/tasks.md")
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	files := []fileMutation{{Path: "docs/tasks.md", After: []byte("after\n")}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	external := []byte("external\n")
	if err := os.WriteFile(path, external, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := publishTransaction(repo, journal, files, nil); err == nil ||
		!strings.Contains(err.Error(), "precondition") {
		t.Fatalf("concurrent content drift was not refused: %v", err)
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, external) {
		t.Fatalf("refused publication overwrote external bytes: body=%q err=%v", current, err)
	}
}

func TestPublishTransactionRefusesConcurrentFileModeDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission-bit drift")
	}
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/tasks.md")
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	files := []fileMutation{{Path: "docs/tasks.md", After: []byte("after\n")}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishTransaction(repo, journal, files, nil); err == nil ||
		!strings.Contains(err.Error(), "mode") {
		t.Fatalf("concurrent mode drift was not refused: %v", err)
	}
	if current, err := os.ReadFile(path); err != nil || string(current) != "before\n" {
		t.Fatalf("refused publication changed file: body=%q err=%v", current, err)
	}
}

func TestPublishTransactionRefusesConcurrentMoveDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "source",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				writeFile(t, repo, "docs/tasks/001-source.md", []byte("external source\n"))
			},
		},
		{
			name: "destination",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				writeFile(t, repo, "docs/tasks/done/001-source.md", []byte("external destination\n"))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFile(t, repo, "docs/tasks/001-source.md", []byte("original\n"))
			moves := []moveMutation{{
				Source: "docs/tasks/001-source.md", Destination: "docs/tasks/done/001-source.md",
			}}
			journal, err := buildTransaction(repo, "test", nil, moves)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, repo)
			if err := publishTransaction(repo, journal, nil, moves); err == nil ||
				!strings.Contains(err.Error(), "precondition") {
				t.Fatalf("concurrent %s drift was not refused: %v", test.name, err)
			}
			if test.name == "source" {
				if body, err := os.ReadFile(filepath.Join(repo, "docs/tasks/001-source.md")); err != nil ||
					string(body) != "external source\n" {
					t.Fatalf("source drift was overwritten: body=%q err=%v", body, err)
				}
			} else {
				if body, err := os.ReadFile(filepath.Join(repo, "docs/tasks/done/001-source.md")); err != nil ||
					string(body) != "external destination\n" {
					t.Fatalf("destination drift was overwritten: body=%q err=%v", body, err)
				}
			}
		})
	}
}

func TestMoveTransactionCapturesSourceBytesAndMode(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	moves := []moveMutation{{
		Source: "docs/tasks/001-source.md", Destination: "docs/tasks/done/001-source.md",
	}}
	journal, err := buildTransaction(repo, "test", nil, moves)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Files) != 1 || journal.Files[0].Path != moves[0].Source ||
		!bytes.Equal(journal.Files[0].Before, []byte("detail\n")) ||
		journal.Files[0].BeforeMode == 0 {
		t.Fatalf("move source before-image is incomplete: %+v", journal.Files)
	}
}

func TestRecoverRefusesModeOnlyDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission-bit drift")
	}
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/tasks.md")
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	files := []fileMutation{{Path: "docs/tasks.md", After: []byte("after\n")}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("mode-only recovery drift was not refused: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != "after\n" || info.Mode().Perm() != 0o600 {
		t.Fatalf("refused recovery changed state: body=%q mode=%o err=%v", current, info.Mode().Perm(), err)
	}
}

func TestRecoverRefusesCreatedFileContentDrift(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/created.md")
	files := []fileMutation{{
		Path: "docs/created.md", After: []byte("created\n"), Create: true,
	}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	external := []byte("external\n")
	if err := os.WriteFile(path, external, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "changed outside") {
		t.Fatalf("created-file content drift was not refused: %v", err)
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, external) {
		t.Fatalf("refused recovery changed created file: body=%q err=%v", current, err)
	}
}

func TestRecoverRefusesCreatedFileModeDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission-bit drift")
	}
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/created.md")
	files := []fileMutation{{
		Path: "docs/created.md", After: []byte("created\n"), Create: true,
	}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("created-file mode drift was not refused: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("refused recovery changed created-file mode: %04o", info.Mode().Perm())
	}
}

func TestRecoverReversesLinkedMoveIntermediate(t *testing.T) {
	repo := t.TempDir()
	source := filepath.Join(repo, "docs/tasks/001-source.md")
	destination := filepath.Join(repo, "docs/tasks/done/001-source.md")
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	moves := []moveMutation{{
		Source: "docs/tasks/001-source.md", Destination: "docs/tasks/done/001-source.md",
	}}
	journal, err := buildTransaction(repo, "test", nil, moves)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := prepareTransactionDirectories(repo, journal.CreatedDirectories); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(source, destination); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err != nil {
		t.Fatalf("recover linked move intermediate: %v", err)
	}
	if current, err := os.ReadFile(source); err != nil || string(current) != "detail\n" {
		t.Fatalf("linked move recovery lost source: body=%q err=%v", current, err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("linked move recovery left destination: %v", err)
	}
}

func TestRecoverRefusesMovedDestinationModeDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission-bit drift")
	}
	repo := t.TempDir()
	destination := filepath.Join(repo, "docs/tasks/done/001-source.md")
	writeFile(t, repo, "docs/tasks/001-source.md", []byte("detail\n"))
	moves := []moveMutation{{
		Source: "docs/tasks/001-source.md", Destination: "docs/tasks/done/001-source.md",
	}}
	journal, err := buildTransaction(repo, "test", nil, moves)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, nil, moves); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("moved destination mode drift was not refused: %v", err)
	}
	if current, err := os.ReadFile(destination); err != nil || string(current) != "detail\n" {
		t.Fatalf("refused recovery changed destination: body=%q err=%v", current, err)
	}
}

func TestRecoverRefusesFileTypeDrift(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/tasks.md")
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	files := []fileMutation{{Path: "docs/tasks.md", After: []byte("after\n")}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("file-type drift was not refused: %v", err)
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		t.Fatalf("refused recovery changed replacement directory: info=%v err=%v", info, err)
	}
}

func TestRecoverRefusesSymlinkDrift(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "docs/tasks.md")
	target := filepath.Join(repo, "external.md")
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	writeFile(t, repo, "external.md", []byte("external\n"))
	files := []fileMutation{{Path: "docs/tasks.md", After: []byte("after\n")}}
	journal, err := buildTransaction(repo, "test", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, body)
	if err := publishTransaction(repo, journal, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := Recover(repo); err == nil || !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("symlink drift was not refused: %v", err)
	}
	if current, err := os.ReadFile(target); err != nil || string(current) != "external\n" {
		t.Fatalf("refused recovery changed symlink target: body=%q err=%v", current, err)
	}
}

func TestReadTransactionRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown field",
			body: `{
				"format_version": 1,
				"action": "test",
				"files": [],
				"unexpected": true
			}`,
			want: "unknown field",
		},
		{
			name: "multiple values",
			body: `{
				"format_version": 1,
				"action": "test",
				"files": []
			} {}`,
			want: "trailing JSON value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFile(t, repo, transactionRel, []byte(test.body))
			if _, err := readTransaction(repo); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid transaction journal passed: %v", err)
			}
		})
	}
}

func TestTransactionPresenceFailsClosedOnInspectionError(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".reconc", []byte("not a directory\n"))
	if exists, err := transactionExists(repo); err == nil || exists {
		t.Fatalf("uninspectable transaction path did not fail closed: exists=%v err=%v", exists, err)
	}
}

func TestTransactionJournalShapeRejectsCorruption(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*transaction)
		want   string
	}{
		{name: "version", mutate: func(journal *transaction) { journal.FormatVersion++ }, want: "format_version"},
		{name: "phase", mutate: func(journal *transaction) { journal.Phase = "unknown" }, want: "phase"},
		{name: "action", mutate: func(journal *transaction) { journal.Action = "" }, want: "no action"},
		{name: "path", mutate: func(journal *transaction) { journal.Files[0].Path = "../escape" }, want: "invalid path"},
		{name: "duplicate file", mutate: func(journal *transaction) {
			journal.Files = append(journal.Files, journal.Files[0])
		}, want: "repeats path"},
		{name: "mode", mutate: func(journal *transaction) { journal.Files[0].BeforeMode = 0 }, want: "before mode"},
		{name: "hash", mutate: func(journal *transaction) { journal.Files[0].AfterHash = "invalid" }, want: "content hash"},
		{name: "created before image", mutate: func(journal *transaction) {
			journal.Files[0].Created = true
		}, want: "has a before image"},
		{name: "before image hash", mutate: func(journal *transaction) {
			journal.Files[0].Before = []byte("wrong\n")
		}, want: "wrong hash"},
		{name: "move path", mutate: func(journal *transaction) {
			addValidTestMove(journal)
			journal.Moves[0].Destination = "../escape"
		}, want: "invalid move path"},
		{name: "move conflict", mutate: func(journal *transaction) {
			addValidTestMove(journal)
			journal.Moves[0].Destination = journal.Moves[0].Source
		}, want: "conflicting move"},
		{name: "move hash", mutate: func(journal *transaction) {
			addValidTestMove(journal)
			journal.Moves[0].AfterHash = "invalid"
		}, want: "invalid move hash"},
		{name: "move source image", mutate: func(journal *transaction) {
			addValidTestMove(journal)
			journal.Files = nil
		}, want: "no before-image"},
		{name: "move image mismatch", mutate: func(journal *transaction) {
			addValidTestMove(journal)
			journal.Moves[0].AfterHash = hashContent([]byte("different\n"))
		}, want: "disagrees"},
		{name: "directory path", mutate: func(journal *transaction) {
			journal.CreatedDirectories = []transactionDirectory{{Path: "../escape", MarkerToken: strings.Repeat("a", 64)}}
		}, want: "created directory"},
		{name: "directory token", mutate: func(journal *transaction) {
			journal.CreatedDirectories = []transactionDirectory{{Path: "docs/new", MarkerToken: "invalid"}}
		}, want: "ownership token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal := validTestTransaction()
			test.mutate(&journal)
			if err := validateTransactionShape(journal); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("corrupt journal passed shape validation: %v", err)
			}
		})
	}
}

func TestPublishInputsRejectJournalRequestMismatch(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("before\n"))
	original := fileMutation{Path: "docs/tasks.md", After: []byte("after\n")}

	t.Run("after image", func(t *testing.T) {
		journal, err := buildTransaction(repo, "test", []fileMutation{original}, nil)
		if err != nil {
			t.Fatal(err)
		}
		changed := original
		changed.After = []byte("different\n")
		if err := validatePublishInputs(repo, journal, []fileMutation{changed}, nil); err == nil {
			t.Fatal("mismatched requested after-image passed")
		}
	})

	t.Run("duplicate input", func(t *testing.T) {
		journal, err := buildTransaction(repo, "test", []fileMutation{original}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := validatePublishInputs(repo, journal, []fileMutation{original, original}, nil); err == nil {
			t.Fatal("duplicate requested file passed")
		}
	})

	t.Run("unrequested journal file", func(t *testing.T) {
		journal, err := buildTransaction(repo, "test", []fileMutation{original}, nil)
		if err != nil {
			t.Fatal(err)
		}
		extra := journal.Files[0]
		extra.Path = "docs/extra.md"
		journal.Files = append(journal.Files, extra)
		if err := validatePublishInputs(repo, journal, []fileMutation{original}, nil); err == nil {
			t.Fatal("unrequested journal file passed")
		}
	})

	t.Run("move count", func(t *testing.T) {
		journal, err := buildTransaction(repo, "test", []fileMutation{original}, nil)
		if err != nil {
			t.Fatal(err)
		}
		journal.Moves = append(journal.Moves, transactionMove{})
		if err := validatePublishInputs(repo, journal, []fileMutation{original}, nil); err == nil {
			t.Fatal("journal move without a requested move passed")
		}
	})

	t.Run("move identity", func(t *testing.T) {
		move := moveMutation{Source: "docs/tasks.md", Destination: "docs/done.md"}
		journal, err := buildTransaction(repo, "test", []fileMutation{original}, []moveMutation{move})
		if err != nil {
			t.Fatal(err)
		}
		move.Destination = "docs/other.md"
		if err := validatePublishInputs(repo, journal, []fileMutation{original}, []moveMutation{move}); err == nil {
			t.Fatal("mismatched requested move passed")
		}
	})

	t.Run("unsafe move path", func(t *testing.T) {
		move := moveMutation{Source: "docs/tasks.md", Destination: "docs/done.md"}
		journal, err := buildTransaction(repo, "test", []fileMutation{original}, []moveMutation{move})
		if err != nil {
			t.Fatal(err)
		}
		move.Source = "../escape"
		if err := validatePublishInputs(repo, journal, []fileMutation{original}, []moveMutation{move}); err == nil {
			t.Fatal("unsafe requested move passed")
		}
	})
}

func validTestTransaction() transaction {
	before := []byte("before\n")
	return transaction{
		FormatVersion: transactionVersion,
		Action:        "test",
		Phase:         transactionPhasePrepared,
		Files: []transactionFile{{
			Path:       "docs/tasks.md",
			Before:     before,
			BeforeMode: 0o644,
			BeforeHash: hashContent(before),
			AfterHash:  hashContent([]byte("after\n")),
		}},
	}
}

func addValidTestMove(journal *transaction) {
	file := journal.Files[0]
	journal.Moves = []transactionMove{{
		Source:      file.Path,
		Destination: "docs/tasks/done/tasks.md",
		BeforeHash:  file.BeforeHash,
		AfterHash:   file.AfterHash,
	}}
}
