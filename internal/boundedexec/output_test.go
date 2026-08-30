package boundedexec

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

func TestBufferRetainsExactPrefixAndReportsTruncation(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		writes    []string
		want      string
		truncated bool
	}{
		{name: "below limit", limit: 5, writes: []string{"ab", "cd"}, want: "abcd"},
		{name: "exact limit", limit: 4, writes: []string{"ab", "cd"}, want: "abcd"},
		{name: "crosses limit", limit: 4, writes: []string{"ab", "cdef"}, want: "abcd", truncated: true},
		{name: "write after limit", limit: 2, writes: []string{"ab", "cd"}, want: "ab", truncated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := NewBuffer(test.limit)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range test.writes {
				written, writeErr := output.Write([]byte(value))
				if writeErr != nil || written != len(value) {
					t.Fatalf("Write(%q) = %d, %v", value, written, writeErr)
				}
			}
			if got := output.String(); got != test.want {
				t.Fatalf("retained prefix = %q, want %q", got, test.want)
			}
			if got := output.Truncated(); got != test.truncated {
				t.Fatalf("Truncated() = %t, want %t", got, test.truncated)
			}
		})
	}
}

func TestNewBufferRejectsNonPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := NewBuffer(limit); err == nil {
			t.Fatalf("NewBuffer(%d) succeeded", limit)
		}
	}
}

func TestBufferBytesReturnsStableCopy(t *testing.T) {
	output, err := NewBuffer(4)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = output.Write([]byte("abcd"))
	first := output.Bytes()
	first[0] = 'z'
	if got := output.String(); got != "abcd" {
		t.Fatalf("mutating returned bytes changed buffer to %q", got)
	}
}

func TestBufferConcurrentWritesPreserveAllAdmittedBytes(t *testing.T) {
	const (
		writers = 8
		writes  = 128
	)
	output, err := NewBuffer(writers * writes)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		group.Add(1)
		go func(value byte) {
			defer group.Done()
			for range writes {
				if written, writeErr := output.Write([]byte{value}); writeErr != nil || written != 1 {
					t.Errorf("concurrent Write = %d, %v", written, writeErr)
					return
				}
			}
		}(byte('a' + writer))
	}
	group.Wait()
	retained := output.Bytes()
	if len(retained) != writers*writes || output.Truncated() {
		t.Fatalf("retained=%d truncated=%t", len(retained), output.Truncated())
	}
	for writer := 0; writer < writers; writer++ {
		if count := strings.Count(string(retained), string([]byte{byte('a' + writer)})); count != writes {
			t.Fatalf("writer %d retained %d bytes, want %d", writer, count, writes)
		}
	}

	overflow, err := NewBuffer(writes)
	if err != nil {
		t.Fatal(err)
	}
	for writer := 0; writer < writers; writer++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = overflow.Write([]byte(strings.Repeat("x", writes)))
		}()
	}
	group.Wait()
	if got := len(overflow.Bytes()); got != writes || !overflow.Truncated() {
		t.Fatalf("concurrent overflow retained=%d truncated=%t", got, overflow.Truncated())
	}
}

func TestBufferOverflowWritesAllocateNothing(t *testing.T) {
	output, err := NewBuffer(1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = output.Write([]byte("x"))
	payload := []byte("discarded")
	allocations := testing.AllocsPerRun(1000, func() {
		_, _ = output.Write(payload)
	})
	if allocations != 0 {
		t.Fatalf("overflow write allocations = %f, want 0", allocations)
	}
	if got := output.String(); got != "x" || !output.Truncated() {
		t.Fatalf("overflow result = %q truncated=%t", got, output.Truncated())
	}
}

func TestCombinedOutputCapsChildOutput(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestBoundedExecHelper$")
	command.Env = append(os.Environ(), "RECONC_BOUNDED_EXEC_HELPER=combined")
	output, err := CombinedOutput(command, 1024)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("expected output-limit error, got %v", err)
	}
	if len(output) != 1024 {
		t.Fatalf("combined output length=%d, want 1024", len(output))
	}
}

func TestOutputReturnsExactStdout(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestBoundedExecHelper$")
	command.Env = append(os.Environ(), "RECONC_BOUNDED_EXEC_HELPER=stdout")
	output, err := Output(command, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "stdout" {
		t.Fatalf("output=%q", output)
	}
}

func TestOutputAccountsForStreamsIndependently(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestBoundedExecHelper$")
	command.Env = append(os.Environ(), "RECONC_BOUNDED_EXEC_HELPER=streams")
	output, err := Output(command, 1024)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("expected stderr output-limit error, got %v", err)
	}
	if got := string(output); got != strings.Repeat("x", 1024) {
		t.Fatalf("stdout prefix length=%d", len(got))
	}
}

func BenchmarkBufferCapture(b *testing.B) {
	payload := []byte(strings.Repeat("x", 4096))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for range b.N {
		output, err := NewBuffer(len(payload))
		if err != nil {
			b.Fatal(err)
		}
		_, _ = output.Write(payload)
	}
}

func BenchmarkBufferOverflowWrite(b *testing.B) {
	output, err := NewBuffer(1)
	if err != nil {
		b.Fatal(err)
	}
	_, _ = output.Write([]byte("x"))
	payload := []byte(strings.Repeat("y", 4096))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		_, _ = output.Write(payload)
	}
}

func TestBoundedExecHelper(t *testing.T) {
	switch os.Getenv("RECONC_BOUNDED_EXEC_HELPER") {
	case "combined":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 768))
		_, _ = os.Stderr.WriteString(strings.Repeat("y", 768))
		os.Exit(0)
	case "stdout":
		_, _ = os.Stdout.WriteString("stdout")
		_, _ = os.Stderr.WriteString("stderr")
		os.Exit(0)
	case "streams":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 1024))
		_, _ = os.Stderr.WriteString(strings.Repeat("y", 1025))
		os.Exit(0)
	}
}
