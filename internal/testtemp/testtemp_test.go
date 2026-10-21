package testtemp

import "testing"

// Only the top-level process of a cacheable go test run receives the test log
// flag; re-executed helper binaries never do.
func TestWritesTestLogRecognizesOnlyTheGoTestFlag(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      bool
	}{
		{[]string{"-test.paniconexit0", "-test.testlogfile=/tmp/testlog.txt", "-test.timeout=10m0s"}, true},
		{[]string{"-test.testlogfile", "/tmp/testlog.txt"}, true},
		{[]string{"-test.run=^TestMCPGatewayFakeProcess$"}, false},
		{[]string{"-test.testlogfilex=/tmp/testlog.txt"}, false},
		{nil, false},
	} {
		if got := writesTestLog(test.arguments); got != test.want {
			t.Fatalf("writesTestLog(%q) = %t, want %t", test.arguments, got, test.want)
		}
	}
}
