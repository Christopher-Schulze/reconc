// Package main implements task-claim: a helper that auto-asserts the
// session-scoped Reconc claims relevant to the active TASK so the agent does
// not have to remember which protected-path claims belong to which TASK.
//
// Reconc claims are session-scoped (one assertion lives until the agent
// session ends or the cache is cleared). This tool reads the active TASK
// from docs/tasks.md, looks up the matching claim set in
// tools/reconc/harness/template/config/workflow/task-claim-bindings.yaml, and forwards each claim
// to `reconc hook claim <repo> <claim-name>` -- the public CLI wrapper around
// the agentsession.RecordClaim internal API.
//
// Run from the repository root:
//
//	tools/reconc/harness/template/utils/task-claim/run-task-claim assert
//	tools/reconc/harness/template/utils/task-claim/run-task-claim show
//	tools/reconc/harness/template/utils/task-claim/run-task-claim assert --task TASK-0099-X
//
// The tool fails closed: missing tasks.md, missing bindings file, malformed
// YAML, or `reconc hook claim` non-zero exit each abort with a clear error.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"reconc-harness/template/audits/lib/reconcbinary"

	"gopkg.in/yaml.v3"
)

const (
	tasksRel           = "docs/tasks.md"
	bindingsRel        = "tools/reconc/harness/template/config/workflow/task-claim-bindings.yaml"
	claimTimeout       = 30 * time.Second
	maxInputBytes      = 4 << 20
	maxOutputBytes     = 64 << 10
	maxBindingEntries  = 4096
	maxClaimEntries    = 8192
	maxBindingTextSize = 1024
)

var currentRe = regexp.MustCompile(`(?m)^Current: (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) -> tasks/`)
var taskNameRe = regexp.MustCompile(`^TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)

type commandOptions struct {
	command      string
	taskOverride string
}

type binding struct {
	Match  string   `yaml:"match"`
	Claims []string `yaml:"claims"`
}

type bindingsConfig struct {
	DefaultClaims []string  `yaml:"default_claims"`
	Bindings      []binding `yaml:"bindings"`
}

func main() {
	options, err := parseCommand(os.Args[1:])
	if err != nil {
		printUsage(os.Stderr)
		fail("%v", err)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		fail("get cwd: %v", err)
	}
	root, err := findRepoRoot(workingDir)
	if err != nil {
		fail("%v", err)
	}
	taskName, err := resolveTask(root, options.taskOverride)
	if err != nil {
		fail("%v", err)
	}
	bindings, err := loadBindings(filepath.Join(root, filepath.FromSlash(bindingsRel)))
	if err != nil {
		fail("%v", err)
	}
	claims := claimsForTask(taskName, bindings)
	switch options.command {
	case "show":
		printPlan(taskName, claims)
	case "assert":
		if err := assertClaims(root, taskName, claims); err != nil {
			fail("%v", err)
		}
	}
}

func parseCommand(args []string) (commandOptions, error) {
	if len(args) == 0 {
		return commandOptions{}, fmt.Errorf("missing command")
	}
	command := args[0]
	if command != "show" && command != "assert" {
		return commandOptions{}, fmt.Errorf("unknown command %q", command)
	}
	flags := flag.NewFlagSet("task-claim "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	taskOverride := flags.String("task", "", "explicit TASK name; defaults to Current: from tasks.md")
	if err := flags.Parse(args[1:]); err != nil {
		return commandOptions{}, fmt.Errorf("parse %s flags: %w", command, err)
	}
	if flags.NArg() != 0 {
		return commandOptions{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *taskOverride != "" && !taskNameRe.MatchString(*taskOverride) {
		return commandOptions{}, fmt.Errorf("invalid TASK name %q", *taskOverride)
	}
	return commandOptions{command: command, taskOverride: *taskOverride}, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: task-claim <show|assert> [--task TASK-NNNN-Name]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "show   - print the claim set that would be asserted, no side effects")
	fmt.Fprintln(w, "assert - call `reconc hook claim` for every claim in the set")
}

func findRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve start path %s: %w", start, err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve start identity %s: %w", start, err)
	}
	for {
		if isRegularFile(filepath.Join(dir, filepath.FromSlash(tasksRel))) &&
			isRegularFile(filepath.Join(dir, filepath.FromSlash(bindingsRel))) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("repository root not found from %s: require %s and %s", start, tasksRel, bindingsRel)
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}

func resolveTask(root string, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	tasksPath := filepath.Join(root, filepath.FromSlash(tasksRel))
	bytes, err := readRegularFile(tasksPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", tasksRel, err)
	}
	match := currentRe.FindStringSubmatch(string(bytes))
	if match == nil {
		return "", fmt.Errorf("%s has no Current: header pointing to an open TASK", tasksRel)
	}
	return match[1], nil
}

func loadBindings(path string) (bindingsConfig, error) {
	body, err := readRegularFile(path)
	if err != nil {
		return bindingsConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg bindingsConfig
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return bindingsConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return bindingsConfig{}, fmt.Errorf("parse %s: multiple YAML documents are not allowed", path)
		}
		return bindingsConfig{}, fmt.Errorf("parse trailing document in %s: %w", path, err)
	}
	if err := validateBindings(cfg); err != nil {
		return bindingsConfig{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func validateBindings(cfg bindingsConfig) error {
	if len(cfg.Bindings) > maxBindingEntries {
		return fmt.Errorf("bindings count %d exceeds %d", len(cfg.Bindings), maxBindingEntries)
	}
	totalClaims := len(cfg.DefaultClaims)
	for _, item := range cfg.Bindings {
		if len(item.Match) > maxBindingTextSize {
			return fmt.Errorf("binding match exceeds %d bytes", maxBindingTextSize)
		}
		totalClaims += len(item.Claims)
	}
	if totalClaims > maxClaimEntries {
		return fmt.Errorf("claim entry count %d exceeds %d", totalClaims, maxClaimEntries)
	}
	for _, claim := range cfg.DefaultClaims {
		if len(claim) > maxBindingTextSize {
			return fmt.Errorf("default claim exceeds %d bytes", maxBindingTextSize)
		}
	}
	for _, item := range cfg.Bindings {
		for _, claim := range item.Claims {
			if len(claim) > maxBindingTextSize {
				return fmt.Errorf("claim for match %q exceeds %d bytes", item.Match, maxBindingTextSize)
			}
		}
	}
	return nil
}

func claimsForTask(taskName string, cfg bindingsConfig) []string {
	set := map[string]bool{}
	for _, c := range cfg.DefaultClaims {
		c = strings.TrimSpace(c)
		if c != "" {
			set[c] = true
		}
	}
	for _, b := range cfg.Bindings {
		b.Match = strings.TrimSpace(b.Match)
		if b.Match == "" {
			continue
		}
		if strings.Contains(taskName, b.Match) {
			for _, c := range b.Claims {
				c = strings.TrimSpace(c)
				if c != "" {
					set[c] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func printPlan(taskName string, claims []string) {
	fmt.Printf("task: %s\n", taskName)
	if len(claims) == 0 {
		fmt.Println("claims: (none -- this TASK has no auto-claim binding)")
		return
	}
	fmt.Println("claims:")
	for _, c := range claims {
		fmt.Printf("  - %s\n", c)
	}
}

func assertClaims(root string, taskName string, claims []string) error {
	return assertClaimsWithTimeout(root, taskName, claims, claimTimeout)
}

func assertClaimsWithTimeout(root string, taskName string, claims []string, timeout time.Duration) error {
	if len(claims) == 0 {
		fmt.Printf("task: %s\nclaims: (none -- nothing to assert)\n", taskName)
		return nil
	}
	verified, err := reconcbinary.Open(root, true)
	if err != nil {
		return err
	}
	defer verified.Close()
	snapshot, err := verified.Snapshot()
	if err != nil {
		return fmt.Errorf("prepare verified Reconc claim authority: %w", err)
	}
	defer snapshot.Close()
	fmt.Printf("task: %s\n", taskName)
	for _, claim := range claims {
		if err := errors.Join(verified.Revalidate(), snapshot.Revalidate()); err != nil {
			return fmt.Errorf("revalidate Reconc claim authority before %s: %w", claim, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, snapshot.Path(), "hook", "claim", root, claim)
		cmd.WaitDelay = 2 * time.Second
		var stdout boundedOutput
		var stderr boundedOutput
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Start()
		if err == nil {
			if identityErr := errors.Join(verified.Revalidate(), snapshot.Revalidate()); identityErr != nil {
				killErr := cmd.Process.Kill()
				waitErr := cmd.Wait()
				cancel()
				return fmt.Errorf("reconc claim authority changed during %s process setup: %w", claim, errors.Join(identityErr, killErr, waitErr))
			}
			err = cmd.Wait()
		}
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()
		if timedOut {
			return fmt.Errorf("reconc hook claim %s timed out after %s", claim, timeout)
		}
		if err != nil {
			return fmt.Errorf("reconc hook claim %s failed: %v\n%s", claim, err, strings.TrimSpace(stdout.String()+stderr.String()))
		}
		fmt.Printf("  asserted: %s\n", claim)
	}
	return nil
}

type boundedOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (output *boundedOutput) Write(value []byte) (int, error) {
	remaining := maxOutputBytes - output.buffer.Len()
	if remaining > 0 {
		if len(value) < remaining {
			remaining = len(value)
		}
		_, _ = output.buffer.Write(value[:remaining])
	}
	if len(value) > remaining {
		output.truncated = true
	}
	return len(value), nil
}

func (output *boundedOutput) String() string {
	if output.truncated {
		return output.buffer.String() + "\n[output truncated]"
	}
	return output.buffer.String()
}

func readRegularFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("must be a non-symlink regular file")
	}
	if before.Size() > maxInputBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxInputBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxInputBytes+1))
	afterFile, statErr := file.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, lstatErr, closeErr); err != nil {
		return nil, err
	}
	if len(body) > maxInputBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxInputBytes)
	}
	if !os.SameFile(before, afterFile) || !os.SameFile(afterFile, afterPath) ||
		before.Mode() != afterFile.Mode() || before.Size() != afterFile.Size() ||
		!before.ModTime().Equal(afterFile.ModTime()) {
		return nil, fmt.Errorf("changed while reading")
	}
	return body, nil
}

func reconcBinaryRel() string {
	return reconcbinary.RelativePath()
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "task-claim: "+format+"\n", args...)
	os.Exit(2)
}
