package mcpgateway

import (
	"bytes"
	"context"
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

func TestValidateConfigRejectsIncompleteOrAmbiguousLaunch(t *testing.T) {
	valid := Config{
		Repository: "/repository", ServerLabel: "server", Principal: "operator",
		PolicyAuthority: actionstate.PolicyAuthority{
			Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: strings.Repeat("a", 64),
		},
		Command: "/tool", Version: "test", CallTimeout: time.Second,
		Input: bytes.NewReader(nil), Output: io.Discard, Diagnostics: io.Discard,
		PolicyLoader: staticPolicyLoader{},
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing repository", mutate: func(config *Config) { config.Repository = "" }},
		{name: "missing server", mutate: func(config *Config) { config.ServerLabel = "" }},
		{name: "missing principal", mutate: func(config *Config) { config.Principal = "" }},
		{name: "missing command", mutate: func(config *Config) { config.Command = "" }},
		{name: "missing version", mutate: func(config *Config) { config.Version = "" }},
		{name: "missing input", mutate: func(config *Config) { config.Input = nil }},
		{name: "missing output", mutate: func(config *Config) { config.Output = nil }},
		{name: "missing diagnostics", mutate: func(config *Config) { config.Diagnostics = nil }},
		{name: "missing loader", mutate: func(config *Config) { config.PolicyLoader = nil }},
		{name: "missing authority", mutate: func(config *Config) { config.PolicyAuthority = actionstate.PolicyAuthority{} }},
		{name: "short timeout", mutate: func(config *Config) { config.CallTimeout = time.Nanosecond }},
		{name: "long timeout", mutate: func(config *Config) { config.CallTimeout = MaximumCallTimeout + time.Nanosecond }},
		{name: "approval registry only", mutate: func(config *Config) { config.ApprovalAuthorities = "/registry" }},
		{name: "approval policy only", mutate: func(config *Config) { config.ApprovalPolicyID = "policy" }},
	}
	if err := validateConfig(valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if err := validateConfig(config); err == nil {
				t.Fatal("invalid gateway launch was accepted")
			}
		})
	}
}

func TestValidatePolicySnapshotBindsCanonicalPlanAndDigests(t *testing.T) {
	repository, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan, evaluator := testGatewayPlan(t, action.DecisionAllow)
	valid := PolicySnapshot{
		Repository: repository, Plan: plan, Evaluator: evaluator,
		SourceDigest: strings.Repeat("a", 64), LockDigest: strings.Repeat("b", 64),
	}
	tests := []struct {
		name   string
		mutate func(*PolicySnapshot)
	}{
		{name: "missing repository", mutate: func(snapshot *PolicySnapshot) { snapshot.Repository = "" }},
		{name: "relative repository", mutate: func(snapshot *PolicySnapshot) { snapshot.Repository = "." }},
		{name: "missing plan", mutate: func(snapshot *PolicySnapshot) { snapshot.Plan = nil }},
		{name: "missing evaluator", mutate: func(snapshot *PolicySnapshot) { snapshot.Evaluator = nil }},
		{name: "uppercase source digest", mutate: func(snapshot *PolicySnapshot) { snapshot.SourceDigest = strings.Repeat("A", 64) }},
		{name: "short lock digest", mutate: func(snapshot *PolicySnapshot) { snapshot.LockDigest = strings.Repeat("b", 63) }},
	}
	if err := validatePolicySnapshot(valid); err != nil {
		t.Fatalf("valid policy snapshot: %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := valid
			test.mutate(&snapshot)
			if err := validatePolicySnapshot(snapshot); err == nil {
				t.Fatal("invalid policy snapshot was accepted")
			}
		})
	}
}

func TestSelectedEnvironmentIsExplicitSortedAndDuplicateFree(t *testing.T) {
	first := "RECONC_TEST_GATEWAY_ENV_FIRST"
	second := "RECONC_TEST_GATEWAY_ENV_SECOND"
	t.Setenv(first, "one")
	t.Setenv(second, "two")
	bindings, environment, err := selectedEnvironment([]string{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[0].Name != first || bindings[1].Name != second ||
		len(environment) != 2 || environment[0] != first+"=one" || environment[1] != second+"=two" {
		t.Fatalf("selected environment = %#v, %#v", bindings, environment)
	}
	if _, _, err := selectedEnvironment([]string{first, first}); err == nil {
		t.Fatal("duplicate environment name was accepted")
	}
	missing := filepath.Base(t.TempDir())
	if _, exists := os.LookupEnv(missing); exists {
		t.Fatalf("test environment %q unexpectedly exists", missing)
	}
	if _, _, err := selectedEnvironment([]string{missing}); err == nil {
		t.Fatal("missing environment name was accepted")
	}
}

func TestTerminalContextDetachesOnlyBoundedCleanup(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	terminal, terminalCancel := terminalContext(cancelled)
	defer terminalCancel()
	if terminal.Err() != nil {
		t.Fatalf("terminal context inherited cancellation: %v", terminal.Err())
	}
	deadline, bounded := terminal.Deadline()
	if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > CancellationGrace {
		t.Fatalf("terminal context deadline = %v, bounded=%t", deadline, bounded)
	}
	live := context.Background()
	reused, reusedCancel := terminalContext(live)
	defer reusedCancel()
	if reused != live {
		t.Fatal("unbounded live context was needlessly replaced")
	}
}
