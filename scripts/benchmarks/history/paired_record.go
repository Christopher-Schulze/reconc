package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

type pairedRecordOptions struct {
	roots         [2]string
	outputs       [2]string
	diagnostics   [2]string
	goBinary      string
	referencePath string
	profiles      *profileOptions
}

func parsePairedRecordOptions(args []string) (pairedRecordOptions, error) {
	var options pairedRecordOptions
	flags := flag.NewFlagSet("record-pair", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "candidate repository root")
	baselineRoot := flags.String("baseline-root", "", "checked baseline source root")
	referencePath := flags.String("reference", "", "checked baseline supplying exact source and parameters")
	goBinary := flags.String("go", "go", "Go command")
	output := flags.String("output", "", "new candidate result path")
	baselineOutput := flags.String("baseline-output", "", "new baseline result path")
	profileDir := flags.String("profile-dir", "", "optional empty directory for bounded profiles")
	profileGroups := flags.String("profile-groups", "", "comma-separated benchmark groups to profile")
	diagnostics := flags.String("rss-diagnostics", "", "optional new candidate RSS process-peak report path")
	baselineDiagnostics := flags.String("baseline-rss-diagnostics", "", "optional new baseline RSS process-peak report path")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 || *baselineRoot == "" || *referencePath == "" || *output == "" || *baselineOutput == "" {
		return options, errors.New("usage: history record-pair --baseline-root PATH --reference PATH --baseline-output PATH --output PATH [--root PATH] [--go PATH] [--profile-dir PATH --profile-groups GROUP,...] [--rss-diagnostics PATH] [--baseline-rss-diagnostics PATH]")
	}
	options = pairedRecordOptions{roots: [2]string{*baselineRoot, *root},
		outputs: [2]string{*baselineOutput, *output}, diagnostics: [2]string{*baselineDiagnostics, *diagnostics},
		goBinary: *goBinary, referencePath: *referencePath}
	paths := options.outputs[:]
	for _, path := range options.diagnostics {
		if path != "" {
			paths = append(paths, path)
		}
	}
	if err := validatePairedOutputs(paths); err != nil {
		return options, err
	}
	var err error
	options.profiles, err = profileOptionsFromFlags(*root, *profileDir, *profileGroups)
	return options, err
}

func runRecordPair(args []string, stdout io.Writer) error {
	options, err := parsePairedRecordOptions(args)
	if err != nil {
		return err
	}
	reference, err := readBaseline(options.referencePath)
	if err != nil {
		return err
	}
	var collectors [2]*rssCollector
	for index, path := range options.diagnostics {
		if path != "" {
			collectors[index] = newRSSCollector()
		}
	}
	results, err := recordPairedBenchmarks(options.roots, options.goBinary, reference, collectors)
	if err != nil {
		return err
	}
	for index, result := range results {
		body, err := encodeContract(result)
		if err != nil {
			return err
		}
		if err := publishContract(options.outputs[index], body, stdout); err != nil {
			return err
		}
		if collectors[index] == nil {
			continue
		}
		report, err := encodeContract(collectors[index].report(result))
		if err != nil {
			return err
		}
		if err := publishContract(options.diagnostics[index], report, stdout); err != nil {
			return err
		}
	}
	if options.profiles != nil {
		return runProfiles(options.roots[1], options.goBinary, reference.Result.Parameters, results[1].Environment, *options.profiles)
	}
	return nil
}

func validatePairedOutputs(outputs []string) error {
	for _, path := range outputs {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return fmt.Errorf("inspect paired result output: %w", err)
			}
			return errors.New("paired result outputs must be new files")
		}
	}
	absolute, err := pathidentity.ResolveProspectiveBatch(outputs)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(absolute))
	for _, path := range absolute {
		if seen[path] {
			return errors.New("paired result outputs must be distinct")
		}
		seen[path] = true
	}
	return nil
}

// recordPairedBenchmarks records the baseline and candidate roots; each
// non-nil collector receives its root's fixed-work process peaks.
func recordPairedBenchmarks(roots [2]string, goBinary string, reference BenchmarkBaseline, diagnostics [2]*rssCollector) ([2]BenchmarkResult, error) {
	var results [2]BenchmarkResult
	if err := validateBaseline(reference); err != nil {
		return results, err
	}
	for index, root := range roots {
		environment, err := detectEnvironment(root, goBinary)
		if err != nil {
			return results, err
		}
		results[index] = BenchmarkResult{FormatVersion: resultFormat, SuiteVersion: suiteVersion,
			Environment: environment, Parameters: reference.Result.Parameters}
	}
	if err := validatePairedEnvironments(reference, results); err != nil {
		return results, err
	}
	groups, err := runSuite(roots[:], goBinary, reference.Result.Parameters, diagnostics[:])
	if err != nil {
		return results, err
	}
	for index, root := range roots {
		results[index].Groups = groups[index]
		if err := validateResult(results[index]); err != nil {
			return results, err
		}
		environment, err := detectEnvironment(root, goBinary)
		if err != nil {
			return results, err
		}
		if environment != results[index].Environment {
			return results, errors.New("paired benchmark source environment changed during recording")
		}
	}
	return results, nil
}

func validatePairedEnvironments(reference BenchmarkBaseline, results [2]BenchmarkResult) error {
	commit := reference.Result.Environment.Commit
	if !baselineCommitPattern.MatchString(commit) || results[0].Environment.Commit != commit {
		return errors.New("paired baseline root must match the exact checked source commit")
	}
	for _, result := range results {
		if result.Environment.Dirty || !baselineCommitPattern.MatchString(result.Environment.Commit) {
			return errors.New("paired benchmark roots must identify clean immutable source commits")
		}
		if result.Parameters != reference.Result.Parameters {
			return errors.New("paired benchmark parameters must match the checked baseline")
		}
	}
	if issues := compatibilityIssues(results[0], results[1]); len(issues) != 0 {
		return fmt.Errorf("paired benchmark environments are incompatible: %s", strings.Join(issues, "; "))
	}
	return nil
}
