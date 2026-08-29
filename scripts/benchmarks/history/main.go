// Command history records and compares calibrated Go benchmark history.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"reconc.dev/reconc/internal/atomicfile"
)

var benchtimePattern = regexp.MustCompile(`^[1-9][0-9]*(x|ns|us|ms|s)$`)

func main() {
	err := run(os.Args[1:], os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "benchmark-history:", err)
	if errors.Is(err, errRegression) {
		os.Exit(2)
	}
	os.Exit(1)
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: history record|compare|baseline [flags]")
	}
	switch args[0] {
	case "record":
		return runRecord(args[1:], stdout)
	case "compare":
		return runCompare(args[1:], stdout)
	case "baseline":
		return runBaseline(args[1:], stdout)
	default:
		return fmt.Errorf("unknown benchmark-history command %q", args[0])
	}
}

func runRecord(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("record", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "repository root")
	goBinary := flags.String("go", "go", "Go command")
	output := flags.String("output", "", "result path")
	count := flags.Int("count", 5, "samples per benchmark")
	benchtime := flags.String("benchtime", "100x", "Go benchmark duration or iteration count")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" || *count < 1 || *count > 20 || !validBenchtime(*benchtime) {
		return errors.New("usage: history record --output PATH [--root PATH] [--go PATH] [--count 1..20] [--benchtime VALUE]")
	}
	result, err := recordBenchmarks(*root, *goBinary, Parameters{Count: *count, Benchtime: *benchtime, CPU: 1})
	if err != nil {
		return err
	}
	body, err := encodeContract(result)
	if err != nil {
		return err
	}
	return publishContract(*output, body, stdout)
}

func runCompare(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	baselinePath := flags.String("baseline", "", "baseline path")
	resultPath := flags.String("result", "", "current result path")
	output := flags.String("output", "", "optional comparison report path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *baselinePath == "" || *resultPath == "" {
		return errors.New("usage: history compare --baseline PATH --result PATH [--output PATH]")
	}
	baseline, err := readBaseline(*baselinePath)
	if err != nil {
		return err
	}
	result, err := readResult(*resultPath)
	if err != nil {
		return err
	}
	report, compareErr := compareResults(baseline, result)
	if report.FormatVersion != "" {
		body, encodeErr := encodeContract(report)
		if encodeErr != nil {
			return encodeErr
		}
		if *output == "" {
			if _, err := stdout.Write(body); err != nil {
				return err
			}
		} else if err := publishContract(*output, body, stdout); err != nil {
			return err
		}
	}
	return compareErr
}

func runBaseline(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("baseline", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	resultPath := flags.String("result", "", "source result path")
	output := flags.String("output", "", "baseline path")
	refresh := flags.Bool("refresh", false, "confirm intentional baseline replacement")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *resultPath == "" || *output == "" || !*refresh {
		return errors.New("usage: history baseline --result PATH --output PATH --refresh")
	}
	result, err := readResult(*resultPath)
	if err != nil {
		return err
	}
	baseline, err := refreshBaseline(result)
	if err != nil {
		return err
	}
	body, err := encodeContract(baseline)
	if err != nil {
		return err
	}
	return publishContract(*output, body, stdout)
}

func publishContract(path string, body []byte, stdout io.Writer) error {
	result, err := atomicfile.WriteIfChanged(filepath.Clean(path), body, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "benchmark contract %s: changed=%t\n", path, result.Changed)
	return err
}

func validBenchtime(value string) bool {
	return benchtimePattern.MatchString(value)
}
