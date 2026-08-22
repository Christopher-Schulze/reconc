package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
)

const maxBenchmarkOutput = 16 << 20

type groupSpec struct {
	Name        string
	Package     string
	Calibration string
	Targets     []string
}

var benchmarkSuite = []groupSpec{
	{Name: "action-bounded-trace", Package: "./internal/action", Calibration: "BenchmarkActionEvaluatorRepresentativeCalibrated", Targets: []string{"BenchmarkActionEvaluatorMaximumLegalPlanCalibrated", "BenchmarkActionContextRootPredicates"}},
	{Name: "action-decision-cache", Package: "./internal/action", Calibration: "BenchmarkPreparedDecisionCacheHit", Targets: []string{"BenchmarkPreparedDecisionCacheStore"}},
	{Name: "action-ledger-checkpoint", Package: "./internal/actionledger", Calibration: "BenchmarkLedgerCheckpointAdvanceNoActiveCalls", Targets: []string{"BenchmarkLedgerCheckpointAdvanceActive256", "BenchmarkLedgerCheckpointAdvanceTerminal65536"}},
	{Name: "action-structured-inspection", Package: "./internal/actioninspect", Calibration: "BenchmarkStructuredJSONRepresentative", Targets: []string{"BenchmarkMaximumLegalContentArray"}},
	{Name: "compiler-canonical-json", Package: "./internal/compiler", Calibration: "BenchmarkNormalizeJSONValueTwice", Targets: []string{"BenchmarkNormalizeJSONValueOnce"}},
	{Name: "hook-worker-frame-growth", Package: "./internal/cli", Calibration: "BenchmarkHookWorkerFrameRepresentativeCalibrated", Targets: []string{"BenchmarkHookWorkerFrameLarge"}},
	{Name: "ingest-source-context", Package: "./internal/ingest", Calibration: "BenchmarkLoadPolicySourcesWithDiscovery", Targets: []string{"BenchmarkLoadPolicySourcesWithContext"}},
	{Name: "prospective-path-resolution", Package: "./internal/pathidentity", Calibration: "BenchmarkResolveProspectiveIndependent", Targets: []string{"BenchmarkResolveProspectiveBatch"}},
	{Name: "runtime-command-matching", Package: "./internal/runtime", Calibration: "BenchmarkForbiddenCommandReparse", Targets: []string{"BenchmarkForbiddenCommandPrepared"}},
	{Name: "runtime-command-evidence", Package: "./internal/runtime", Calibration: "BenchmarkCommandEvidenceReparse", Targets: []string{"BenchmarkCommandEvidencePrepared"}},
	{Name: "runtime-source-freshness", Package: "./internal/runtime", Calibration: "BenchmarkRuntimePlanFreshnessHit", Targets: []string{"BenchmarkRuntimePlanFreshnessLargeSourceSet"}},
	{Name: "runtime-write-epochs", Package: "./internal/runtime", Calibration: "BenchmarkNormalizeWriteEpochsPerPath", Targets: []string{"BenchmarkNormalizeWriteEpochsBatch"}},
}

type goEnvironment struct {
	GoVersion string `json:"GOVERSION"`
	GOOS      string `json:"GOOS"`
	GOARCH    string `json:"GOARCH"`
}

func recordBenchmarks(root, goBinary string, parameters Parameters) (BenchmarkResult, error) {
	environment, err := detectEnvironment(root, goBinary)
	if err != nil {
		return BenchmarkResult{}, err
	}
	samples, err := runSuite(root, goBinary, parameters)
	if err != nil {
		return BenchmarkResult{}, err
	}
	groups, err := buildGroups(samples, parameters.Count)
	if err != nil {
		return BenchmarkResult{}, err
	}
	result := BenchmarkResult{
		FormatVersion: resultFormat, SuiteVersion: suiteVersion,
		Environment: environment, Parameters: parameters, Groups: groups,
	}
	return result, validateResult(result)
}

func runSuite(root, goBinary string, parameters Parameters) (map[string][]MetricSample, error) {
	byPackage := make(map[string][]string)
	for _, group := range benchmarkSuite {
		byPackage[group.Package] = append(byPackage[group.Package], group.Calibration)
		byPackage[group.Package] = append(byPackage[group.Package], group.Targets...)
	}
	packages := make([]string, 0, len(byPackage))
	for packageName := range byPackage {
		packages = append(packages, packageName)
	}
	sort.Strings(packages)
	all := make(map[string][]MetricSample)
	for _, packageName := range packages {
		names := uniqueSorted(byPackage[packageName])
		parsed, err := runPackageBenchmarks(root, goBinary, packageName, names, parameters)
		if err != nil {
			return nil, err
		}
		for name, values := range parsed {
			if _, exists := all[name]; exists {
				return nil, fmt.Errorf("benchmark %s was emitted by multiple packages", name)
			}
			all[name] = values
		}
	}
	return all, nil
}

func runPackageBenchmarks(root, goBinary, packageName string, names []string, parameters Parameters) (map[string][]MetricSample, error) {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = regexp.QuoteMeta(name)
	}
	pattern := "^(" + strings.Join(quoted, "|") + ")$"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	all := make(map[string][]MetricSample, len(names))
	for sampleIndex := 0; sampleIndex < parameters.Count; sampleIndex++ {
		parsed, err := runBenchmarkSample(ctx, root, goBinary, packageName, pattern, parameters, sampleIndex)
		if err != nil {
			return nil, err
		}
		if len(parsed) != len(names) {
			return nil, fmt.Errorf("benchmark package %s sample %d emitted %v, want %v", packageName, sampleIndex+1, sortedBenchmarkNames(parsed), names)
		}
		for _, name := range names {
			if len(parsed[name]) != 1 {
				return nil, fmt.Errorf("benchmark %s sample %d emitted %d measurements, want 1", name, sampleIndex+1, len(parsed[name]))
			}
			all[name] = append(all[name], parsed[name][0])
		}
	}
	return all, nil
}

func runBenchmarkSample(ctx context.Context, root, goBinary, packageName, pattern string, parameters Parameters, sampleIndex int) (map[string][]MetricSample, error) {
	args := []string{"test", "-json", "-run", "^$", "-bench", pattern, "-benchmem", "-count", "1", "-benchtime", parameters.Benchtime, "-cpu", strconv.Itoa(parameters.CPU), "-timeout", "5m", packageName}
	command := exec.CommandContext(ctx, goBinary, args...)
	command.Dir = root
	output, err := boundedexec.Output(command, maxBenchmarkOutput)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("benchmark package %s timed out: %w", packageName, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("benchmark package %s sample %d failed: %w", packageName, sampleIndex+1, err)
	}
	parsed, err := parseBenchmarkJSON(output)
	if err != nil {
		return nil, fmt.Errorf("parse benchmark package %s sample %d: %w", packageName, sampleIndex+1, err)
	}
	return parsed, nil
}

func sortedBenchmarkNames(samples map[string][]MetricSample) []string {
	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildGroups(samples map[string][]MetricSample, count int) ([]GroupResult, error) {
	groups := make([]GroupResult, 0, len(benchmarkSuite))
	for _, spec := range benchmarkSuite {
		calibration, err := statsFor(spec.Calibration, samples[spec.Calibration], count)
		if err != nil {
			return nil, err
		}
		group := GroupResult{Name: spec.Name, Package: spec.Package, Calibration: calibration}
		for _, targetName := range spec.Targets {
			target, err := statsFor(targetName, samples[targetName], count)
			if err != nil {
				return nil, err
			}
			normalized, err := normalize(target.Median, calibration.Median)
			if err != nil {
				return nil, fmt.Errorf("normalize %s against %s: %w", targetName, spec.Calibration, err)
			}
			group.Targets = append(group.Targets, TargetResult{Benchmark: target, Normalized: normalized})
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func statsFor(name string, samples []MetricSample, count int) (BenchmarkStats, error) {
	if len(samples) != count {
		return BenchmarkStats{}, fmt.Errorf("benchmark %s has %d samples, want %d", name, len(samples), count)
	}
	stats := BenchmarkStats{Name: name, Samples: append([]MetricSample(nil), samples...), Median: medianMetrics(samples)}
	return stats, validateStats(stats, count)
}

func normalize(target, calibration MetricValues) (MetricValues, error) {
	if calibration.NSPerOp <= 0 || calibration.BytesPerOp <= 0 || calibration.AllocsPerOp <= 0 {
		return MetricValues{}, errors.New("calibration metrics must all be positive")
	}
	return MetricValues{
		NSPerOp:     target.NSPerOp / calibration.NSPerOp,
		BytesPerOp:  target.BytesPerOp / calibration.BytesPerOp,
		AllocsPerOp: target.AllocsPerOp / calibration.AllocsPerOp,
	}, nil
}

func detectEnvironment(root, goBinary string) (Environment, error) {
	goOutput, err := commandOutput(root, 10*time.Second, 8<<10, goBinary, "env", "-json", "GOVERSION", "GOOS", "GOARCH")
	if err != nil {
		return Environment{}, fmt.Errorf("inspect Go environment: %w", err)
	}
	var goEnv goEnvironment
	if err := json.Unmarshal(goOutput, &goEnv); err != nil || goEnv.GoVersion == "" || goEnv.GOOS == "" || goEnv.GOARCH == "" {
		return Environment{}, errors.New("go environment output is incomplete")
	}
	cpu, err := cpuIdentity(root, goEnv.GOOS)
	if err != nil {
		return Environment{}, err
	}
	commitOutput, err := commandOutput(root, 10*time.Second, 8<<10, "git", "rev-parse", "HEAD")
	if err != nil {
		return Environment{}, fmt.Errorf("inspect repository commit: %w", err)
	}
	status, err := commandOutput(root, 10*time.Second, 1<<20, "git", "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return Environment{}, fmt.Errorf("inspect repository status: %w", err)
	}
	return Environment{
		GoVersion: goEnv.GoVersion, GOOS: goEnv.GOOS, GOARCH: goEnv.GOARCH,
		CPU: cpu, Commit: strings.TrimSpace(string(commitOutput)), Dirty: strings.TrimSpace(string(status)) != "",
	}, nil
}

func cpuIdentity(root, goos string) (string, error) {
	var body []byte
	var err error
	switch goos {
	case "darwin":
		body, err = commandOutput(root, 10*time.Second, 8<<10, "sysctl", "-n", "machdep.cpu.brand_string")
	case "linux":
		body, err = commandOutput(root, 10*time.Second, 1<<20, "cat", "/proc/cpuinfo")
		if err == nil {
			body = []byte(firstCPUField(string(body), "model name", "Hardware", "Processor"))
		}
	case "windows":
		body, err = commandOutput(root, 15*time.Second, 8<<10, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_Processor | Select-Object -First 1 -ExpandProperty Name)")
	default:
		return "", fmt.Errorf("cpu identity is unsupported on %s", goos)
	}
	identity := strings.Join(strings.Fields(string(body)), " ")
	if err != nil || identity == "" {
		return "", errors.New("cpu identity is unavailable")
	}
	return identity, nil
}

func firstCPUField(body string, names ...string) string {
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		for _, name := range names {
			if strings.TrimSpace(key) == name {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func commandOutput(root string, timeout time.Duration, limit int, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = root
	output, err := boundedexec.Output(command, limit)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return output, err
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	sort.Strings(unique)
	return unique
}
