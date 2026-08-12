package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/actionstate"
)

func TestActionKeyInitCreatesPrivateStableIdentityExactlyOnce(t *testing.T) {
	home := filepath.Join(t.TempDir(), "operator-home")
	var stdout bytes.Buffer
	if err := runAction([]string{"key", "init", "--reconc-home", home, "--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var report actionKeyInitReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	resolvedHome, err := actionstate.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if report.FormatVersion != actionKeyReportFormat || report.Status != "initialized" ||
		len(report.KeyID) != 32 || report.ReconcHome != resolvedHome {
		t.Fatalf("action key report = %#v", report)
	}
	for _, character := range report.KeyID {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			t.Fatalf("action key ID is not lowercase hex: %q", report.KeyID)
		}
	}
	keyPath := filepath.Join(home, "action", "identity-key.json")
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, filepath.Join(home, "action")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("private directory %s mode = %v, %v", path, infoMode(info), err)
		}
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity key mode = %v, %v", infoMode(info), err)
	}
	stdout.Reset()
	err = runAction([]string{"key", "init", "--reconc-home", home}, &stdout)
	if err == nil || !strings.Contains(err.Error(), "identity key already exists") {
		t.Fatalf("repeat initialization error = %v", err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repeat initialization changed key: %v", err)
	}
}

func TestActionKeyInitRejectsAmbiguousArguments(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing subcommand", args: []string{"key"}, want: "missing subcommand"},
		{name: "unknown subcommand", args: []string{"key", "rotate"}, want: "expected init"},
		{name: "duplicate home", args: []string{"key", "init", "--reconc-home", "/tmp/a", "--reconc-home", "/tmp/b"}, want: "requires one path"},
		{name: "duplicate JSON", args: []string{"key", "init", "--json", "--json"}, want: "only once"},
		{name: "positional", args: []string{"key", "init", "extra"}, want: "positional arguments"},
		{name: "unknown flag", args: []string{"key", "init", "--replace"}, want: "unknown flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runAction(test.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}
