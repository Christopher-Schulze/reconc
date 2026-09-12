package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

func TestLiveHookProbePolicyCoversBothDeniedOperations(t *testing.T) {
	repo := t.TempDir()
	if err := initializeHookVerificationRepo(repo, false); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, payload string
		denied        bool
	}{
		{"denied-write", `{"tool_name":"Write","tool_input":{"file_path":"forbidden.txt"}}`, true},
		{"denied-shell", `{"tool_name":"Bash","tool_input":{"command":"touch forbidden-command-marker"}}`, true},
		{"allowed-write", `{"tool_name":"Write","tool_input":{"file_path":"allowed.txt"}}`, false},
		{"allowed-shell", `{"tool_name":"Bash","tool_input":{"command":"touch allowed-command-marker"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			payload := fmt.Sprintf(`{"session_id":"probe-%s",%s`, test.name, test.payload[1:])
			err := runHookRuntimeWithInput([]string{"opencode-pre-tool-use", repo}, strings.NewReader(payload), &stdout, &stderr)
			if got := ExitCode(err); (got == 2) != test.denied || got != 0 && got != 2 {
				t.Fatalf("denied=%t exit=%d stdout=%s stderr=%s err=%v", test.denied, got, &stdout, &stderr, err)
			}
		})
	}
}

func liveHookReceiptFixture(t *testing.T) (string, *hooks.InstallReport) {
	t.Helper()
	repo := t.TempDir()
	for _, path := range []string{".reconc.yml", ".reconc/policy.lock.json", "hooks.json", hooks.WrapperPath} {
		target := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("fixture for "+path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo, &hooks.InstallReport{TargetPath: "hooks.json", WrapperPath: hooks.WrapperPath}
}

func TestLiveHookReceiptBindsRealFilesAndStartsUnproven(t *testing.T) {
	repo, installation := liveHookReceiptFixture(t)
	before := time.Now()
	seen := map[string]bool{}
	var previous *liveHookReceipt
	for range 2 {
		receipt, err := newLiveHookReceipt(repo, installation)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.StartedAt.Before(before) || receipt.StartedAt.After(time.Now()) || receipt.Mode != "operator-assisted" || len(receipt.ReconcSHA256) != 64 || len(receipt.Files) != 4 {
			t.Fatalf("receipt=%+v", receipt)
		}
		if previous != nil && (receipt.RepositorySHA256 != previous.RepositorySHA256 || receipt.ConfigurationSHA256 != previous.ConfigurationSHA256 || receipt.ReconcSHA256 != previous.ReconcSHA256) {
			t.Fatal("unchanged identities drifted")
		}
		if seen[receipt.RunID] || len(receipt.RunID) != 32 {
			t.Fatal("run nonce missing or reused")
		}
		seen[receipt.RunID] = true
		var operations []string
		for _, operation := range receipt.Operations {
			operations = append(operations, operation.Operation)
			if seen[operation.Nonce] || len(operation.Nonce) != 32 || operation.Complete || operation.AttemptID != "" || operation.Decision != "unproven" || operation.HostOutcome != "unproven" || operation.EffectCheck != "unproven" {
				t.Fatalf("invented operation proof or reused nonce: %+v", operation)
			}
			seen[operation.Nonce] = true
		}
		if strings.Join(operations, ",") != "read,allowed-write,denied-write,allowed-shell,denied-shell,nonzero-shell,classified-mcp" {
			t.Fatalf("operations=%v", operations)
		}
		if err := validateLiveHookReceiptFiles(repo, receipt); err != nil {
			t.Fatal(err)
		}
		previous = receipt
	}
}

func TestLiveHookReceiptRejectsChangedAndForeignIdentity(t *testing.T) {
	for _, change := range []string{"configuration", "repository", "file-digest", "duplicate-file", "missing-receipt", "escaping-path"} {
		t.Run(change, func(t *testing.T) {
			repo, installation := liveHookReceiptFixture(t)
			receipt, err := newLiveHookReceipt(repo, installation)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "configuration":
				if err := os.WriteFile(filepath.Join(repo, "hooks.json"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "repository":
				repo = t.TempDir()
			case "file-digest":
				receipt.Files[0].SHA256 = strings.Repeat("0", 64)
			case "duplicate-file":
				receipt.Files = append(receipt.Files, receipt.Files[0])
			case "missing-receipt":
				receipt = nil
			case "escaping-path":
				receipt.Files[0].Path = "../outside"
			}
			if err := validateLiveHookReceiptFiles(repo, receipt); err == nil {
				t.Fatal("changed receipt accepted")
			}
		})
	}
}

func TestLiveHookCaptureBindingPreservesGeneratedWrapper(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		repo, installation := liveHookReceiptFixture(t)
		receipt, err := newLiveHookReceipt(repo, installation)
		if err != nil {
			t.Fatal(err)
		}
		if err := installLiveHookProbeShim(repo, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		if corrupt {
			if err := os.WriteFile(filepath.Join(repo, hooks.WrapperPath+"-verify-real"), []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := bindLiveHookCaptureFiles(repo, receipt); (err != nil) != corrupt {
			t.Fatalf("corrupt=%t binding error=%v", corrupt, err)
		}
		if !corrupt {
			if err := validateLiveHookReceiptFiles(repo, receipt); err != nil {
				t.Fatal(err)
			}
		}
	}
}
