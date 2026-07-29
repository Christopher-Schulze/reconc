package safename

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid", input: " docs-sync ", want: "docs-sync"},
		{name: "single", input: "a", want: "a"},
		{name: "empty", input: "", wantErr: true},
		{name: "parent traversal", input: "../secret", wantErr: true},
		{name: "separator", input: "nested/name", wantErr: true},
		{name: "dot", input: "name.yml", wantErr: true},
		{name: "uppercase", input: "Name", wantErr: true},
		{name: "leading hyphen", input: "-name", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Normalize("template", test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("Normalize(%q) unexpectedly succeeded with %q", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
