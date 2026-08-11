package actionstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

const (
	processHelperEnabled = "RECONC_ACTIONSTATE_PROCESS_HELPER"
	processHelperHome    = "RECONC_ACTIONSTATE_PROCESS_HOME"
	processHelperRepo    = "RECONC_ACTIONSTATE_PROCESS_REPOSITORY"
	processHelperVersion = "RECONC_ACTIONSTATE_PROCESS_VERSION"
	processHelperCall    = "RECONC_ACTIONSTATE_PROCESS_CALL"
	processHelperGate    = "RECONC_ACTIONSTATE_PROCESS_GATE"
)

func TestBudgetStoreProcessHelper(t *testing.T) {
	if os.Getenv(processHelperEnabled) != "1" {
		return
	}
	gate := os.Getenv(processHelperGate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("process reservation gate timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	home := os.Getenv(processHelperHome)
	repository := os.Getenv(processHelperRepo)
	lease, err := AcquireIdentityKey(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Errorf("close process identity-key lease: %v", err)
		}
	}()
	credential, err := CredentialIdentity(lease.Key, "warehouse", []byte("credential-secret"))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := (OperatorContext{
		Principal: "release-operator", Role: "operator", Environment: "production",
		Credentials: []CredentialBinding{{Label: "warehouse", Identity: credential}},
		ServerLabel: "server", RunID: "Run:42", SessionID: "session_7",
	}).Bind(lease.Key)
	if err != nil {
		t.Fatal(err)
	}
	resolved, repositoryIdentity, err := ObserveRepository(lease.Key, repository)
	if err != nil {
		t.Fatal(err)
	}
	server := testObservedServer(lease.Key, storeExecutableDigest, "server-fixture")
	fingerprint := server.ServerIdentity
	plan := compileStorePlan(t, fingerprint, []action.Budget{storeBudget(
		"process-single", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	store, err := OpenStore(StoreOptions{
		Home: home, Repository: resolved, KeyLease: lease,
		Clock: SystemClock{}, OwnerID: "owner-" + strings.TrimPrefix(os.Getenv(processHelperCall), "act_"),
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := action.ParseJSON([]byte(`{"amount":1}`))
	if err != nil {
		t.Fatal(err)
	}
	request := action.Request{
		FormatVersion: action.RequestFormatVersion, CallID: os.Getenv(processHelperCall),
		Transport: action.TransportMCPStdio, ServerLabel: "server", ServerFingerprint: fingerprint,
		Tool: "execute", ToolContractDigest: storeToolDigest, Phase: action.PhasePreCall,
		RepositoryIdentity: repositoryIdentity, PolicyDigest: storePolicyDigest,
		LockDigest: storeLockDigest, AuthorityMode: action.AuthorityOperatorPinned,
		Arguments: &arguments, Context: []action.ContextValue{},
		Completeness: action.CompleteEvidence(), Deadline: action.DeadlineReady,
		StateVersion: os.Getenv(processHelperVersion),
	}
	result, err := store.Reserve(context.Background(), ReserveRequest{
		Plan: plan, Request: request, Context: bound,
		Authority: PolicyAuthority{
			Mode: action.AuthorityOperatorPinned, ExpectedLockDigest: storeLockDigest,
		},
		Server: server,
	})
	if err != nil {
		var stateErr *StateError
		if errors.As(err, &stateErr) {
			fmt.Printf("blocked:%s", stateErr.Code)
			return
		}
		t.Fatal(err)
	}
	if result.Reservation == nil {
		fmt.Print("exhausted")
		return
	}
	fmt.Print("reserved")
}

func TestBudgetStoreSerializesSeparateProcesses(t *testing.T) {
	fixture := newStoreFixture(t, []action.Budget{storeBudget(
		"process-single", action.BudgetLimits{CallCount: 1}, action.BudgetResetNever,
	)})
	version, err := fixture.store.CurrentStateVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "start")
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	for index, id := range []string{callID("y"), callID("z")} {
		command := exec.Command(executable, "-test.run=^TestBudgetStoreProcessHelper$")
		command.Env = append(os.Environ(),
			processHelperEnabled+"=1",
			processHelperHome+"="+fixture.home,
			processHelperRepo+"="+fixture.repository,
			processHelperVersion+"="+version,
			processHelperCall+"="+id,
			processHelperGate+"="+gate,
		)
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands[index] = command
	}
	if err := os.WriteFile(gate, []byte("start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("reservation helper failed: %v; outputs=%q/%q", err, outputs[0].String(), outputs[1].String())
		}
	}
	reserved, blocked := 0, 0
	for index := range outputs {
		output := outputs[index].String()
		if strings.Contains(output, "reserved") {
			reserved++
		}
		if strings.Contains(output, "blocked:"+string(action.ReasonStateUnavailable)) {
			blocked++
		}
	}
	if reserved != 1 || blocked != 1 {
		t.Fatalf("separate-process results = %q / %q", outputs[0].String(), outputs[1].String())
	}
}
