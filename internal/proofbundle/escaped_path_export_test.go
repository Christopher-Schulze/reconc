package proofbundle_test

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policyproof"
	"reconc.dev/reconc/internal/proofbundle"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestGeneratedProofRedactsEscapedQuotedPathSuffixes(t *testing.T) {
	repo := proofRepo(t, `rules:
  - id: denied-generated
    kind: deny_write
    paths: [gen/**]
    mode: block
    message: 'error: \"/srv/Private Client/report.txt\"'
`, map[string]string{"src/main.go": "package main\n"})
	initGit(t, repo)
	state, err := agentsession.CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"gen/output.go"}
	blocked, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(repo, "check", state.Fingerprint, blocked); err != nil {
		t.Fatal(err)
	}
	bundle, err := proofbundle.Generate(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	body, err := proofbundle.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var markdown bytes.Buffer
	if err := proofbundle.RenderMarkdown(&markdown, bundle); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(body), markdown.String()} {
		if !strings.Contains(output, "policy/unresolved/denied-generated") || !strings.Contains(output, "external") {
			t.Fatalf("real denial evidence missing: %s", output)
		}
		if strings.Contains(output, "Private") || strings.Contains(output, "Client/report.txt") {
			t.Fatalf("verified proof leaks private path suffix: %s", output)
		}
	}
}
