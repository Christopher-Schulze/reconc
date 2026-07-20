//go:build windows

package pathidentity

import "testing"

func TestNormalizeWindowsFinalPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "drive", path: `\\?\C:\workspace\repo`, want: `C:\workspace\repo`},
		{name: "UNC", path: `\\?\UNC\server\share\repo`, want: `\\server\share\repo`},
		{name: "plain", path: `C:\workspace\repo`, want: `C:\workspace\repo`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeWindowsFinalPath(test.path); got != test.want {
				t.Fatalf("normalizeWindowsFinalPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}
