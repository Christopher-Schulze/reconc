package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/pathidentity"
	productruntime "reconc.dev/reconc/internal/runtime"
)

func TestParseMCPGatewayOptionsKeepsDownstreamArgumentsOpaque(t *testing.T) {
	options, err := parseMCPGatewayOptions([]string{
		"repo", "--server", "tools", "--principal", "operator",
		"--allow-repository-managed-policy", "--", "/bin/tool", "--", "--server", "child",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.repository != "repo" || options.command != "/bin/tool" ||
		!reflect.DeepEqual(options.arguments, []string{"--", "--server", "child"}) {
		t.Fatalf("parsed gateway launch = %#v", options)
	}
}

func TestParseMCPGatewayOptionsRejectsAmbiguousAuthorityAndUnknownFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "both policy authorities",
			args: []string{
				"--server", "tools", "--principal", "operator",
				"--expect-lock-digest", strings.Repeat("a", 64),
				"--allow-repository-managed-policy", "--", "/bin/tool",
			},
			want: "exactly one",
		},
		{
			name: "unknown flag",
			args: []string{"--unknown", "--", "/bin/tool"},
			want: "unknown flag",
		},
		{
			name: "missing separator",
			args: []string{
				"--server", "tools", "--principal", "operator",
				"--allow-repository-managed-policy",
			},
			want: "required after --",
		},
		{
			name: "duplicated single-value flag",
			args: []string{
				"--server", "tools", "--server", "other", "--principal", "operator",
				"--allow-repository-managed-policy", "--", "/bin/tool",
			},
			want: "flag --server is duplicated",
		},
		{
			name: "flag consumed as another flag value",
			args: []string{
				"--server", "tools", "--principal", "operator", "--role",
				"--allow-repository-managed-policy", "--expect-lock-digest", strings.Repeat("a", 64),
				"--", "/bin/tool",
			},
			want: "--role requires a value before --allow-repository-managed-policy",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseMCPGatewayOptions(test.args); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMCPGatewayHelpDoesNotStartGateway(t *testing.T) {
	stdout := &bytes.Buffer{}
	if err := runMCP([]string{"gateway", "--help"}, "test", stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "Usage: reconc mcp gateway") {
		t.Fatalf("gateway help = %q", stdout.String())
	}
}

func TestMCPGatewayRunErrorPreservesCleanupFailures(t *testing.T) {
	failures := []struct {
		name string
		err  error
	}{
		{name: "gateway close", err: errors.New("close gateway transport failed")},
		{name: "evidence finalize", err: errors.New("finalize evidence failed")},
		{name: "child process", err: errors.New("close child process failed")},
		{name: "lease release", err: errors.New("release gateway lease failed")},
	}
	for _, test := range failures {
		t.Run(test.name+" alone", func(t *testing.T) {
			err := mcpGatewayRunError(test.err)
			if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), test.err.Error()) {
				t.Fatalf("cleanup error = %v", err)
			}
		})
		t.Run(test.name+" joined with cancellation", func(t *testing.T) {
			joined := errors.Join(context.Canceled, test.err)
			err := mcpGatewayRunError(joined)
			if err == nil || ExitCode(err) != 1 ||
				!strings.Contains(err.Error(), context.Canceled.Error()) ||
				!strings.Contains(err.Error(), test.err.Error()) {
				t.Fatalf("joined cleanup error = %v", err)
			}
		})
	}
}

func TestMCPGatewayRunErrorTreatsPureCancellationAsClean(t *testing.T) {
	cases := []error{
		context.Canceled,
		fmt.Errorf("serve stopped: %w", context.Canceled),
		errors.Join(context.Canceled, fmt.Errorf("wrapped cancellation: %w", context.Canceled)),
	}
	for _, test := range cases {
		if err := mcpGatewayRunError(test); err != nil {
			t.Fatalf("pure cancellation = %v", err)
		}
	}
	if err := mcpGatewayRunError(errors.Join(context.Canceled, context.DeadlineExceeded)); err == nil {
		t.Fatal("mixed cancellation and deadline was treated as clean")
	}
}

func TestGatewayConfigSharesRuntimeEvaluatorAcrossPolicyBoundaries(t *testing.T) {
	config := gatewayConfig(
		mcpGatewayOptions{
			repository: ".", serverLabel: "server", principal: "operator",
			repositoryManaged: true, command: "/tool",
		},
		"test", &bytes.Buffer{}, &bytes.Buffer{},
	)
	loader, ok := config.PolicyLoader.(gatewayPolicyLoader)
	if !ok {
		t.Fatalf("gateway policy loader = %T", config.PolicyLoader)
	}
	provider, ok := config.EvidenceProvider.(gatewayEvidenceProvider)
	if !ok {
		t.Fatalf("gateway evidence provider = %T", config.EvidenceProvider)
	}
	if loader.evaluator == nil || provider.evaluator == nil || loader.evaluator != provider.evaluator {
		t.Fatalf("gateway runtime evaluator ownership is not shared: loader=%p provider=%p", loader.evaluator, provider.evaluator)
	}
}

func TestGatewayPolicyLoaderReusesCompiledPlanAndBindsRepositoryCheck(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policyText := "rules:\n  - id: deny-source\n    kind: deny_write\n    paths: ['src/**']\n    mode: block\n    message: denied\n"
	if err := os.WriteFile(filepath.Join(repository, ".reconc.yml"), []byte(policyText), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repository, "gateway-evaluator-test"); err != nil {
		t.Fatal(err)
	}
	runtimeEvaluator := productruntime.NewEvaluator()
	loader := gatewayPolicyLoader{evaluator: runtimeEvaluator}
	first, err := loader.Load(context.Background(), repository)
	if err != nil {
		t.Fatalf("first gateway policy load: %v", err)
	}
	second, err := loader.Load(context.Background(), repository)
	if err != nil {
		t.Fatalf("second gateway policy load: %v", err)
	}
	if first.Plan != second.Plan {
		t.Fatalf("gateway policy loads rebuilt the immutable plan: first=%p second=%p", first.Plan, second.Plan)
	}
	if first.RepositoryEffectCheck == nil || second.RepositoryEffectCheck == nil {
		t.Fatal("gateway policy snapshot did not bind a repository-effect check")
	}

	arguments, err := action.ParseObjectJSON([]byte(`{"path":"src/main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := (gatewayEvidenceProvider{evaluator: runtimeEvaluator}).Observe(
		context.Background(), first,
		action.Request{Arguments: &arguments},
		action.Tool{Effect: action.Effect{
			Kind: action.EffectRepositoryWrite, PathFields: []string{"/path"},
		}},
	)
	if err != nil {
		t.Fatalf("repository-effect evidence: %v", err)
	}
	if evidence.RepositoryEffect == nil || evidence.RepositoryEffect.Decision != action.DecisionBlock {
		t.Fatalf("repository-effect decision = %#v", evidence.RepositoryEffect)
	}

	updated := strings.Replace(policyText, "src/**", "dist/**", 1)
	if err := os.WriteFile(filepath.Join(repository, ".reconc.yml"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background(), repository); err == nil ||
		(!strings.Contains(err.Error(), "source_digest") && !strings.Contains(err.Error(), "source_count")) {
		t.Fatalf("policy mutation was not rejected by the shared evaluator: %v", err)
	}
}

func TestGatewayRepositoryEffectCheckIsConcurrentSafe(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".reconc.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repository, "gateway-evaluator-race-test"); err != nil {
		t.Fatal(err)
	}
	runtimeEvaluator := productruntime.NewEvaluator()
	snapshot, err := (gatewayPolicyLoader{evaluator: runtimeEvaluator}).Load(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := action.ParseObjectJSON([]byte(`{"path":"src/main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	provider := gatewayEvidenceProvider{evaluator: runtimeEvaluator}
	const workers = 16
	var group sync.WaitGroup
	group.Add(workers)
	errors := make(chan error, workers)
	for range workers {
		go func() {
			defer group.Done()
			evidence, observeErr := provider.Observe(
				context.Background(), snapshot,
				action.Request{Arguments: &arguments},
				action.Tool{Effect: action.Effect{
					Kind: action.EffectRepositoryRead, PathFields: []string{"/path"},
				}},
			)
			if observeErr != nil {
				errors <- observeErr
				return
			}
			if evidence.RepositoryEffect == nil || evidence.RepositoryEffect.Decision != action.DecisionAllow {
				errors <- fmt.Errorf("unexpected concurrent repository-effect evidence: %#v", evidence.RepositoryEffect)
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

func TestNormalizeGatewayPathsReturnsExactLexicalBindings(t *testing.T) {
	repository, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repository, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repository, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink test is unavailable: %v", err)
	}
	paths, bindings, err := normalizeGatewayPaths(repository, []string{"link/future"})
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := filepath.Join(target, "future")
	if !reflect.DeepEqual(paths, []string{"target/future"}) || len(bindings) != 1 ||
		bindings[0].Lexical != filepath.Join(link, "future") ||
		bindings[0].Identity != wantIdentity {
		t.Fatalf("normalized paths = %#v, bindings = %#v", paths, bindings)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if _, _, err := normalizeGatewayPaths(repository, []string{outside}); err == nil {
		t.Fatal("lexical path outside the repository was accepted")
	}
}
