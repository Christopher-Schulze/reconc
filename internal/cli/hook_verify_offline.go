package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

const (
	maxHookVerificationOutput     = 1 << 20
	maxHookVerificationExecutable = 256 << 20
	hookVerificationCopyBuffer    = 128 << 10
	hookVerificationChildEnv      = "RECONC_HOOK_VERIFY_ISOLATED_CHILD"
	hookVerificationRepoEnv       = "RECONC_HOOK_VERIFY_REPO"
)

var newHookVerificationChildCommand = func(ctx context.Context, executable string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, executable, args...)
}

type hookVerificationWorkspace struct {
	executable  string
	repo        string
	environment []string
	cleanup     func()
}

func runOfflineHookVerification(options hookVerifyOptions, surfaces []hooks.VerificationSurface) (hookVerificationReport, error) {
	workspace, err := newHookVerificationWorkspace("reconc-hook-verify-")
	if err != nil {
		return hookVerificationReport{}, err
	}
	defer workspace.cleanup()
	body, err := runHookVerificationChild(workspace, "hook", "__verify-offline", options.host, options.surface, workspace.repo)
	if err != nil {
		return hookVerificationReport{}, err
	}
	var report hookVerificationReport
	if err := json.Unmarshal(body, &report); err != nil {
		return hookVerificationReport{}, fmt.Errorf("decode isolated verification report: %w", err)
	}
	if report.FormatVersion != hookVerificationFormatVersion || report.Mode != "offline" {
		return hookVerificationReport{}, fmt.Errorf("isolated verification report contract drifted")
	}
	return report, nil
}

func runHookVerificationOfflineChild(args []string, stdout io.Writer) error {
	if os.Getenv(hookVerificationChildEnv) != "1" || len(args) != 3 || args[2] != os.Getenv(hookVerificationRepoEnv) {
		return &CLIError{ExitCode: 1, Message: "reconc hook: unknown subcommand \"__verify-offline\""}
	}
	surfaces, err := selectHookVerificationSurfaces(args[0], args[1])
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: invalid isolated offline surface"}
	}
	report, err := runOfflineHookVerificationLocal(surfaces)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: encode isolated report: " + err.Error()}
	}
	return nil
}

func runOfflineHookVerificationLocal(surfaces []hooks.VerificationSurface) (hookVerificationReport, error) {
	repo, cleanup, err := prepareOfflineHookVerification()
	if err != nil {
		return hookVerificationReport{}, err
	}
	defer cleanup()
	byKind := make(map[string]offlineHookKindResult)
	for _, kind := range uniqueVerificationKinds(surfaces) {
		byKind[kind] = verifyOfflineHookKind(kind, repo)
	}
	return assembleOfflineHookReport(surfaces, byKind), nil
}

func prepareOfflineHookVerification() (string, func(), error) {
	return prepareHookVerificationRepo("reconc-hook-verify-", true)
}

func prepareHookVerificationRepo(prefix string, stageDeniedPath bool) (string, func(), error) {
	if os.Getenv(hookVerificationChildEnv) != "1" {
		return "", nil, fmt.Errorf("%s setup must run in an isolated child", prefix)
	}
	repo := os.Getenv(hookVerificationRepoEnv)
	if repo == "" {
		return "", nil, fmt.Errorf("isolated verification repository is missing")
	}
	if err := initializeHookVerificationRepo(repo, stageDeniedPath); err != nil {
		return "", nil, err
	}
	return repo, func() {}, nil
}

func assembleOfflineHookReport(surfaces []hooks.VerificationSurface, byKind map[string]offlineHookKindResult) hookVerificationReport {
	report := hookVerificationReport{
		FormatVersion: hookVerificationFormatVersion,
		Mode:          "offline",
		Complete:      true,
		Results:       make([]hookVerificationResult, 0, len(surfaces)),
	}
	for _, surface := range surfaces {
		result := offlineSurfaceResult(surface, byKind[surface.Kind])
		if !hookVerificationResultComplete(result) {
			report.Complete = false
		}
		report.Results = append(report.Results, result)
	}
	return report
}

func offlineSurfaceResult(surface hooks.VerificationSurface, source offlineHookKindResult) hookVerificationResult {
	resultClass := "synthetic-block"
	if surface.Kind == hooks.KindGitPreCommit {
		resultClass = "synthetic-commit-block"
	}
	return hookVerificationResult{
		Kind: surface.Kind, Surface: surface.Surface,
		ArtifactGeneration: source.artifactGeneration, Configuration: source.configuration,
		Transport: source.transport, PolicyDecision: source.policyDecision,
		ResponseAdaptation: source.responseAdaptation, DurationMillis: source.durationMillis,
		Configured: source.configured, Discoverable: source.discoverable,
		SyntheticEnforced: source.syntheticEnforced, Inferred: surface.Inferred,
		Degraded: source.degraded, Unsupported: append([]string{}, source.unsupported...),
		ExpectedEvents: append([]string(nil), surface.ExpectedEvents...),
		UnprovenEvents: append([]string(nil), surface.ExpectedEvents...),
		ObservedFields: []string{}, ResultClass: resultClass, Detail: source.detail,
		ActionRequired: surface.Action,
	}
}

func hookVerificationResultComplete(result hookVerificationResult) bool {
	return !result.Degraded && result.ArtifactGeneration == "verified" && result.Configuration == "verified" &&
		result.Transport == "verified" && result.PolicyDecision == "verified" && result.ResponseAdaptation == "verified"
}

func uniqueVerificationKinds(surfaces []hooks.VerificationSurface) []string {
	seen := map[string]bool{}
	kinds := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		if seen[surface.Kind] {
			continue
		}
		seen[surface.Kind] = true
		kinds = append(kinds, surface.Kind)
	}
	sort.Strings(kinds)
	return kinds
}

func initializeHookVerificationRepo(repo string, stageDeniedPath bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := gitexec.CommandContext(ctx, repo, nil, "init", "-q")
	if output, err := boundedexec.CombinedOutput(command, maxHookVerificationOutput); err != nil {
		return fmt.Errorf("initialize disposable Git repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	files := map[string]string{
		"AGENTS.md":   "# Disposable Reconc hook verification repository\n",
		".reconc.yml": "rules:\n  - id: hook-verify-deny-write\n    kind: deny_write\n    paths: ['forbidden.txt']\n    mode: block\n    message: synthetic hook verification denial\n",
	}
	if stageDeniedPath {
		files["forbidden.txt"] = "synthetic verification input\n"
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(repo, relative), []byte(content), 0o644); err != nil {
			return fmt.Errorf("write disposable %s: %w", relative, err)
		}
	}
	if _, err := compiler.CompileRepoPolicy(repo, "hook-verify"); err != nil {
		return fmt.Errorf("compile disposable policy: %w", err)
	}
	if stageDeniedPath {
		command = gitexec.CommandContext(ctx, repo, nil, "add", "--", "forbidden.txt")
		if output, err := boundedexec.CombinedOutput(command, maxHookVerificationOutput); err != nil {
			return fmt.Errorf("stage disposable denied path: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func newHookVerificationWorkspace(prefix string) (hookVerificationWorkspace, error) {
	probeRoot, err := os.MkdirTemp("", prefix)
	if err != nil {
		return hookVerificationWorkspace{}, fmt.Errorf("create disposable root: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(probeRoot) }
	repo := filepath.Join(probeRoot, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		cleanup()
		return hookVerificationWorkspace{}, fmt.Errorf("create disposable repository: %w", err)
	}
	binDir := filepath.Join(probeRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		cleanup()
		return hookVerificationWorkspace{}, fmt.Errorf("create disposable binary directory: %w", err)
	}
	if err := installHookVerificationBareExecutable(binDir); err != nil {
		cleanup()
		return hookVerificationWorkspace{}, err
	}
	for _, directory := range hookVerificationPrivateDirectories(probeRoot) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			cleanup()
			return hookVerificationWorkspace{}, fmt.Errorf("create disposable environment directory: %w", err)
		}
	}
	values := hookVerificationEnvironmentValues(probeRoot, binDir, os.Environ())
	values[hookVerificationChildEnv] = "1"
	values[hookVerificationRepoEnv] = repo
	executable, err := os.Executable()
	if err != nil {
		cleanup()
		return hookVerificationWorkspace{}, fmt.Errorf("resolve verification executable: %w", err)
	}
	return hookVerificationWorkspace{
		executable:  executable,
		repo:        repo,
		environment: hookVerificationEnvironment(values),
		cleanup:     cleanup,
	}, nil
}

func installHookVerificationBareExecutable(binDir string) error {
	running, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve running executable: %w", err)
	}
	bareName := "reconc"
	if runtime.GOOS == "windows" {
		bareName += ".exe"
	}
	return linkOrCopyVerificationExecutable(running, filepath.Join(binDir, bareName))
}

func hookVerificationEnvironmentValues(probeRoot, binDir string, inherited []string) map[string]string {
	home := filepath.Join(probeRoot, "home")
	temporary := filepath.Join(probeRoot, "tmp")
	values := map[string]string{
		"HOME":                    home,
		"USERPROFILE":             home,
		"XDG_CACHE_HOME":          filepath.Join(probeRoot, "cache"),
		"XDG_CONFIG_HOME":         filepath.Join(probeRoot, "config"),
		"XDG_DATA_HOME":           filepath.Join(probeRoot, "data"),
		"TMPDIR":                  temporary,
		"TMP":                     temporary,
		"TEMP":                    temporary,
		"LANG":                    "C",
		"LC_ALL":                  "C",
		"TZ":                      "UTC",
		"RECONC_HOME":             filepath.Join(probeRoot, "reconc-home"),
		agentsession.StateRootEnv: filepath.Join(probeRoot, "session-state"),
		"KIMI_CODE_HOME":          filepath.Join(probeRoot, "kimi-code"),
		"PI_CODING_AGENT_DIR":     filepath.Join(probeRoot, "pi-agent"),
		"KILO_PURE":               "",
		"PATH":                    hookVerificationPath(binDir, environmentValue(inherited, "PATH")),
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"COMSPEC", "PATHEXT", "SYSTEMROOT", "WINDIR"} {
			if value, ok := lookupEnvironment(inherited, name); ok {
				values[name] = value
			}
		}
	}
	return values
}

func hookVerificationPrivateDirectories(probeRoot string) []string {
	return []string{
		filepath.Join(probeRoot, "cache"),
		filepath.Join(probeRoot, "config"),
		filepath.Join(probeRoot, "data"),
		filepath.Join(probeRoot, "home"),
		filepath.Join(probeRoot, "tmp"),
	}
}

func hookVerificationEnvironment(values map[string]string) []string {
	environment := make([]string, 0, len(values))
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment = append(environment, name+"="+values[name])
	}
	return environment
}

func hookVerificationPath(binDir, inherited string) string {
	paths := make([]string, 0, len(filepath.SplitList(inherited))+1)
	seen := make(map[string]struct{}, len(paths))
	for _, path := range append([]string{binDir}, filepath.SplitList(inherited)...) {
		if path == "" || !filepath.IsAbs(path) {
			continue
		}
		path = filepath.Clean(path)
		identity := path
		if runtime.GOOS == "windows" {
			identity = strings.ToLower(path)
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		paths = append(paths, path)
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func environmentValue(environment []string, name string) string {
	value, _ := lookupEnvironment(environment, name)
	return value
}

func lookupEnvironment(environment []string, name string) (string, bool) {
	for index := len(environment) - 1; index >= 0; index-- {
		key, value, found := strings.Cut(environment[index], "=")
		if found && strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

func runHookVerificationChild(workspace hookVerificationWorkspace, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := newHookVerificationChildCommand(ctx, workspace.executable, args...)
	command.Env = append([]string(nil), workspace.environment...)
	body, err := boundedexec.Output(command, 4*maxHookVerificationOutput)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && len(exitError.Stderr) > 0 {
			return nil, fmt.Errorf("isolated hook verification failed: %w: %s", err, strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, fmt.Errorf("isolated hook verification failed: %w", err)
	}
	return body, nil
}

func linkOrCopyVerificationExecutable(source, target string) error {
	return linkOrCopyVerificationExecutableWithOps(source, target, hookVerificationCopyOps{
		link: os.Link,
		openTarget: func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
			return os.OpenFile(path, flag, mode)
		},
	})
}

type hookVerificationCopyTarget interface {
	io.Writer
	Stat() (os.FileInfo, error)
	Chmod(os.FileMode) error
	Close() error
}

type hookVerificationCopyOps struct {
	link       func(string, string) error
	openTarget func(string, int, os.FileMode) (hookVerificationCopyTarget, error)
}

func linkOrCopyVerificationExecutableWithOps(source, target string, operations hookVerificationCopyOps) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve running executable identity: %w", err)
	}
	before, err := os.Lstat(resolved)
	if err != nil {
		return fmt.Errorf("inspect running executable: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() <= 0 || before.Size() > maxHookVerificationExecutable {
		return fmt.Errorf("running executable must be a non-symlink regular file within %d bytes", maxHookVerificationExecutable)
	}
	if err := operations.link(resolved, target); err == nil {
		afterSource, sourceErr := os.Lstat(resolved)
		targetInfo, targetErr := os.Lstat(target)
		stable := sourceErr == nil && targetErr == nil && targetInfo.Mode().IsRegular() &&
			targetInfo.Mode()&os.ModeSymlink == 0 && os.SameFile(before, afterSource) &&
			os.SameFile(afterSource, targetInfo) && before.Mode() == afterSource.Mode() &&
			before.Size() == afterSource.Size() && before.ModTime().Equal(afterSource.ModTime())
		if stable {
			return nil
		}
		if targetErr == nil && os.SameFile(before, targetInfo) {
			_ = os.Remove(target)
		}
		return errors.Join(sourceErr, targetErr, errors.New("running executable changed while creating disposable hard link"))
	}
	return streamHookVerificationExecutable(resolved, target, before, operations.openTarget)
}

func streamHookVerificationExecutable(
	sourcePath string,
	targetPath string,
	expectedSource os.FileInfo,
	openTarget func(string, int, os.FileMode) (hookVerificationCopyTarget, error),
) error {
	var createdIdentity os.FileInfo
	err := boundedio.WithRegularFileSnapshot(sourcePath, maxHookVerificationExecutable, func(source *os.File, sourceInfo os.FileInfo) error {
		if !sameHookVerificationSnapshot(expectedSource, sourceInfo) {
			return errors.New("running executable changed before streaming")
		}
		target, err := openTarget(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return fmt.Errorf("create disposable bare executable: %w", err)
		}
		createdInfo, err := target.Stat()
		if err != nil || !createdInfo.Mode().IsRegular() {
			return errors.Join(err, errors.New("disposable bare executable is not a regular file"), target.Close())
		}
		createdIdentity = createdInfo

		sourceHash := sha256.New()
		limited := io.LimitReader(source, maxHookVerificationExecutable+1)
		writer := struct{ io.Writer }{Writer: target}
		written, copyErr := io.CopyBuffer(writer, io.TeeReader(limited, sourceHash), make([]byte, hookVerificationCopyBuffer))
		if copyErr != nil {
			return closeAndRemoveHookVerificationTarget(targetPath, createdInfo, target, fmt.Errorf("copy running executable: %w", copyErr))
		}
		if written != sourceInfo.Size() {
			return closeAndRemoveHookVerificationTarget(targetPath, createdInfo, target, fmt.Errorf("copy running executable: wrote %d of %d bytes", written, sourceInfo.Size()))
		}
		if err := target.Chmod(0o700); err != nil {
			return closeAndRemoveHookVerificationTarget(targetPath, createdInfo, target, fmt.Errorf("make disposable bare executable runnable: %w", err))
		}
		streamedInfo, statErr := target.Stat()
		pathInfo, pathErr := os.Lstat(targetPath)
		if statErr != nil || pathErr != nil || !sameHookVerificationIdentity(createdInfo, streamedInfo) ||
			!sameHookVerificationIdentity(streamedInfo, pathInfo) || streamedInfo.Size() != written ||
			(runtime.GOOS != "windows" && streamedInfo.Mode().Perm() != 0o700) {
			return closeAndRemoveHookVerificationTarget(targetPath, createdInfo, target, errors.Join(statErr, pathErr, errors.New("disposable bare executable changed identity, size, or mode while streaming")))
		}
		if err := target.Close(); err != nil {
			return removeHookVerificationTarget(targetPath, createdInfo, fmt.Errorf("close disposable bare executable: %w", err))
		}
		targetDigest, err := hashHookVerificationExecutable(targetPath, streamedInfo)
		if err != nil {
			return removeHookVerificationTarget(targetPath, createdInfo, err)
		}
		if !bytes.Equal(sourceHash.Sum(nil), targetDigest) {
			return removeHookVerificationTarget(targetPath, createdInfo, errors.New("disposable bare executable checksum differs from running executable"))
		}
		return nil
	})
	if err != nil && createdIdentity != nil {
		return removeHookVerificationTarget(targetPath, createdIdentity, err)
	}
	return err
}

func hashHookVerificationExecutable(path string, expected os.FileInfo) ([]byte, error) {
	hash := sha256.New()
	err := boundedio.WithRegularFileSnapshot(path, maxHookVerificationExecutable, func(file *os.File, info os.FileInfo) error {
		if !sameHookVerificationSnapshot(expected, info) {
			return errors.New("disposable bare executable changed identity, size, mode, or modification time before checksum verification")
		}
		written, err := io.CopyBuffer(hash, file, make([]byte, hookVerificationCopyBuffer))
		if err != nil {
			return err
		}
		if written != info.Size() {
			return fmt.Errorf("hashed %d of %d bytes", written, info.Size())
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("verify disposable bare executable checksum: %w", err)
	}
	return hash.Sum(nil), nil
}

func closeAndRemoveHookVerificationTarget(path string, expected os.FileInfo, target hookVerificationCopyTarget, cause error) error {
	return removeHookVerificationTarget(path, expected, errors.Join(cause, target.Close()))
}

func removeHookVerificationTarget(path string, expected os.FileInfo, cause error) error {
	current, inspectErr := os.Lstat(path)
	if os.IsNotExist(inspectErr) {
		return cause
	}
	if inspectErr != nil {
		return errors.Join(cause, fmt.Errorf("inspect disposable bare executable for cleanup: %w", inspectErr))
	}
	if !sameHookVerificationIdentity(expected, current) {
		return errors.Join(cause, errors.New("disposable bare executable changed identity before cleanup; replacement was preserved"))
	}
	removeErr := os.Remove(path)
	return errors.Join(cause, removeErr)
}

func sameHookVerificationSnapshot(left, right os.FileInfo) bool {
	return sameHookVerificationIdentity(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func sameHookVerificationIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 && os.SameFile(left, right)
}

func verifyOfflineHookKind(kind, repo string) offlineHookKindResult {
	result := offlineHookKindResult{
		artifactGeneration: "failed", configuration: "failed", transport: "failed",
		policyDecision: "failed", responseAdaptation: "failed",
	}
	if detail := verifyOfflineArtifactAndInstall(kind, repo); detail != "" {
		result.detail, result.degraded = detail, true
		return result
	}
	result.artifactGeneration, result.configuration = "verified", "verified"
	result.configured, result.discoverable = true, true
	decision := verifyOfflinePolicyDecision(kind, repo)
	result.durationMillis = decision.durationMillis
	if !decision.verified {
		result.detail, result.degraded = decision.detail, true
		return result
	}
	result.policyDecision, result.responseAdaptation = "verified", "verified"
	result.syntheticEnforced = true
	transport, adapter, unsupported, detail := verifyGeneratedHookTransport(kind, repo)
	applyOfflineTransportResult(&result, transport, adapter, unsupported, detail)
	return result
}

func verifyOfflineArtifactAndInstall(kind, repo string) string {
	artifact, err := hooks.Generate(kind)
	if err != nil {
		return "artifact generation failed: " + err.Error()
	}
	if artifact.TargetPath == "" || artifact.Content == "" {
		return "generated artifact is incomplete"
	}
	if _, err := hooks.Install(kind, repo, false); err != nil {
		return "isolated installation failed: " + err.Error()
	}
	return ""
}

type syntheticHookDecision struct {
	verified       bool
	durationMillis int64
	detail         string
}

func verifyOfflinePolicyDecision(kind, repo string) syntheticHookDecision {
	started := time.Now()
	if kind == hooks.KindGitPreCommit {
		return verifyOfflineGitDecision(repo, started)
	}
	return verifyOfflineAgentDecision(kind, repo, started)
}

func verifyOfflineGitDecision(repo string, started time.Time) syntheticHookDecision {
	stdout, err := boundedexec.NewBuffer(maxHookRuntimeCapture)
	if err != nil {
		return syntheticHookDecision{durationMillis: elapsedMillis(started), detail: err.Error()}
	}
	stderr, err := boundedexec.NewBuffer(maxHookRuntimeCapture)
	if err != nil {
		return syntheticHookDecision{durationMillis: elapsedMillis(started), detail: err.Error()}
	}
	err = runCI([]string{repo, "--staged", "--json"}, "hook-verify", stdout, stderr)
	if stdout.Truncated() || stderr.Truncated() {
		return syntheticHookDecision{durationMillis: elapsedMillis(started), detail: "synthetic staged decision output exceeded the hook limit"}
	}
	verified := ExitCode(err) == 2 && strings.Contains(stdout.String()+stderr.String(), "hook-verify-deny-write")
	detail := ""
	if !verified {
		detail = fmt.Sprintf("synthetic staged decision was not blocked (exit %d)", ExitCode(err))
	}
	return syntheticHookDecision{verified: verified, durationMillis: elapsedMillis(started), detail: detail}
}

func verifyOfflineAgentDecision(kind, repo string, started time.Time) syntheticHookDecision {
	event, ok := hooks.RuntimeEventFor(kind, hooks.EventPreToolUse)
	if !ok {
		return syntheticHookDecision{detail: "registry has no first-class pre-tool route"}
	}
	payload, err := hookVerificationPayload(kind, repo)
	if err != nil {
		return syntheticHookDecision{detail: err.Error()}
	}
	stdout, err := boundedexec.NewBuffer(maxHookRuntimeCapture)
	if err != nil {
		return syntheticHookDecision{durationMillis: elapsedMillis(started), detail: err.Error()}
	}
	stderr, err := boundedexec.NewBuffer(maxHookRuntimeCapture)
	if err != nil {
		return syntheticHookDecision{durationMillis: elapsedMillis(started), detail: err.Error()}
	}
	runtimeErr := runHookRuntimeWithInput([]string{event, repo}, bytes.NewReader(payload), stdout, stderr)
	if stdout.Truncated() || stderr.Truncated() {
		return syntheticHookDecision{durationMillis: elapsedMillis(started), detail: "synthetic agent decision output exceeded the hook limit"}
	}
	verified := hookVerificationBlocked(kind, runtimeErr, stdout.String(), stderr.String())
	detail := ""
	if !verified {
		detail = fmt.Sprintf("synthetic denied write was not blocked (exit %d)", ExitCode(runtimeErr))
	}
	return syntheticHookDecision{verified: verified, durationMillis: elapsedMillis(started), detail: detail}
}

func applyOfflineTransportResult(result *offlineHookKindResult, transport, adapter string, unsupported []string, detail string) {
	result.transport = transport
	if adapter != "not-applicable" {
		result.responseAdaptation = adapter
	}
	result.unsupported = unsupported
	if transport != "verified" || result.responseAdaptation != "verified" {
		result.degraded = true
	}
	result.detail = detail
	if result.detail == "" {
		result.detail = "artifact, isolated configuration, generated transport, policy decision, and native response contract verified; no live host execution claimed"
	}
}

func elapsedMillis(started time.Time) int64 {
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 1 {
		return 1
	}
	return elapsed
}

func hookVerificationPayload(kind, repo string) ([]byte, error) {
	var payload map[string]interface{}
	switch kind {
	case hooks.KindClaudeCode, hooks.KindCodex:
		payload = map[string]interface{}{"session_id": "verify-" + kind, "tool_name": "Write", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindOpenCode, hooks.KindKilo:
		payload = map[string]interface{}{"session_id": "verify-" + kind, "reconc_runtime": kind, "tool_name": "Write", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindDevinCLI:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-devin", "cwd": repo, "tool_name": "edit", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindCursor:
		payload = map[string]interface{}{"conversation_id": "verify-cursor", "tool_name": "Write", "tool_input": map[string]interface{}{"filePath": "forbidden.txt"}}
	case hooks.KindGitHubCopilot:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-copilot", "cwd": repo, "tool_name": "Edit", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}}
	case hooks.KindGrok:
		payload = map[string]interface{}{"hookEventName": "pre_tool_use", "sessionId": "verify-grok", "workspaceRoot": repo, "toolName": "search_replace", "toolUseId": "call-1", "toolInput": map[string]interface{}{"path": "forbidden.txt", "old_string": "a", "new_string": "b"}, "toolInputTruncated": false}
	case hooks.KindOMP, hooks.KindPi:
		payload = map[string]interface{}{"hook_event_name": "tool_call", "session_id": "verify-" + kind, "cwd": repo, "tool_name": "write", "tool_input": map[string]interface{}{"path": "forbidden.txt"}, "tool_call_id": "call-1"}
	case hooks.KindAntigravity:
		payload = map[string]interface{}{"conversationId": "verify-antigravity", "stepIdx": 1, "toolCall": map[string]interface{}{"name": "write_to_file", "args": map[string]interface{}{"TargetFile": "forbidden.txt"}}}
	case hooks.KindKimiCode:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-kimi", "cwd": repo, "tool_name": "Write", "tool_input": map[string]interface{}{"path": "forbidden.txt"}, "tool_call_id": "call-1"}
	case hooks.KindZCode:
		payload = map[string]interface{}{"hook_event_name": "PreToolUse", "session_id": "verify-zcode", "cwd": repo, "tool_name": "Write", "tool_input": map[string]interface{}{"file_path": "forbidden.txt"}, "tool_use_id": "call-1"}
	default:
		return nil, fmt.Errorf("no synthetic payload contract for %s", kind)
	}
	return json.Marshal(payload)
}

func hookVerificationBlocked(kind string, err error, stdout, stderr string) bool {
	combined := stdout + stderr
	if !strings.Contains(combined, "hook-verify-deny-write") {
		return false
	}
	switch kind {
	case hooks.KindCursor:
		return ExitCode(err) == 0 && strings.Contains(stdout, `"permission":"deny"`)
	case hooks.KindGitHubCopilot:
		return ExitCode(err) == 0 && strings.Contains(stdout, `"permissionDecision":"deny"`)
	case hooks.KindGrok, hooks.KindAntigravity:
		return ExitCode(err) == 0 && strings.Contains(stdout, `"decision":"deny"`)
	default:
		return ExitCode(err) == 2
	}
}

func verifyGeneratedHookTransport(kind, repo string) (string, string, []string, string) {
	if kind == hooks.KindKimiCode {
		return verifyGeneratedKimiTransport()
	}
	shell, err := hookVerificationShell()
	if err != nil {
		return "unavailable", "unavailable",
			[]string{"generated shell transport execution without POSIX sh"},
			"transport execution requires the shipped POSIX-compatible hook shell: " + err.Error()
	}
	if kind == hooks.KindGitPreCommit {
		return verifyGeneratedGitTransport(repo, shell)
	}
	if err := writeHookVerificationFakeBinary(repo); err != nil {
		return "failed", "failed", nil, "create transport probe: " + err.Error()
	}
	transport, detail := verifyGeneratedWrapperTransport(kind, repo, shell)
	if transport != "verified" {
		return transport, "failed", nil, detail
	}
	return verifyGeneratedAdapterTransport(kind, repo)
}

func hookVerificationShell() (string, error) {
	name := "/bin/sh"
	if runtime.GOOS == "windows" {
		name = "sh"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return path, nil
}

func verifyGeneratedGitTransport(repo, shell string) (string, string, []string, string) {
	if err := writeHookVerificationFakeBinary(repo); err != nil {
		return "failed", "failed", nil, "create transport probe: " + err.Error()
	}
	hookPath := filepath.Join(repo, filepath.FromSlash(hooks.GitPreCommitPath))
	command := exec.Command(shell, hookPath)
	command.Dir = repo
	command.Env = os.Environ()
	output, err := boundedexec.CombinedOutput(command, maxHookVerificationOutput)
	if exitCodeOfProcess(err) != 2 || !strings.Contains(string(output), "hook-verify-transport") {
		return "failed", "failed", nil, "generated pre-commit transport did not preserve the blocking exit contract"
	}
	return "verified", "not-applicable", nil, ""
}

func verifyGeneratedKimiTransport() (string, string, []string, string) {
	artifact, err := hooks.Generate(hooks.KindKimiCode)
	if err != nil || !strings.Contains(artifact.Content, `command = "reconc hook kimi-runtime receipt-v1 kimi-pre-tool-use"`) {
		return "failed", "failed", nil, "generated Kimi Code command transport is incomplete"
	}
	return "verified", "not-applicable", nil, ""
}

func verifyGeneratedWrapperTransport(kind, repo, shell string) (string, string) {
	event, ok := hooks.RuntimeEventFor(kind, hooks.EventPreToolUse)
	if !ok {
		return "failed", "registry pre-tool route missing"
	}
	wrapperPath := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	if info, statErr := os.Stat(wrapperPath); statErr != nil {
		return "failed", "generated wrapper is unavailable after installation: " + statErr.Error()
	} else if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return "failed", "generated wrapper is not a regular executable file after installation"
	}
	command := exec.Command(shell, wrapperPath, event, repo)
	command.Dir = repo
	command.Stdin = strings.NewReader("{}")
	command.Env = os.Environ()
	output, err := boundedexec.CombinedOutput(command, maxHookVerificationOutput)
	if kind == hooks.KindGrok {
		if err != nil || !strings.Contains(string(output), `"decision":"deny"`) {
			return "failed", "generated Grok wrapper did not preserve explicit deny JSON"
		}
	} else if exitCodeOfProcess(err) != 2 || !strings.Contains(string(output), "hook-verify-transport") {
		return "failed", fmt.Sprintf("generated wrapper did not preserve the blocking exit contract (exit=%d marker=%t error=%v)", exitCodeOfProcess(err), strings.Contains(string(output), "hook-verify-transport"), err)
	}
	return "verified", ""
}

func verifyGeneratedAdapterTransport(kind, repo string) (string, string, []string, string) {
	if kind != hooks.KindOpenCode && kind != hooks.KindKilo && kind != hooks.KindOMP && kind != hooks.KindPi {
		return "verified", "not-applicable", nil, ""
	}
	adapterStatus, detail := verifyGeneratedBunAdapter(kind, repo)
	if adapterStatus == "unavailable" {
		return "verified", adapterStatus, []string{"generated TypeScript adapter execution without Bun"}, detail
	}
	if adapterStatus != "verified" {
		return "failed", adapterStatus, nil, detail
	}
	return "verified", "verified", nil, ""
}

func writeHookVerificationFakeBinary(repo string) error {
	target := filepath.Join(repo, ".build", "bin", "reconc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body := `#!/bin/sh
set -eu
if [ "${1:-}" != "hook" ] && [ "${1:-}" != "ci" ]; then
  exit 9
fi
if [ "${1:-}" = "ci" ]; then
  printf '%s\n' 'hook-verify-transport' >&2
  exit 2
fi
case "${2:-}" in
  grok-pre-tool-guard)
    printf '%s\n' '{"decision":"deny","reason":"hook-verify-transport"}'
    exit 0
    ;;
  runtime)
    cat >/dev/null
    printf '%s\n' 'hook-verify-transport' >&2
    exit 2
    ;;
esac
exit 9
`
	return os.WriteFile(target, []byte(body), 0o755)
}

func exitCodeOfProcess(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return -1
	}
	return exitError.ExitCode()
}

const generatedBunAdapterDriver = `import { pathToFileURL } from "node:url"
const path = Bun.argv[2]
const kind = Bun.argv[3]
const repo = Bun.argv[4]
const module = await import(pathToFileURL(path).href + "?verify=" + Date.now())
if (kind === "opencode" || kind === "kilo") {
  const factory = kind === "opencode" ? module.ReconcOpenCodePlugin : module.default?.server
  if (typeof factory !== "function") throw new Error("plugin factory missing")
  const hooks = await factory({ directory: repo, worktree: repo, client: {} })
  let blocked = false
  try {
    await hooks["tool.execute.before"](
      { sessionID: "verify-" + kind, tool: "write", callID: "call-1", args: { file_path: "forbidden.txt" } },
      {},
    )
  } catch (error) {
    blocked = String(error?.message || error).includes("hook-verify-transport")
  }
  if (!blocked) throw new Error("plugin did not adapt blocking transport")
} else {
  if (typeof module.default !== "function") throw new Error("extension factory missing")
  const handlers = new Map()
  if (kind === "omp") module.default({ logger: { warn: () => {} }, on: (event, handler) => handlers.set(event, handler) })
  else module.default({ on: (event, handler) => handlers.set(event, handler), sendUserMessage: () => {} })
  const ctx = { cwd: repo, signal: undefined, sessionManager: { getSessionId: () => "verify-" + kind, getSessionFile: () => undefined } }
  const decision = await handlers.get("tool_call")(
    { type: "tool_call", toolCallId: "call-1", toolName: "write", input: { path: "forbidden.txt" } },
    ctx,
  )
  if (decision?.block !== true || !String(decision.reason).includes("hook-verify-transport")) {
    throw new Error("extension did not adapt blocking transport")
  }
}
`

func verifyGeneratedBunAdapter(kind, repo string) (string, string) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		return "unavailable", "Bun is unavailable; the generated wrapper and native response were verified, but the TypeScript host adapter was not executed"
	}
	platform, ok := hooks.PlatformForKind(kind)
	if !ok {
		return "failed", "platform disappeared from registry"
	}
	artifactPath := filepath.Join(repo, filepath.FromSlash(platform.TargetPath))
	driverPath := filepath.Join(filepath.Dir(artifactPath), "reconc-hook-verify.js")
	driver := generatedBunAdapterDriver
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		return "failed", "write TypeScript adapter driver: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, driverPath, artifactPath, kind, repo)
	command.Env = os.Environ()
	if output, err := boundedexec.CombinedOutput(command, maxHookVerificationOutput); err != nil {
		return "failed", fmt.Sprintf("generated TypeScript adapter failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return "verified", ""
}
