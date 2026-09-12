package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/hooks"
)

// Launch and fingerprint the native executable itself. A known npm layout is
// a discovery candidate, never proof that an arbitrary script delegates to it.
func bindCodexNativeHost(ctx context.Context, host *liveHookHostIdentity) error {
	if host == nil || !host.IdentityVerified || host.Version != "codex-cli 0.154.0" {
		return fmt.Errorf("installed Codex version has no qualified native probe contract")
	}
	for _, candidate := range codexNativeCandidates(host.Executable) {
		if !liveHookNativeExecutable(candidate) {
			continue
		}
		native, err := inspectLiveHookHost(ctx, hooks.KindCodex, "cli", candidate)
		if err != nil || native.Version != host.Version {
			continue
		}
		host.RuntimeExecutable, host.RuntimeSHA256 = native.Executable, native.EntrypointSHA256
		return validateLiveHookHostFiles(host)
	}
	return fmt.Errorf("native Codex executable could not be resolved and fingerprinted")
}

func codexNativeCandidates(entrypoint string) []string {
	candidates := []string{entrypoint}
	if filepath.Base(entrypoint) != "codex.js" || filepath.Base(filepath.Dir(entrypoint)) != "bin" {
		return candidates
	}
	var target, platform string
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		target, platform = "aarch64-apple-darwin", "darwin-arm64"
	case "darwin/amd64":
		target, platform = "x86_64-apple-darwin", "darwin-x64"
	case "linux/arm64":
		target, platform = "aarch64-unknown-linux-musl", "linux-arm64"
	case "linux/amd64":
		target, platform = "x86_64-unknown-linux-musl", "linux-x64"
	default:
		return candidates
	}
	root := filepath.Dir(filepath.Dir(entrypoint))
	for directory := root; ; directory = filepath.Dir(directory) {
		candidates = append(candidates, filepath.Join(directory, "node_modules", "@openai", "codex-"+platform, "vendor", target, "bin", "codex"))
		if filepath.Dir(directory) == directory {
			break
		}
	}
	return append(candidates, filepath.Join(root, "vendor", target, "bin", "codex"))
}

func liveHookNativeExecutable(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	var header [4]byte
	err = boundedio.WithRegularFileSnapshot(resolved, maxHookVerificationExecutable, func(file *os.File, _ os.FileInfo) error {
		_, err := io.ReadFull(file, header[:])
		return err
	})
	if err != nil {
		return false
	}
	return bytes.Equal(header[:], []byte{0x7f, 'E', 'L', 'F'}) ||
		bytes.Equal(header[:], []byte{0xcf, 0xfa, 0xed, 0xfe}) ||
		bytes.Equal(header[:], []byte{0xca, 0xfe, 0xba, 0xbe}) || header[0] == 'M' && header[1] == 'Z'
}

func validateLiveHookHostFiles(host *liveHookHostIdentity) error {
	if host == nil || !host.IdentityVerified {
		return fmt.Errorf("native host identity is unavailable")
	}
	for _, file := range []struct{ path, digest string }{{host.Executable, host.EntrypointSHA256}, {host.RuntimeExecutable, host.RuntimeSHA256}} {
		if !filepath.IsAbs(file.path) || !liveHookHex(file.digest, 32) {
			return fmt.Errorf("native host file identity is incomplete")
		}
		info, err := os.Lstat(file.path)
		if err != nil {
			return fmt.Errorf("native host file is unavailable")
		}
		digest, err := hashHookVerificationExecutable(file.path, info)
		if err != nil || fmt.Sprintf("%x", digest) != file.digest {
			return fmt.Errorf("native host file changed during the probe")
		}
	}
	return nil
}
