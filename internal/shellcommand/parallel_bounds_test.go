package shellcommand

import (
	"fmt"
	"testing"
)

func TestParallelOptionReplacementsRemainIncomplete(t *testing.T) {
	for _, option := range []string{"--tagstring", "--tag-string", "--workdir", "--wd", "--results", "--retries"} {
		for _, separator := range []string{" ", "="} {
			for _, value := range []string{`{= system("git status"); =}`, "{1}", "prefix{#}"} {
				command := fmt.Sprintf("parallel %s%s'%s' echo ::: .", option, separator, value)
				t.Run(command, func(t *testing.T) {
					if _, reason := InvocationsWithReason(command, 16); reason != IncompleteDynamicCommand {
						t.Fatalf("replacement option received complete analysis: %q", reason)
					}
				})
			}
		}
	}
}

func TestParallelInnerCommandUsesDepthBudget(t *testing.T) {
	for _, command := range []string{"parallel echo ::: .", "parallel -q echo ::: ."} {
		t.Run(command, func(t *testing.T) {
			if _, reason := InvocationsWithReason(command, 0); reason != IncompleteNestingDepth {
				t.Fatalf("zero depth budget: reason=%q", reason)
			}
			invocations, complete := Invocations(command, 16)
			if !complete {
				t.Fatal("literal command did not resolve with sufficient depth")
			}
			for _, invocation := range invocations {
				if len(invocation.Words) == 0 || invocation.Words[0] != "echo" {
					continue
				}
				matched, uncertain := Match(invocation, "echo", false)
				if matched || !uncertain {
					t.Fatalf("runtime-appended input lost: matched=%t uncertain=%t", matched, uncertain)
				}
				return
			}
			t.Fatal("inner echo invocation missing")
		})
	}
}
