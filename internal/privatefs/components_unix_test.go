//go:build !windows

package privatefs

import (
	"reflect"
	"testing"
)

func TestSplitComponentsUsesUnixPathSeparatorsOnly(t *testing.T) {
	for _, test := range []struct {
		path string
		want []string
	}{
		{path: "alpha/beta", want: []string{"alpha", "beta"}},
		{path: "/alpha/beta", want: []string{"alpha", "beta"}},
		{path: "alpha:beta", want: []string{"alpha:beta"}},
		{path: "", want: []string{}},
		{path: ".", want: []string{}},
		{path: "/", want: []string{}},
	} {
		if got := splitComponents(test.path); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("splitComponents(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}
