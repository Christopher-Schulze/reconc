package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
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
	{Name: "compiler-conflict-scaling", Package: "./internal/compiler", Calibration: "BenchmarkDetectConflictsUniqueRules", Targets: []string{"BenchmarkDetectConflictsGroupedDuplicates"}},
	{Name: "hook-worker-frame-growth", Package: "./internal/cli", Calibration: "BenchmarkHookWorkerFrameRepresentativeCalibrated", Targets: []string{"BenchmarkHookWorkerFrameLarge"}},
	{Name: "hook-worker-end-to-end", Package: "./internal/cli", Calibration: "BenchmarkHookRuntimeTransport/stdio-worker", Targets: []string{"BenchmarkHookRuntimeTransport/one-shot"}},
	{Name: "ingest-source-context", Package: "./internal/ingest", Calibration: "BenchmarkLoadPolicySourcesWithDiscovery", Targets: []string{"BenchmarkLoadPolicySourcesWithContext"}},
	{Name: "mcp-frame-routing", Package: "./internal/mcpgateway", Calibration: "BenchmarkParseFrameSmall", Targets: []string{"BenchmarkParseFrameProgress", "BenchmarkParseFrameRepresentative"}},
	{Name: "prospective-path-resolution", Package: "./internal/pathidentity", Calibration: "BenchmarkResolveProspectiveIndependent", Targets: []string{"BenchmarkResolveProspectiveBatch"}},
	{Name: "runtime-command-matching", Package: "./internal/runtime", Calibration: "BenchmarkForbiddenCommandReparse", Targets: []string{"BenchmarkForbiddenCommandPrepared"}},
	{Name: "runtime-command-evidence", Package: "./internal/runtime", Calibration: "BenchmarkCommandEvidenceReparse", Targets: []string{"BenchmarkCommandEvidencePrepared"}},
	{Name: "runtime-evaluation-memos", Package: "./internal/runtime", Calibration: "BenchmarkEvidenceMatchMemoShared", Targets: []string{"BenchmarkMatchContextMemoHit"}},
	{Name: "runtime-execution-input", Package: "./internal/runtime", Calibration: "BenchmarkLoadExecutionInputsEvents256", Targets: []string{"BenchmarkLoadExecutionInputsEvents8192"}},
	{Name: "runtime-lockfile-decode", Package: "./internal/runtime", Calibration: "BenchmarkDecodeCurrentLockfileRepresentative", Targets: []string{"BenchmarkDecodeCurrentLockfileMaximumRules"}},
	{Name: "runtime-source-freshness", Package: "./internal/runtime", Calibration: "BenchmarkRuntimePlanFreshnessHit", Targets: []string{"BenchmarkRuntimePlanFreshnessLargeSourceSet", "BenchmarkRuntimePlanConcurrentRoots"}},
	{Name: "runtime-write-epochs", Package: "./internal/runtime", Calibration: "BenchmarkNormalizeWriteEpochsPerPath", Targets: []string{"BenchmarkNormalizeWriteEpochsBatch"}},
	{Name: "session-evidence-workloads", Package: "./internal/runtime/agentsession", Calibration: "BenchmarkVerifiedEvidencePrefix", Targets: []string{"BenchmarkWorkerPreHookVerifiedEvidencePrefix/warm-prefix", "BenchmarkWorkerPreHookVerifiedEvidencePrefix/cold-prefix"}},
}

type goEnvironment struct {
	GoVersion string `json:"GOVERSION"`
	GOOS      string `json:"GOOS"`
	GOARCH    string `json:"GOARCH"`
}

func recordBenchmarksWithProfiles(root, goBinary string, parameters Parameters, profiles *profileOptions) (BenchmarkResult, error) {
	environment, err := detectEnvironment(root, goBinary)
	if err != nil {
		return BenchmarkResult{}, err
	}
	samples, cpuCalibrations, err := runSuite(root, goBinary, parameters)
	if err != nil {
		return BenchmarkResult{}, err
	}
	groups, err := buildGroups(samples, cpuCalibrations, parameters.Count)
	if err != nil {
		return BenchmarkResult{}, err
	}
	result := BenchmarkResult{
		FormatVersion: resultFormat, SuiteVersion: suiteVersion,
		Environment: environment, Parameters: parameters, Groups: groups,
	}
	if profiles != nil {
		if err := runProfiles(root, goBinary, parameters, environment, *profiles); err != nil {
			return BenchmarkResult{}, err
		}
	}
	return result, validateResult(result)
}

func runSuite(root, goBinary string, parameters Parameters) (map[string][]MetricSample, map[string][]MetricSample, error) {
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
	cpuCalibrations := make(map[string][]MetricSample, len(packages))
	for _, packageName := range packages {
		names := uniqueSorted(byPackage[packageName])
		parsed, cpuSamples, err := runPackageBenchmarks(root, goBinary, packageName, names, parameters)
		if err != nil {
			return nil, nil, err
		}
		cpuCalibrations[packageName] = cpuSamples
		for name, values := range parsed {
			if _, exists := all[name]; exists {
				return nil, nil, fmt.Errorf("benchmark %s was emitted by multiple packages", name)
			}
			all[name] = values
		}
	}
	return all, cpuCalibrations, nil
}

func runPackageBenchmarks(root, goBinary, packageName string, names []string, parameters Parameters) (map[string][]MetricSample, []MetricSample, error) {
	patterns := benchmarkPatterns(names)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	all := make(map[string][]MetricSample, len(names))
	cpuSamples := make([]MetricSample, 0, parameters.Count)
	for sampleIndex := 0; sampleIndex < parameters.Count; sampleIndex++ {
		before, err := runCPUSentinelSample(ctx, goBinary, parameters)
		if err != nil {
			return nil, nil, fmt.Errorf("CPU sentinel before package %s sample %d: %w", packageName, sampleIndex+1, err)
		}
		sample := make(map[string][]MetricSample, len(names))
		for _, pattern := range patterns {
			parsed, err := runBenchmarkSample(ctx, root, goBinary, packageName, pattern, parameters, sampleIndex)
			if err != nil {
				return nil, nil, err
			}
			for name, values := range parsed {
				if _, exists := sample[name]; exists {
					return nil, nil, fmt.Errorf("benchmark package %s sample %d emitted duplicate %s", packageName, sampleIndex+1, name)
				}
				sample[name] = values
			}
		}
		after, err := runCPUSentinelSample(ctx, goBinary, parameters)
		if err != nil {
			return nil, nil, fmt.Errorf("CPU sentinel after package %s sample %d: %w", packageName, sampleIndex+1, err)
		}
		bracketed, err := collapseMetricSamples([]MetricSample{before, after})
		if err != nil {
			return nil, nil, fmt.Errorf("collapse CPU sentinel for package %s sample %d: %w", packageName, sampleIndex+1, err)
		}
		cpuSamples = append(cpuSamples, bracketed)
		if len(sample) != len(names) {
			return nil, nil, fmt.Errorf("benchmark package %s sample %d emitted %v, want %v", packageName, sampleIndex+1, sortedBenchmarkNames(sample), names)
		}
		for _, name := range names {
			if len(sample[name]) != 1 {
				return nil, nil, fmt.Errorf("benchmark %s sample %d emitted %d measurements, want 1", name, sampleIndex+1, len(sample[name]))
			}
			all[name] = append(all[name], sample[name][0])
		}
	}
	return all, cpuSamples, nil
}

const cpuSentinelSource = `package sentinel

import "testing"

var cpuSentinelSink uint64

func BenchmarkReconcCPUSentinel(b *testing.B) {
	var value uint64
	for index := 0; index < b.N; index++ {
		for round := 0; round < 32; round++ {
			value = value*6364136223846793005 + 1442695040888963407 + uint64(round)
			value ^= value >> 33
		}
	}
	cpuSentinelSink = value
}
`

func runCPUSentinelSample(ctx context.Context, goBinary string, parameters Parameters) (sample MetricSample, err error) {
	directory, err := os.MkdirTemp("", "reconc-benchmark-cpu-sentinel-")
	if err != nil {
		return MetricSample{}, fmt.Errorf("create CPU sentinel directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); err == nil && cleanupErr != nil {
			err = fmt.Errorf("remove CPU sentinel directory: %w", cleanupErr)
		}
	}()
	path := filepath.Join(directory, "sentinel_test.go")
	if err := os.WriteFile(path, []byte(cpuSentinelSource), 0o600); err != nil {
		return MetricSample{}, fmt.Errorf("write CPU sentinel source: %w", err)
	}
	args := []string{"test", "-json", "-run", "^$", "-bench", "^" + regexp.QuoteMeta(cpuSentinelName) + "$", "-benchmem", "-count", strconv.Itoa(parameters.Repetitions), "-benchtime", parameters.Benchtime, "-cpu", strconv.Itoa(parameters.CPU), "-timeout", "5m"}
	command := exec.CommandContext(ctx, goBinary, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GO111MODULE=off")
	output, err := boundedexec.Output(command, maxBenchmarkOutput)
	if ctx.Err() != nil {
		return MetricSample{}, fmt.Errorf("CPU sentinel timed out: %w", ctx.Err())
	}
	if err != nil {
		return MetricSample{}, fmt.Errorf("CPU sentinel failed: %w", err)
	}
	parsed, err := parseBenchmarkJSON(output)
	if err != nil {
		return MetricSample{}, fmt.Errorf("parse CPU sentinel: %w", err)
	}
	values := parsed[cpuSentinelName]
	if len(values) != parameters.Repetitions || len(parsed) != 1 {
		return MetricSample{}, fmt.Errorf("CPU sentinel emitted %d %s measurements, want %d", len(values), cpuSentinelName, parameters.Repetitions)
	}
	peakRSSBytes := processPeakRSSBytes(command.ProcessState)
	for index := range values {
		values[index].PeakRSSBytes = peakRSSBytes
	}
	return collapseMetricSamples(values)
}

func collapseMetricSamples(samples []MetricSample) (MetricSample, error) {
	if len(samples) == 0 {
		return MetricSample{}, errors.New("cannot collapse an empty benchmark sample set")
	}
	iterations := make([]uint64, len(samples))
	var peakRSSBytes uint64
	for index, sample := range samples {
		if sample.Iterations == 0 || !validMetricValues(sample.MetricValues) {
			return MetricSample{}, errors.New("benchmark sample is invalid")
		}
		iterations[index] = sample.Iterations
		peakRSSBytes = maxUint64(peakRSSBytes, sample.PeakRSSBytes)
	}
	sort.Slice(iterations, func(left, right int) bool { return iterations[left] < iterations[right] })
	return MetricSample{
		Iterations:   iterations[len(iterations)/2],
		PeakRSSBytes: peakRSSBytes,
		MetricValues: medianMetrics(samples),
	}, nil
}

func maxUint64(first, second uint64) uint64 {
	if first > second {
		return first
	}
	return second
}

func benchmarkPatterns(names []string) []string {
	plain := make([]string, 0, len(names))
	grouped := make(map[string][]string)
	for _, name := range names {
		root, sub, hasSub := strings.Cut(name, "/")
		if !hasSub {
			plain = append(plain, regexp.QuoteMeta(name))
			continue
		}
		grouped[root] = append(grouped[root], regexp.QuoteMeta(sub))
	}
	patterns := make([]string, 0, len(grouped)+1)
	if len(plain) > 0 {
		patterns = append(patterns, "^("+strings.Join(plain, "|")+")$")
	}
	roots := make([]string, 0, len(grouped))
	for root := range grouped {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		patterns = append(patterns, "^"+regexp.QuoteMeta(root)+"$/^("+strings.Join(grouped[root], "|")+")$")
	}
	return patterns
}

func runBenchmarkSample(ctx context.Context, root, goBinary, packageName, pattern string, parameters Parameters, sampleIndex int) (map[string][]MetricSample, error) {
	args := []string{"test", "-json", "-run", "^$", "-bench", pattern, "-benchmem", "-count", strconv.Itoa(parameters.Repetitions), "-benchtime", parameters.Benchtime, "-cpu", strconv.Itoa(parameters.CPU), "-timeout", "5m", packageName}
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
	peakRSSBytes := processPeakRSSBytes(command.ProcessState)
	for name := range parsed {
		for index := range parsed[name] {
			parsed[name][index].PeakRSSBytes = peakRSSBytes
		}
		if len(parsed[name]) != parameters.Repetitions {
			return nil, fmt.Errorf("benchmark package %s sample %d benchmark %s emitted %d measurements, want %d", packageName, sampleIndex+1, name, len(parsed[name]), parameters.Repetitions)
		}
		collapsed, collapseErr := collapseMetricSamples(parsed[name])
		if collapseErr != nil {
			return nil, fmt.Errorf("collapse benchmark package %s sample %d benchmark %s: %w", packageName, sampleIndex+1, name, collapseErr)
		}
		parsed[name] = []MetricSample{collapsed}
	}
	return parsed, nil
}

func processPeakRSSBytes(state *os.ProcessState) uint64 {
	if state == nil {
		return 0
	}
	usage := state.SysUsage()
	value := reflect.ValueOf(usage)
	if !value.IsValid() {
		return 0
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0
	}
	field := value.FieldByName("Maxrss")
	if !field.IsValid() || !field.CanInt() || field.Int() <= 0 {
		return 0
	}
	bytes := uint64(field.Int())
	if runtime.GOOS == "linux" {
		bytes *= 1024
	}
	return bytes
}

func sortedBenchmarkNames(samples map[string][]MetricSample) []string {
	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildGroups(samples map[string][]MetricSample, cpuCalibrations map[string][]MetricSample, count int) ([]GroupResult, error) {
	groups := make([]GroupResult, 0, len(benchmarkSuite))
	for _, spec := range benchmarkSuite {
		calibration, err := statsFor(spec.Calibration, samples[spec.Calibration], count)
		if err != nil {
			return nil, err
		}
		cpuCalibration, err := statsFor(cpuSentinelName, cpuCalibrations[spec.Package], count)
		if err != nil {
			return nil, fmt.Errorf("CPU calibration for %s: %w", spec.Package, err)
		}
		group := GroupResult{Name: spec.Name, Package: spec.Package, Calibration: calibration, CPUCalibration: cpuCalibration}
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
	stats := BenchmarkStats{
		Name: name, Samples: append([]MetricSample(nil), samples...),
		Median: medianMetrics(samples), P50: percentileMetrics(samples, 0.50), P95: percentileMetrics(samples, 0.95),
	}
	for _, sample := range samples {
		if sample.PeakRSSBytes > stats.PeakRSSBytes {
			stats.PeakRSSBytes = sample.PeakRSSBytes
		}
	}
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
