package main

import (
	"strings"
	"testing"
)

func TestLineCount(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{name: "empty", content: "", want: 0},
		{name: "single line no trailing newline", content: "a", want: 1},
		{name: "single line trailing newline", content: "a\n", want: 1},
		{name: "two lines no trailing newline", content: "a\nb", want: 2},
		{name: "two lines trailing newline", content: "a\nb\n", want: 2},
		{name: "blank line only", content: "\n", want: 1},
		{name: "trailing newline does not add phantom line", content: "x\ny\nz\n", want: 3},
		{name: "spec-like 6599 newlines trailing", content: strings.Repeat("x\n", 6599), want: 6599},
		{name: "6599 lines no trailing newline", content: strings.Repeat("x\n", 6598) + "x", want: 6599},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lineCount(tt.content); got != tt.want {
				t.Fatalf("lineCount(%q): got %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}
