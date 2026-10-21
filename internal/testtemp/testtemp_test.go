package testtemp

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Only the top-level process of a cacheable go test run receives the test log
// flag; re-executed helper binaries never do.
func TestWritesTestLogRecognizesOnlyTheGoTestFlag(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      bool
	}{
		{[]string{"-test.paniconexit0", "-test.testlogfile=/tmp/testlog.txt", "-test.timeout=10m0s"}, true},
		{[]string{"-test.testlogfile", "/tmp/testlog.txt"}, true},
		{[]string{"-test.run=^TestMCPGatewayFakeProcess$"}, false},
		{[]string{"-test.testlogfilex=/tmp/testlog.txt"}, false},
		{nil, false},
	} {
		if got := writesTestLog(test.arguments); got != test.want {
			t.Fatalf("writesTestLog(%q) = %t, want %t", test.arguments, got, test.want)
		}
	}
}

// The short root and a package's setup are removed on every path, also when
// setup fails after the root exists, and every failure keeps the exit code
// nonzero.
func TestMainWithSetupCleansUpOnEveryPath(t *testing.T) {
	for _, test := range []struct {
		name       string
		rootErr    bool
		setupErr   bool
		cleanupErr bool
		runCode    int
		want       int
		ran        bool
	}{
		{name: "root creation fails", rootErr: true, want: 1},
		{name: "setup fails after creating its directory", setupErr: true, want: 1},
		{name: "setup cleanup fails", cleanupErr: true, want: 1, ran: true},
		{name: "tests fail", runCode: 3, want: 3, ran: true},
		{name: "tests pass", want: 0, ran: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			staged := filepath.Join(parent, "staged")
			useRoot := func() (func() error, error) {
				if test.rootErr {
					return nil, errors.New("root unavailable")
				}
				if err := os.Mkdir(root, 0o700); err != nil {
					return nil, err
				}
				return func() error { return os.RemoveAll(root) }, nil
			}
			setup := func() (func() error, error) {
				if err := os.Mkdir(staged, 0o700); err != nil {
					return nil, err
				}
				cleanup := func() error {
					if err := os.RemoveAll(staged); err != nil {
						return err
					}
					if test.cleanupErr {
						return errors.New("cleanup failed")
					}
					return nil
				}
				if test.setupErr {
					return cleanup, errors.New("setup failed")
				}
				return cleanup, nil
			}
			ran := false
			got := mainWithSetup(useRoot, setup, func() int { ran = true; return test.runCode }, io.Discard)
			if got != test.want || ran != test.ran {
				t.Fatalf("exit code %d, ran %t; want %d, %t", got, ran, test.want, test.ran)
			}
			for _, path := range []string{root, staged} {
				if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("%s left behind: %v", path, err)
				}
			}
		})
	}
}
