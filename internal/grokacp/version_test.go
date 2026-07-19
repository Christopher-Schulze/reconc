package grokacp

import "testing"

func TestSupportsNativeStopGate(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "minimum", version: "0.2.106", want: true},
		{name: "cli output", version: "grok 0.2.106 (ba76b0a) [stable]", want: true},
		{name: "prefixed", version: "v0.2.107", want: true},
		{name: "new minor", version: "0.3.0", want: true},
		{name: "new major", version: "1.0.0", want: true},
		{name: "older", version: "0.2.105", want: false},
		{name: "unrelated digits", version: "build 2106", want: false},
		{name: "malformed", version: "development", want: false},
		{name: "empty", version: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SupportsNativeStopGate(test.version); got != test.want {
				t.Fatalf("SupportsNativeStopGate(%q) = %t, want %t", test.version, got, test.want)
			}
		})
	}
}
