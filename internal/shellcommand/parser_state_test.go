package shellcommand

import (
	"bytes"
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
