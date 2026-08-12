package mcpgateway

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/pathidentity"
)

func TestRepositoryPathBindingsRejectSymlinkDriftAndLexicalEscape(t *testing.T) {
	repository, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(repository, "first")
	second := filepath.Join(repository, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repository, "link")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlink test is unavailable: %v", err)
	}
	lexical := filepath.Join(link, "future")
	identity, err := pathidentity.ResolveProspective(lexical)
	if err != nil {
		t.Fatal(err)
	}
	binding := RepositoryPathBinding{Lexical: lexical, Identity: identity}
	if err := validateRepositoryPathBindings(repository, []RepositoryPathBinding{binding}); err != nil {
		t.Fatalf("stable binding: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if err := validateRepositoryPathBindings(repository, []RepositoryPathBinding{binding}); err == nil {
		t.Fatal("changed symlink identity was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	outsideIdentity, err := pathidentity.ResolveProspective(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRepositoryPathBindings(repository, []RepositoryPathBinding{{
		Lexical: outside, Identity: outsideIdentity,
	}}); err == nil {
		t.Fatal("lexical path outside the repository was accepted")
	}
}

func TestBindRepositoryEffectIsOrderIndependentAndPathBound(t *testing.T) {
	markers := t.TempDir()
	t.Setenv(fakeProcessEnvironment, "1")
	t.Setenv(fakeMarkerEnvironment, filepath.Join(markers, "invoked"))
	t.Setenv(fakeModeEnvironment, "normal")
	t.Setenv(fakeCancellationMarkerEnvironment, filepath.Join(markers, "cancelled"))
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	harness := newRawGatewayHarness(t, plan, evaluator)
	repository := harness.gateway.snapshot.Repository
	lexical := filepath.Join(repository, "future.txt")
	identity, err := pathidentity.ResolveProspective(lexical)
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := action.ParseObjectJSON([]byte(`{"path":"future.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	request := action.Request{
		Arguments: &arguments, ToolContractDigest: "sha256:" + strings.Repeat("a", 64),
	}
	tool := action.Tool{ID: "echo-tool"}
	binding := RepositoryPathBinding{Lexical: lexical, Identity: identity}
	first := &action.RepositoryEffectCandidate{
		Decision: action.DecisionAllow, Reason: action.ReasonRuleMatched,
		RuleIDs: []string{"rule-b", "rule-a"}, Complete: true,
	}
	if err := harness.gateway.bindRepositoryEffect(request, tool, first, []RepositoryPathBinding{binding}); err != nil {
		t.Fatal(err)
	}
	if !action.ValidKeyedIdentity(first.Identity) || !reflect.DeepEqual(first.RuleIDs, []string{"rule-a", "rule-b"}) {
		t.Fatalf("bound repository effect = %#v", first)
	}
	second := &action.RepositoryEffectCandidate{
		Decision: action.DecisionAllow, Reason: action.ReasonRuleMatched,
		RuleIDs: []string{"rule-a", "rule-b"}, Complete: true,
	}
	if err := harness.gateway.bindRepositoryEffect(request, tool, second, []RepositoryPathBinding{binding}); err != nil {
		t.Fatal(err)
	}
	if second.Identity != first.Identity {
		t.Fatalf("repository effect identity changed with input order: %s != %s", second.Identity, first.Identity)
	}
	invalid := &action.RepositoryEffectCandidate{
		Decision: action.DecisionAllow, Reason: action.ReasonRuleMatched,
		RuleIDs: []string{"duplicate", "duplicate"}, Complete: true,
	}
	if err := harness.gateway.bindRepositoryEffect(request, tool, invalid, []RepositoryPathBinding{binding}); err == nil {
		t.Fatal("duplicate repository-effect rule identity was accepted")
	}
}
