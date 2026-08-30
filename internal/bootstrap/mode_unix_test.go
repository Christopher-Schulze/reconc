//go:build !windows

package bootstrap

import (
	"os"
	"testing"
)

func TestModeSatisfiesRequiresExactUnixPermissions(t *testing.T) {
	tests := []struct {
		name    string
		actual  os.FileMode
		desired uint32
		want    bool
	}{
		{name: "data exact", actual: 0o644, desired: 0o644, want: true},
		{name: "executable exact", actual: 0o755, desired: 0o755, want: true},
		{name: "missing execute", actual: 0o644, desired: 0o755, want: false},
		{name: "extra group write", actual: 0o664, desired: 0o644, want: false},
		{name: "read only", actual: 0o444, desired: 0o644, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := modeSatisfies(test.actual, test.desired); got != test.want {
				t.Fatalf("modeSatisfies(%04o, %04o) = %t, want %t", test.actual.Perm(), test.desired, got, test.want)
			}
		})
	}
}
