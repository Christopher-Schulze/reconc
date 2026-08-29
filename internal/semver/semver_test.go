package semver

import (
	"strings"
	"testing"
)

func TestParseAndComparePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		left       string
		right      string
		want       int
		leftBuild  []string
		rightBuild []string
	}{
		{name: "equal stable", left: "1.0.0", right: "1.0.0", want: 0},
		{name: "alpha sequence", left: "1.0.0-alpha", right: "1.0.0-alpha.1", want: -1},
		{name: "numeric before text", left: "1.0.0-1", right: "1.0.0-alpha", want: -1},
		{name: "text before numeric", left: "1.0.0-alpha", right: "1.0.0-1", want: 1},
		{name: "numeric ordinal", left: "1.0.0-preview.2", right: "1.0.0-preview.10", want: -1},
		{name: "numeric overflow-safe ordinal", left: "1.0.0-preview.18446744073709551617", right: "1.0.0-preview.18446744073709551616", want: 1},
		{name: "stable after prerelease", left: "1.0.0", right: "1.0.0-rc.1", want: 1},
		{name: "build ignored", left: "1.0.0+build.2", right: "1.0.0+build.1", want: 0,
			leftBuild: []string{"build", "2"}, rightBuild: []string{"build", "1"}},
		{name: "build ignored with prerelease", left: "1.0.0-rc.1+build.2", right: "1.0.0-rc.1+build.1", want: 0,
			leftBuild: []string{"build", "2"}, rightBuild: []string{"build", "1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, err := Parse(test.left)
			if err != nil {
				t.Fatalf("parse left: %v", err)
			}
			right, err := Parse(test.right)
			if err != nil {
				t.Fatalf("parse right: %v", err)
			}
			if got := Compare(left, right); got != test.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
			}
			if !equalStrings(left.Build, test.leftBuild) || !equalStrings(right.Build, test.rightBuild) {
				t.Fatalf("build metadata = %v and %v, want %v and %v", left.Build, right.Build, test.leftBuild, test.rightBuild)
			}
		})
	}
}

func TestParseRejectsMalformedVersions(t *testing.T) {
	for _, value := range []string{
		"", "1.0", "01.0.0", "1.0.0-", "1.0.0-01", "1.0.0+", "1.0.0+build..2",
		"1.0.0-rc..1", "1.0.0-rc+", "18446744073709551616.0.0",
	} {
		t.Run(strings.ReplaceAll(value, ".", "_"), func(t *testing.T) {
			if _, err := Parse(value); err == nil {
				t.Fatalf("malformed version %q was accepted", value)
			}
		})
	}
}

func TestIsNumericIdentifier(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "0", want: true},
		{value: "18446744073709551617", want: true},
		{value: "1a", want: false},
	} {
		if got := IsNumericIdentifier(test.value); got != test.want {
			t.Fatalf("IsNumericIdentifier(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
