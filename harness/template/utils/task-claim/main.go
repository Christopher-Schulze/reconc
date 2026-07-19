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
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	tasksRel    = "docs/tasks.md"
	bindingsRel = "tools/reconc/harness/template/config/workflow/task-claim-bindings.yaml"
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
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func resolveTask(root string, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	tasksPath := filepath.Join(root, filepath.FromSlash(tasksRel))
	bytes, err := os.ReadFile(tasksPath)
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
	bytes, err := os.ReadFile(path)
	if err != nil {
		return bindingsConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg bindingsConfig
	if err := yaml.Unmarshal(bytes, &cfg); err != nil {
		return bindingsConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func claimsForTask(taskName string, cfg bindingsConfig) []string {
	set := map[string]bool{}
	for _, c := range cfg.DefaultClaims {
		if c != "" {
			set[c] = true
		}
	}
	for _, b := range cfg.Bindings {
		if b.Match == "" {
			continue
		}
		if strings.Contains(taskName, b.Match) {
			for _, c := range b.Claims {
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
	if len(claims) == 0 {
		fmt.Printf("task: %s\nclaims: (none -- nothing to assert)\n", taskName)
		return nil
	}
	binaryRel := reconcBinaryRel()
	binPath := filepath.Join(root, filepath.FromSlash(binaryRel))
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("reconc binary missing at %s: %w", binaryRel, err)
	}
	fmt.Printf("task: %s\n", taskName)
	for _, claim := range claims {
		cmd := exec.Command(binPath, "hook", "claim", root, claim)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("reconc hook claim %s failed: %v\n%s", claim, err, strings.TrimSpace(string(out)))
		}
		fmt.Printf("  asserted: %s\n", claim)
	}
	return nil
}

func reconcBinaryRel() string {
	name := "reconc-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name))
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "task-claim: "+format+"\n", args...)
	os.Exit(2)
}
