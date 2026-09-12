package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/hooks"
)

// This is an observation receipt, not a reusable authorization token. Operations
// start unproven; host-specific runners must supply correlated native evidence.
type liveHookReceipt struct {
	RunID               string                    `json:"run_id"`
	Mode                string                    `json:"mode"`
	StartedAt           time.Time                 `json:"started_at"`
	ReconcSHA256        string                    `json:"reconc_sha256"`
	RepositorySHA256    string                    `json:"repository_sha256"`
	ConfigurationSHA256 string                    `json:"configuration_sha256"`
	Files               []liveHookFileIdentity    `json:"files"`
	Operations          []liveHookOperationProof  `json:"operations"`
	Native              *liveHookNativeRunReceipt `json:"native,omitempty"`
}

type liveHookNativeRunReceipt struct {
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	ProcessID        int       `json:"process_id"`
	ExitCode         int       `json:"exit_code"`
	InvocationSHA256 string    `json:"invocation_sha256"`
	StdoutSHA256     string    `json:"stdout_sha256"`
	StderrSHA256     string    `json:"stderr_sha256"`
}

type liveHookFileIdentity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type liveHookOperationProof struct {
	Operation   string `json:"operation"`
	Nonce       string `json:"nonce"`
	AttemptID   string `json:"attempt_id,omitempty"`
	Decision    string `json:"decision"`
	HostOutcome string `json:"host_outcome"`
	EffectCheck string `json:"effect_check"`
	Complete    bool   `json:"complete"`
	Detail      string `json:"detail,omitempty"`
}

func newLiveHookReceipt(repo string, installation *hooks.InstallReport) (*liveHookReceipt, error) {
	runID, err := liveHookProbeNonce()
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return nil, err
	}
	digest, err := hashHookVerificationExecutable(executable, info)
	if err != nil {
		return nil, err
	}
	receipt := &liveHookReceipt{
		RunID: runID, Mode: "operator-assisted", StartedAt: time.Now().UTC(),
		ReconcSHA256: fmt.Sprintf("%x", digest), RepositorySHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(resolved))),
	}
	paths := []string{".reconc.yml", ".reconc/policy.lock.json", installation.TargetPath, installation.WrapperPath, installation.WrapperTargetPath, installation.ActivationPath}
	receipt.Files, receipt.ConfigurationSHA256, err = fingerprintLiveHookFiles(repo, paths)
	if err != nil {
		return nil, err
	}
	for _, operation := range []string{"read", "allowed-write", "denied-write", "allowed-shell", "denied-shell", "nonzero-shell", "classified-mcp"} {
		nonce, err := liveHookProbeNonce()
		if err != nil {
			return nil, err
		}
		receipt.Operations = append(receipt.Operations, liveHookOperationProof{
			Operation: operation, Nonce: nonce, Decision: "unproven", HostOutcome: "unproven", EffectCheck: "unproven",
		})
	}
	return receipt, nil
}

func liveHookProbeNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", nonce), nil
}

func fingerprintLiveHookFiles(repo string, paths []string) ([]liveHookFileIdentity, string, error) {
	unique := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if !filepath.IsLocal(path) {
			return nil, "", fmt.Errorf("probe configuration path must remain repository-relative")
		}
		unique[filepath.ToSlash(filepath.Clean(path))] = true
	}
	names := sortedBoolKeys(unique)
	files := make([]liveHookFileIdentity, 0, len(names))
	combined := sha256.New()
	for _, path := range names {
		body, err := boundedio.ReadRegularFile(filepath.Join(repo, filepath.FromSlash(path)), 4<<20)
		if err != nil {
			return nil, "", fmt.Errorf("fingerprint probe configuration %s: %w", path, err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		files = append(files, liveHookFileIdentity{Path: path, SHA256: digest})
		if _, err := fmt.Fprintf(combined, "%s\x00%s\n", path, digest); err != nil {
			return nil, "", err
		}
	}
	return files, fmt.Sprintf("%x", combined.Sum(nil)), nil
}

func validateLiveHookReceiptFiles(repo string, receipt *liveHookReceipt) error {
	if receipt == nil {
		return fmt.Errorf("live probe receipt is unavailable")
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256([]byte(resolved))) != receipt.RepositorySHA256 {
		return fmt.Errorf("live probe repository identity changed")
	}
	paths := make([]string, 0, len(receipt.Files))
	for _, file := range receipt.Files {
		paths = append(paths, file.Path)
	}
	files, digest, err := fingerprintLiveHookFiles(repo, paths)
	if err != nil {
		return err
	}
	if digest != receipt.ConfigurationSHA256 || len(files) != len(receipt.Files) {
		return fmt.Errorf("live probe configuration changed during the exercise")
	}
	for index, file := range files {
		if file != receipt.Files[index] {
			return fmt.Errorf("live probe file identity changed during the exercise")
		}
	}
	return nil
}

func bindLiveHookCaptureFiles(repo string, receipt *liveHookReceipt) error {
	paths := make([]string, 0, len(receipt.Files)+1)
	for _, file := range receipt.Files {
		paths = append(paths, file.Path)
	}
	paths = append(paths, hooks.WrapperPath+"-verify-real")
	files, digest, err := fingerprintLiveHookFiles(repo, paths)
	if err != nil {
		return err
	}
	for _, previous := range receipt.Files {
		expectedPath := previous.Path
		if expectedPath == hooks.WrapperPath {
			expectedPath += "-verify-real"
		}
		matched := false
		for _, current := range files {
			if current.Path == expectedPath && current.SHA256 == previous.SHA256 {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("probe configuration changed while installing capture")
		}
	}
	receipt.Files, receipt.ConfigurationSHA256 = files, digest
	return nil
}
