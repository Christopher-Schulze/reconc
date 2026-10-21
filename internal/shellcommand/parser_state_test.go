package shellcommand

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestParserStateReusePreservesEarlierAST(t *testing.T) {
	state := newParserState()
	first, err := state.parse("printf first", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.parse("printf second", "second")
	if err != nil {
		t.Fatal(err)
	}

	var firstText, secondText bytes.Buffer
	printer := syntax.NewPrinter()
	if err := printer.Print(&firstText, first); err != nil {
		t.Fatal(err)
	}
	if err := printer.Print(&secondText, second); err != nil {
		t.Fatal(err)
	}
	if firstText.String() != "printf first\n" || secondText.String() != "printf second\n" {
		t.Fatalf("parser reuse changed AST output: first=%q second=%q", firstText.String(), secondText.String())
	}
}

func TestInvocationsWithReasonIsSafeAcrossConcurrentAnalyses(t *testing.T) {
	const command = `env -- bash -lc 'printf ready && git status && echo "$(git diff)"'`
	want, reason := InvocationsWithReason(command, 16)
	if reason != IncompleteNone {
		t.Fatalf("reference analysis reason = %q", reason)
	}

	const workers = 16
	errs := make(chan string, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			got, gotReason := InvocationsWithReason(command, 16)
			if gotReason != IncompleteNone {
				errs <- "analysis became incomplete"
				return
			}
			if len(got) != len(want) {
				errs <- "concurrent analysis returned a different invocation count"
				return
			}
			for index := range want {
				if got[index].Source != want[index].Source || len(got[index].Words) != len(want[index].Words) {
					errs <- "concurrent analysis returned different invocation data"
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// The analyses one hook request runs on a command share one parse of it; a
// command above the sharing bound is parsed by each analysis.
func TestAnalysesOfOneCommandShareOneParse(t *testing.T) {
	previous := parseShellCommand
	t.Cleanup(func() { parseShellCommand = previous })
	parses := map[string]int{}
	var mu sync.Mutex
	parseShellCommand = func(state *parserState, command, name string) (*syntax.File, error) {
		mu.Lock()
		parses[command]++
		mu.Unlock()
		return previous(state, command, name)
	}
	analyze := func(command string) {
		InvocationsWithReason(command, 16)
		HasRedirect(command)
		HasFileWriteRedirect(command)
		StripTrailingRedirects(command)
		LiteralWriteTargets(command, 16)
	}
	shared := "sort shared-parse-input.txt > shared-parse-output.txt"
	analyze(shared)
	large := "printf '%s' '" + strings.Repeat("x", recentParseMaxBytes) + "' > large-output.txt"
	analyze(large)
	if parses[shared] != 1 || parses[large] < 5 {
		t.Fatalf("parses: shared command %d (want 1), command above the bound %d (want one per analysis)", parses[shared], parses[large])
	}
}
