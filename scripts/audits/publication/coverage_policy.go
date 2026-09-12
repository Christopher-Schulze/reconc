package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

const (
	coverageNumber      = `[0-9]+([.][0-9]+)?`
	coveragePercent     = coverageNumber + `[[:space:]]*(%|percent)`
	thresholdWords      = `min(imum)?|floor|threshold|gate`
	coverageRequirement = `must|shall|should|require[ds]?|enforce[ds]?|fails?|rejects?`
	coverageSubject     = `((numeric|minimum)[[:space:]]+)?coverage([[:space:]]+(minimum|floor|threshold|gate))?`
	coverageAmount      = `([[:space:]]+(of|at)[[:space:]]+` + coveragePercent + `)?`
)

var (
	coverageSyntax = regexp.MustCompilePOSIX(
		`(coverage[-_ ]*(` + thresholdWords + `)|(` + thresholdWords + `)[-_ ]*coverage)["'[:space:]]*[:=]|coverage["'[:space:]]*[:=][[:space:]]*[{]["'[:space:]]*(` + thresholdWords + `)["'[:space:]]*[:=]|` +
			`coverage["'[:space:]_[:alnum:].()-]{0,24}([<>]=?|-(lt|le|gt|ge))[[:space:]]*` + coverageNumber)
	coveragePolicy = regexp.MustCompilePOSIX(
		`coverage[[:space:]]+((` + thresholdWords + `)[[:space:]]+)?(is[[:space:]]+)?(` + coverageRequirement + `|at least|no less than).{0,80}` + coveragePercent + `|` +
			`(^|[^[:alnum:]_])(` + coverageRequirement + `).{0,80}coverage.{0,40}` + coveragePercent + `|` +
			`(^|[^[:alnum:]_])(` + coverageRequirement + `|at least|no less than).{0,40}` + coveragePercent + `.{0,40}coverage|` +
			`((` + thresholdWords + `)[-_ ]+coverage|coverage[-_ ]+(` + thresholdWords + `))[[:space:]]*(:|=|is|of|at)?[[:space:]]*` + coveragePercent + `|` +
			coveragePercent + `[[:space:]]+coverage[[:space:]]+(is[[:space:]]+)?required`)
	coverageClauses   = regexp.MustCompilePOSIX(`[.][[:space:]]+|[;!?]`)
	coverageNegations = []*regexp.Regexp{
		regexp.MustCompilePOSIX(`(^|[[:space:]])no[[:space:]]+` + coverageSubject + coverageAmount + `([[:space:]]+is)?[[:space:]]+(required|enforced|imposed)`),
		regexp.MustCompilePOSIX(coverageSubject + coverageAmount + `[[:space:]]+(is[[:space:]]+)?not[[:space:]]+(required|enforced)`),
		regexp.MustCompilePOSIX(`do(es)?[[:space:]]+not[[:space:]]+(require|enforce)`),
	}
)

type coveragePolicyError struct {
	Path string
	Line int
}

func (e *coveragePolicyError) Error() string {
	return fmt.Sprintf("%s:%d contains a numeric coverage pass/fail contract", e.Path, e.Line)
}

type coverageScanError struct{ Cause error }

func (e *coverageScanError) Error() string { return "coverage scan failed: " + e.Cause.Error() }
func (e *coverageScanError) Unwrap() error { return e.Cause }

// Match the old LC_ALL=C byte semantics, including invalid UTF-8 and proximity
// counts. Every non-ASCII byte becomes one invalid UTF-8 byte, hence one regexp
// rune; it cannot become ASCII syntax, whitespace, or a multibyte character.
func coverageRequirementLine(line []byte) bool {
	for i, value := range line {
		switch {
		case value >= 'A' && value <= 'Z':
			line[i] = value + ('a' - 'A')
		case value >= 0x80:
			line[i] = 0xff
		}
	}
	if !bytes.Contains(line, []byte("coverage")) {
		return false
	}
	if coverageSyntax.Match(line) {
		return true
	}
	for _, clause := range coverageClauses.Split(string(line), -1) {
		for index, negation := range coverageNegations {
			replacement := " "
			if index == 2 {
				replacement = ""
			}
			clause = negation.ReplaceAllString(clause, replacement)
		}
		if coveragePolicy.MatchString(clause) {
			return true
		}
	}
	return false
}

func scanCoverageReader(ctx context.Context, path string, input io.Reader) error {
	reader := bufio.NewReader(input)
	for lineNumber := 1; ; lineNumber++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read %s:%d: %w", path, lineNumber, err)
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if coverageRequirementLine(line) {
			return &coveragePolicyError{Path: path, Line: lineNumber}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func scanCoverageFile(ctx context.Context, path string) (resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = &coverageScanError{Cause: errors.Join(resultErr, err)}
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("coverage input is not a regular file: %s", path)
	}
	return scanCoverageReader(ctx, path, file)
}

func coverageProjectText(relative string, entry fs.DirEntry) bool {
	if !entry.Type().IsRegular() || relative == "scripts/tests/release-trust.sh" {
		return false
	}
	if entry.Name() == "Makefile" {
		return true
	}
	switch filepath.Ext(entry.Name()) {
	case ".go", ".md", ".sh", ".yml", ".yaml", ".toml", ".json":
		return true
	default:
		return false
	}
}

func scanCoverageTree(ctx context.Context, root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root && !entry.IsDir() {
			return fmt.Errorf("coverage scan root is not a directory: %s", root)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch relative {
			case ".git", ".build", ".reconc", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !coverageProjectText(filepath.ToSlash(relative), entry) {
			return nil
		}
		count++
		return scanCoverageFile(ctx, path)
	})
	return count, err
}

func runCoverageAudit(ctx context.Context, root string, paths []string, stdout io.Writer) error {
	count := 0
	var err error
	if len(paths) == 0 {
		count, err = scanCoverageTree(ctx, root)
	} else {
		for _, path := range paths {
			count++
			if err = scanCoverageFile(ctx, path); err != nil {
				break
			}
		}
	}
	var scan *coverageScanError
	if errors.As(err, &scan) {
		return err
	}
	var policy *coveragePolicyError
	if errors.As(err, &policy) {
		return err
	}
	if err != nil {
		return &coverageScanError{Cause: err}
	}
	_, err = fmt.Fprintf(stdout, "coverage-review-only: ok (%d files)\n", count)
	if err != nil {
		return &coverageScanError{Cause: err}
	}
	return nil
}
