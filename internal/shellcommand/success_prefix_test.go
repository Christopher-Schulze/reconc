package shellcommand

import "testing"

func TestExtendsSuccessArguments(t *testing.T) {
	tests := []struct {
		expected string
		command  string
		want     bool
	}{
		{"go test ./...", "go test ./... -run TestFoo", true},
		{"cd api && go test ./...", "cd api && go test ./... -count=1", true},
		{"go test ./...", `go test ./... '|| true'`, true},
		{"go test ./...", `go test ./... \&`, true},
		{"go test ./...", "go test ./... || true", false},
		{"go test ./...", "go test ./... | tail", false},
		{"go test ./...", "go test ./... ; true", false},
		{"go test ./...", "go test ./... &", false},
		{"go test ./...", "go test ./... && true", false},
		{"go test ./...", "go test ./... $FLAGS", false},
		{"go test ./...", "go test ./... $(true)", false},
		{"go test ./...", "go test ./... 'broken", false},
		{"", "go test ./...", false},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			if got := ExtendsSuccessArguments(test.command, test.expected); got != test.want {
				t.Fatalf("extension of %q = %t, want %t", test.expected, got, test.want)
			}
		})
	}
}
