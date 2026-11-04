package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestUserApprovedClaimRecordsBoundedGrantWithoutTerminal(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if _, err := agentsession.InitializeSessionState(repo, "approved"); err != nil {
		t.Fatal(err)
	}
	previous := confirmOperatorPresence
	confirmOperatorPresence = func(string, io.Writer) error { return fmt.Errorf("terminal must not be requested") }
	t.Cleanup(func() { confirmOperatorPresence = previous })
	var stdout, stderr bytes.Buffer
	started := time.Now()
	err := Run([]string{"hook", "claim", repo, "operator:edit-protected", "--user-approved", "--for", "1h", "--session", "approved", "--json"}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("chat-approved claim: %v; %s", err, stderr.String())
	}
	var report agentsession.ClaimReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339, report.ExpiresAt)
	if err != nil || expires.Before(started.Add(time.Hour-time.Second)) || expires.After(time.Now().Add(time.Hour)) {
		t.Fatalf("grant expiry = %q, %v", report.ExpiresAt, err)
	}
	claims, err := agentsession.ActiveClaims(repo)
	if err != nil || !slices.Contains(claims, "operator:edit-protected") {
		t.Fatalf("recorded claim is not effective: %v, %v", claims, err)
	}
	if _, err := agentsession.RevokeClaim(repo, "operator:edit-protected", "approved"); err != nil {
		t.Fatal(err)
	}
	claims, err = agentsession.ActiveClaims(repo)
	if err != nil || len(claims) != 0 {
		t.Fatalf("revoked claim remains effective: %v, %v", claims, err)
	}
}

func TestUserApprovedClaimRejectsInvalidAttestationsWithoutStateChanges(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	for _, tc := range []struct {
		name  string
		claim string
		flags []string
	}{
		{"no duration", "operator:edit", nil},
		{"ordinary claim", "ci-green", []string{"--for", "1h"}},
		{"revoke", "operator:edit", []string{"--for", "1h", "--revoke"}},
		{"empty duration", "operator:edit", []string{"--for", ""}},
		{"zero duration", "operator:edit", []string{"--for", "0s"}},
		{"below minimum", "operator:edit", []string{"--for", "59s"}},
		{"above maximum", "operator:edit", []string{"--for", "8h1s"}},
		{"invalid duration", "operator:edit", []string{"--for", "later"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if _, err := agentsession.InitializeSessionState(repo, "invalid"); err != nil {
				t.Fatal(err)
			}
			args := []string{"hook", "claim", repo, tc.claim, "--user-approved", "--session", "invalid"}
			args = append(args, tc.flags...)
			var stdout, stderr bytes.Buffer
			if err := Run(args, "test", &stdout, &stderr); err == nil {
				t.Fatal("invalid attestation was accepted")
			}
			state, err := agentsession.LoadSessionState(repo, "invalid")
			if err != nil || len(state.Claims) != 0 {
				t.Fatalf("rejected command mutated claims: %v, %v", state.Claims, err)
			}
			if strings.Contains(stderr.String(), "type ") {
				t.Fatal("invalid attestation asked for terminal confirmation")
			}
		})
	}
}

func TestUserApprovedClaimKeepsTheAttestedQuoteForReview(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if _, err := agentsession.InitializeSessionState(repo, "quoted"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"hook", "claim", repo, "operator:edit", "--user-approved", "--for", "1h", "--session", "quoted", "--json",
		"--approval-quote", "ja,\n  Diff\tfreigegeben"}, "dev", &stdout, &stderr)
	if err != nil {
		t.Fatalf("quoted claim: %v; %s", err, stderr.String())
	}
	var report agentsession.ClaimReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ApprovalQuote != "ja, Diff freigegeben" {
		t.Fatalf("quote must stay one reviewable line: %q", report.ApprovalQuote)
	}
	state, err := agentsession.LoadSessionState(repo, "quoted")
	if err != nil {
		t.Fatal(err)
	}
	if state.ClaimApprovalQuotes["operator:edit"] != report.ApprovalQuote {
		t.Fatalf("session state lost the quote: %+v", state.ClaimApprovalQuotes)
	}
	if !strings.Contains(agentsession.DescribeClaimReport(&report), "attested approval quote: ja, Diff freigegeben") {
		t.Fatalf("report text omits the quote: %s", agentsession.DescribeClaimReport(&report))
	}
}

func TestApprovalQuoteRequiresTheAttestationAndStaysBounded(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv(agentsession.StateRootEnv, t.TempDir())
	repo := t.TempDir()
	if _, err := agentsession.InitializeSessionState(repo, "bounded"); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no attestation": {"--for", "1h", "--approval-quote", "ja"},
		"blank":          {"--user-approved", "--for", "1h", "--approval-quote", " \n\t "},
		"too long":       {"--user-approved", "--for", "1h", "--approval-quote", strings.Repeat("a", agentsession.MaxClaimApprovalQuoteRunes+1)},
		"missing value":  {"--user-approved", "--for", "1h", "--approval-quote"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(append([]string{"hook", "claim", repo, "operator:edit", "--session", "bounded"}, args...), "dev", &stdout, &stderr)
			if err == nil {
				t.Fatal("an invalid quote must reject the whole claim")
			}
			state, loadErr := agentsession.LoadSessionState(repo, "bounded")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if slices.Contains(state.Claims, "operator:edit") || len(state.ClaimApprovalQuotes) != 0 {
				t.Fatalf("rejected claim changed state: %+v %+v", state.Claims, state.ClaimApprovalQuotes)
			}
		})
	}
}
