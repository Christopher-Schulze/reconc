package mcpgateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	processEnvironmentProbeArgument = "reconc-process-environment-probe"
	processEnvironmentSecret        = "RECONC_TEST_PROCESS_ENVIRONMENT_SECRET"
	processEnvironmentSelected      = "RECONC_TEST_PROCESS_ENVIRONMENT_SELECTED"
)

func TestOwnedProcessEnvironmentIsEmptyUnlessExplicitlySelected(t *testing.T) {
	t.Setenv(processEnvironmentSecret, "must-not-leak")
	t.Setenv(processEnvironmentSelected, "selected-value")
	for _, test := range []struct {
		name          string
		selectedNames []string
		wantSelected  string
	}{
		{name: "empty by default"},
		{name: "explicit selection only", selectedNames: []string{processEnvironmentSelected}, wantSelected: "selected-value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := newPrivateGatewayHome(t)
			lease, err := actionstate.AcquireIdentityKey(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			workingDirectory := t.TempDir()
			resultPath := filepath.Join(workingDirectory, "environment-result")
			arguments := []string{
				"-test.run=^TestOwnedProcessEnvironmentProbe$", "--",
				processEnvironmentProbeArgument, resultPath,
			}
			bindings, environment, err := selectedEnvironment(test.selectedNames)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := actionstate.ObserveServer(
				lease.Key, executable, arguments, workingDirectory, bindings,
			)
			if err != nil {
				t.Fatal(err)
			}
			process, err := startOwnedProcess(observed, arguments, environment)
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Wait(); err != nil {
				_ = process.Close()
				t.Fatal(err)
			}
			if err := process.Close(); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(resultPath)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("secret=false\nselected=%s\n", test.wantSelected)
			if string(body) != want {
				t.Fatalf("child environment = %q, want %q", body, want)
			}
		})
	}
}

func TestOwnedProcessEnvironmentProbe(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == processEnvironmentProbeArgument {
			separator = index
			break
		}
	}
	if separator < 0 {
		return
	}
	if separator+1 >= len(os.Args) {
		t.Fatal("environment probe result path is absent")
	}
	_, secretExists := os.LookupEnv(processEnvironmentSecret)
	body := fmt.Sprintf(
		"secret=%t\nselected=%s\n", secretExists, os.Getenv(processEnvironmentSelected),
	)
	if err := os.WriteFile(os.Args[separator+1], []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayRejectsInvalidChildStartupWithoutLeakingProtocolErrors(t *testing.T) {
	for _, mode := range []string{
		"malformed-json", "stdout-flood", "private-initialize-error", "private-list-error",
	} {
		t.Run(mode, func(t *testing.T) {
			repository, err := pathidentity.ResolveExisting(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := newPrivateGatewayHome(t)
			plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(fakeProcessEnvironment, "1")
			t.Setenv(fakeModeEnvironment, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			gateway, err := startGateway(ctx, Config{
				Repository: repository, ServerLabel: "fake", Principal: "test-operator",
				PolicyAuthority: actionstate.PolicyAuthority{
					Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: strings.Repeat("b", 64),
				},
				Command: executable, Arguments: []string{"-test.run=^TestMCPGatewayFakeProcess$"},
				InheritedEnvNames: []string{fakeModeEnvironment, fakeProcessEnvironment},
				ReconcHome:        home, Version: "test", CallTimeout: time.Second,
				Input: bytes.NewReader(nil), Output: io.Discard, Diagnostics: io.Discard,
				PolicyLoader: staticPolicyLoader{snapshot: PolicySnapshot{
					Repository: repository, Evaluator: evaluator, Plan: plan,
					SourceDigest: strings.Repeat("a", 64), LockDigest: strings.Repeat("b", 64),
				}},
			})
			if gateway != nil {
				_ = gateway.Close()
				t.Fatal("invalid child stdout produced a running gateway")
			}
			if err == nil || ctx.Err() != nil {
				t.Fatalf("startup error = %v, context = %v", err, ctx.Err())
			}
			if strings.Contains(err.Error(), fakePrivateProtocolError) {
				t.Fatalf("startup error leaked downstream protocol content: %v", err)
			}
		})
	}
}
