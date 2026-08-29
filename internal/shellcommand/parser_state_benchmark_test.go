package shellcommand

import (
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

const benchmarkShellCommand = `env MODE=1 sudo -- git status && printf '%s\n' "$(git diff --stat)"`

func BenchmarkParserConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = newParserState()
	}
}

func BenchmarkParserParse(b *testing.B) {
	state := newParserState()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := state.parse(benchmarkShellCommand, "benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParserASTWalk(b *testing.B) {
	state := newParserState()
	file, err := state.parse(benchmarkShellCommand, "benchmark")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		nodes := 0
		syntax.Walk(file, func(node syntax.Node) bool {
			if node != nil {
				nodes++
			}
			return true
		})
		if nodes == 0 {
			b.Fatal("AST walk visited no nodes")
		}
	}
}

func BenchmarkCallerAnalysisCosts(b *testing.B) {
	b.Run("invocations", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, reason := InvocationsWithReason(benchmarkShellCommand, 16); reason != IncompleteNone {
				b.Fatal(reason)
			}
		}
	})
	b.Run("redirects", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, complete := StripTrailingRedirects(benchmarkShellCommand + " 2>&1"); !complete {
				b.Fatal("redirect analysis became incomplete")
			}
		}
	})
	b.Run("combined-callers", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, reason := InvocationsWithReason(benchmarkShellCommand, 16); reason != IncompleteNone {
				b.Fatal(reason)
			}
			if _, complete := StripTrailingRedirects(benchmarkShellCommand + " 2>&1"); !complete {
				b.Fatal("redirect analysis became incomplete")
			}
		}
	})
}
