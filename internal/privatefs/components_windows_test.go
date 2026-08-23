//go:build windows

package privatefs

import (
	"reflect"
	"testing"
)

func TestSplitComponentsPreservesWindowsDriveAndUNCRoots(t *testing.T) {
	for _, test := range []struct {
		path string
		want []string
	}{
		{path: `alpha\beta`, want: []string{"alpha", "beta"}},
		{path: `C:\alpha\beta`, want: []string{"alpha", "beta"}},
		{path: `\\server\share\alpha\beta`, want: []string{"alpha", "beta"}},
		{path: `C:\`, want: []string{}},
		{path: `\\server\share\`, want: []string{}},
		{path: "", want: []string{}},
		{path: ".", want: []string{}},
	} {
		if got := splitComponents(test.path); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("splitComponents(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}
