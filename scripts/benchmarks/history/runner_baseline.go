package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
)

var baselineCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func runBaselineCommit(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("baseline-commit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("baseline", "", "checked baseline path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *path == "" {
		return errors.New("usage: history baseline-commit --baseline PATH")
	}
	baseline, err := readBaseline(*path)
	if err != nil {
		return err
	}
	commit := baseline.Result.Environment.Commit
	if !baselineCommitPattern.MatchString(commit) {
		return errors.New("benchmark baseline commit must be a full lowercase Git object ID")
	}
	_, err = fmt.Fprintln(stdout, commit)
	return err
}

func prepareBaseline(result BenchmarkResult, referencePath, output string) (BenchmarkBaseline, error) {
	if referencePath == "" {
		return refreshBaseline(result)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return BenchmarkBaseline{}, fmt.Errorf("inspect runner baseline output: %w", err)
		}
		return BenchmarkBaseline{}, errors.New("runner baseline output must be a new file")
	}
	reference, err := readBaseline(referencePath)
	if err != nil {
		return BenchmarkBaseline{}, err
	}
	return bindRunnerBaseline(reference, result)
}

func bindRunnerBaseline(reference BenchmarkBaseline, result BenchmarkResult) (BenchmarkBaseline, error) {
	if err := validateBaseline(reference); err != nil {
		return BenchmarkBaseline{}, err
	}
	if result.FormatVersion != resultFormat {
		return BenchmarkBaseline{}, errors.New("runner baseline requires precompiled benchmark measurements")
	}
	commit := reference.Result.Environment.Commit
	if !baselineCommitPattern.MatchString(commit) || result.Environment.Commit != commit {
		return BenchmarkBaseline{}, errors.New("runner result must match the exact checked baseline source commit")
	}
	if result.Parameters != reference.Result.Parameters {
		return BenchmarkBaseline{}, errors.New("runner result must match the checked baseline parameters")
	}
	reference.Result = result
	reference.FormatVersion = baselineFormat
	return reference, validateBaseline(reference)
}
