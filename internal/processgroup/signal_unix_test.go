//go:build !windows

package processgroup

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestSignalRejectsUnsafeGroupIDsAndPreservesInvalidSignal(t *testing.T) {
	const invalidSignal syscall.Signal = 1 << 30
	for _, test := range []struct {
		name    string
		groupID int
		signal  syscall.Signal
	}{
		{name: "negative group", groupID: -1},
		{name: "current group shorthand", groupID: 0},
		{name: "broadcast shorthand", groupID: 1},
		{name: "invalid signal", groupID: os.Getpid(), signal: invalidSignal},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Signal(test.groupID, test.signal); !errors.Is(err, syscall.EINVAL) {
				t.Fatalf("signal error = %v, want EINVAL", err)
			}
		})
	}
}
