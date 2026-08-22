package shellcommand

import "testing"

func TestStripTrailingRedirectsUsesShellSyntax(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"go test ./... 2>&1", "go test ./..."},
		{"go test ./... > /dev/null 2>&1", "go test ./..."},
		{"go test ./... | tail -5", "go test ./... | tail -5"},
		{`go test ./... "2>&1"`, `go test ./... "2>&1"`},
		{`go test ./... '> out.log'`, `go test ./... '> out.log'`},
		{`go test ./... \> out.log`, `go test ./... \> out.log`},
		{`go test ./... "literal > out.log"`, `go test ./... "literal > out.log"`},
	}
	for _, test := range tests {
		got, complete := StripTrailingRedirects(test.command)
		if !complete || got != test.want {
			t.Fatalf("StripTrailingRedirects(%q) = %q, %v; want %q, true", test.command, got, complete, test.want)
		}
	}
}

func FuzzStripTrailingRedirectsIsIdempotent(f *testing.F) {
	for _, seed := range []string{
		"go test ./... 2>&1",
		`go test ./... "literal > out.log"`,
		`go test ./... \> out.log`,
		"go test ./... | tail",
		"0>0",
		"!>0",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, command string) {
		first, complete := StripTrailingRedirects(command)
		if !complete {
			return
		}
		second, secondComplete := StripTrailingRedirects(first)
		if !secondComplete || second != first {
			t.Fatalf("redirect stripping is not idempotent: %q -> %q -> %q (%v)", command, first, second, secondComplete)
		}
	})
}
