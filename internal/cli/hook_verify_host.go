package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/hooks"
)

// The checksum identifies the resolved entrypoint. Script launchers may load
// additional runtime files; host-specific runners must bind those separately.
type liveHookHostIdentity struct {
	Executable        string `json:"executable"`
	Version           string `json:"version"`
	EntrypointSHA256  string `json:"entrypoint_sha256"`
	IdentityVerified  bool   `json:"identity_verified"`
	RuntimeExecutable string `json:"runtime_executable,omitempty"`
	RuntimeSHA256     string `json:"runtime_sha256,omitempty"`
}

func liveHookHostCandidates(kind, surface string) []string {
	switch kind {
	case hooks.KindCodex:
		return []string{"codex"}
	case hooks.KindDevinCLI:
		return []string{"devin"}
	case hooks.KindOMP:
		return []string{"omp"}
	case hooks.KindDSH:
		return []string{"dsh"}
	case hooks.KindCursor:
		switch hooks.HostSurface(surface) {
		case hooks.HostSurfaceCursorCLIInteractive, hooks.HostSurfaceCursorCLIPrint:
			return []string{"cursor-agent", "agent"}
		case hooks.HostSurfaceCursorDesktopAgent, hooks.HostSurfaceCursorDesktopCmdK, hooks.HostSurfaceCursorTab:
			return []string{"/Applications/Cursor.app/Contents/Resources/app/bin/cursor", "cursor"}
		}
	case hooks.KindOpenCode:
		return []string{"opencode"}
	case hooks.KindKilo:
		if surface == "cli" {
			return []string{"kilo", "kilocode"}
		}
	case hooks.KindPi:
		return []string{"pi"}
	case hooks.KindZCode:
		return []string{"zcode"}
	}
	return nil
}

var liveHookVersionPatterns = map[string]*regexp.Regexp{
	hooks.KindCodex:    regexp.MustCompile(`^codex-cli [0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`),
	hooks.KindDevinCLI: regexp.MustCompile(`^devin [0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?(?: \([a-f0-9]+\))?$`),
	hooks.KindOMP:      regexp.MustCompile(`^omp/[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`),
	hooks.KindCursor:   regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[a-f0-9]+$`),
	hooks.KindDSH:      regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`),
}

func discoverLiveHookHost(ctx context.Context, kind, surface string) (*liveHookHostIdentity, error) {
	candidates := liveHookHostCandidates(kind, surface)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("selected surface has no local executable discovery contract")
	}
	for _, candidate := range candidates {
		path, err := hookVerifyLookPath(candidate)
		if err != nil {
			continue
		}
		identity, err := inspectLiveHookHost(ctx, kind, surface, path)
		if err == nil {
			return identity, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("no available executable satisfied the selected host identity contract")
}

func inspectLiveHookHost(ctx context.Context, kind, surface, path string) (*liveHookHostIdentity, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(resolved) {
		return nil, fmt.Errorf("host entrypoint does not resolve to an absolute file")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("host entrypoint is not a regular file")
	}
	digest, err := hashHookVerificationExecutable(resolved, info)
	if err != nil {
		return nil, fmt.Errorf("host entrypoint could not be fingerprinted")
	}
	version, err := readLiveHookHostMetadata(ctx, resolved, "--version")
	if err != nil {
		return nil, err
	}
	version = strings.TrimSpace(version)
	if len(version) == 0 || len(version) > 256 || strings.ContainsAny(version, "\n\r\x00\x1b") {
		return nil, fmt.Errorf("host version output is outside its contract")
	}
	pattern, known := liveHookVersionPatterns[kind]
	if kind == hooks.KindCursor && surface != string(hooks.HostSurfaceCursorCLIInteractive) && surface != string(hooks.HostSurfaceCursorCLIPrint) {
		known = false
	}
	if known && !pattern.MatchString(version) {
		return nil, fmt.Errorf("executable reported a different host identity")
	}
	if kind == hooks.KindCursor && known {
		help, err := readLiveHookHostMetadata(ctx, resolved, "--help")
		if err != nil || !strings.Contains(help, "Start the Cursor Agent") {
			return nil, fmt.Errorf("executable did not identify itself as Cursor Agent")
		}
	}
	if kind == hooks.KindDSH {
		// The pinned DSH launcher prints an unbranded package version. Its own
		// --help exits before profile boot and supplies the required identity.
		help, err := readLiveHookHostMetadata(ctx, resolved, "--help")
		if err != nil || !strings.Contains(help, "dsh: boot a DeepSeek Harness profile") {
			return nil, fmt.Errorf("executable did not identify itself as DeepSeek Harness")
		}
	}
	finalDigest, err := hashHookVerificationExecutable(resolved, info)
	if err != nil || !bytes.Equal(finalDigest, digest) {
		return nil, fmt.Errorf("host entrypoint changed during identification")
	}
	return &liveHookHostIdentity{Executable: resolved, Version: version, EntrypointSHA256: fmt.Sprintf("%x", digest), IdentityVerified: known}, nil
}

func readLiveHookHostMetadata(ctx context.Context, executable, flag string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, flag)
	configureHookVerificationProcess(command)
	body, err := boundedexec.Output(command, 32<<10)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		// Host diagnostics can include credentials or unrelated private state.
		return "", fmt.Errorf("host metadata command failed or exceeded its output limit")
	}
	return string(body), nil
}
