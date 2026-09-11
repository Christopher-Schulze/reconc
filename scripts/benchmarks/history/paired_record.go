package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"reconc.dev/reconc/internal/pathidentity"
)

type pairedRecordOptions struct {
	roots         [2]string
	outputs       [2]string
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
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 || *baselineRoot == "" || *referencePath == "" || *output == "" || *baselineOutput == "" {
		return options, errors.New("usage: history record-pair --baseline-root PATH --reference PATH --baseline-output PATH --output PATH [--root PATH] [--go PATH] [--profile-dir PATH --profile-groups GROUP,...]")
	}
	options = pairedRecordOptions{roots: [2]string{*baselineRoot, *root},
		outputs: [2]string{*baselineOutput, *output}, goBinary: *goBinary, referencePath: *referencePath}
	if err := validatePairedOutputs(options.outputs); err != nil {
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
	results, err := recordPairedBenchmarks(options.roots, options.goBinary, reference)
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
	}
	if options.profiles != nil {
		return runProfiles(options.roots[1], options.goBinary, reference.Result.Parameters, results[1].Environment, *options.profiles)
	}
	return nil
}

func validatePairedOutputs(outputs [2]string) error {
	for _, path := range outputs {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return fmt.Errorf("inspect paired result output: %w", err)
			}
			return errors.New("paired result outputs must be new files")
		}
	}
	absolute, err := pathidentity.ResolveProspectiveBatch(outputs[:])
	if err != nil {
		return err
	}
	if absolute[0] == absolute[1] {
		return errors.New("paired result outputs must be distinct")
	}
	return nil
}

func recordPairedBenchmarks(roots [2]string, goBinary string, reference BenchmarkBaseline) ([2]BenchmarkResult, error) {
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
	groups, err := runPairedSuite(roots, goBinary, reference.Result.Parameters)
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

func runPairedSuite(roots [2]string, goBinary string, parameters Parameters) ([2][]GroupResult, error) {
	var groups [2][]GroupResult
	byPackage, packages := benchmarkPackages()
	all := [2]map[string][]MetricSample{make(map[string][]MetricSample), make(map[string][]MetricSample)}
	cpu := [2]map[string][]MetricSample{make(map[string][]MetricSample), make(map[string][]MetricSample)}
	for _, packageName := range packages {
		samples, calibrations, err := runPairedPackageBenchmarks(context.Background(), roots, goBinary, packageName, byPackage[packageName], parameters)
		if err != nil {
			return groups, err
		}
		for index := range roots {
			cpu[index][packageName] = calibrations[index]
			for name, values := range samples[index] {
				if _, exists := all[index][name]; exists {
					return groups, fmt.Errorf("paired benchmark %s was emitted by multiple packages", name)
				}
				all[index][name] = values
			}
		}
	}
	for index := range roots {
		var err error
		groups[index], err = buildGroups(all[index], cpu[index], parameters.Count)
		if err != nil {
			return groups, err
		}
	}
	return groups, nil
}

func runPairedPackageBenchmarks(parent context.Context, roots [2]string, goBinary, packageName string, names []string, parameters Parameters) ([2]map[string][]MetricSample, [2][]MetricSample, error) {
	all := [2]map[string][]MetricSample{make(map[string][]MetricSample), make(map[string][]MetricSample)}
	var cpu [2][]MetricSample
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	one := parameters
	one.Count = 1
	for sample := 0; sample < parameters.Count; sample++ {
		first := sample % 2
		for _, index := range [2]int{first, 1 - first} {
			values, calibration, err := runPackageBenchmarks(ctx, roots[index], goBinary, packageName, names, one)
			if err != nil {
				return all, cpu, fmt.Errorf("paired root %d package %s sample %d: %w", index, packageName, sample+1, err)
			}
			cpu[index] = append(cpu[index], calibration...)
			for name, samples := range values {
				all[index][name] = append(all[index][name], samples...)
			}
		}
	}
	return all, cpu, nil
}
