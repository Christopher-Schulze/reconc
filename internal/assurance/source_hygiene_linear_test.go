package assurance

import (
	"bytes"
	"testing"
)

func TestSourceHygieneMarkerBoundariesAndOrder(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "folded marker", line: "// tOdO: FIXME later", want: "implementation-debt marker TODO"},
		{name: "marker order", line: "// TODO FIXME", want: "implementation-debt marker TODO"},
		{name: "overlap is not token", line: "// TODOFIXME", want: ""},
		{name: "identifier is not token", line: "// TODOList", want: ""},
		{name: "allowed delimiter", line: "// placeholder[reason]", want: "implementation-debt marker PLACEHOLDER"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sourceHygieneProblem(".go", []byte(test.line)); got != test.want {
				t.Fatalf("sourceHygieneProblem() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContainsCodeFoldHandlesMaximumLineWithoutChangingQuoteSemantics(t *testing.T) {
	prefix := bytes.Repeat([]byte{'x'}, 1<<20)
	line := append(prefix, []byte(` panic("not implemented")`)...)
	if !containsCodeFold(line, []byte(`panic("not implemented")`)) {
		t.Fatal("sentinel at the end of a large line was not found")
	}
	quoted := append(prefix, []byte(` "panic(\"not implemented\")"`)...)
	if containsCodeFold(quoted, []byte(`panic("not implemented")`)) {
		t.Fatal("sentinel inside a quoted example was reported")
	}
}

func BenchmarkContainsCodeFoldLargeLine(b *testing.B) {
	line := append(bytes.Repeat([]byte{'x'}, 1<<20), []byte(` panic("not implemented")`)...)
	target := []byte(`panic("not implemented")`)
	b.ReportAllocs()
	b.SetBytes(int64(len(line)))
	b.ResetTimer()
	for range b.N {
		if !containsCodeFold(line, target) {
			b.Fatal("sentinel not found")
		}
	}
}
