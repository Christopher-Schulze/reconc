package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

const maxBenchmarkEvents = 100000

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Output  string `json:"Output"`
}

func parseBenchmarkJSON(body []byte) (map[string][]MetricSample, error) {
	if len(body) == 0 || len(body) > maxBenchmarkOutput {
		return nil, fmt.Errorf("benchmark output size is outside 1..%d bytes", maxBenchmarkOutput)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	parsed := make(map[string][]MetricSample)
	for events := 0; ; events++ {
		if events >= maxBenchmarkEvents {
			return nil, fmt.Errorf("benchmark output exceeds %d events", maxBenchmarkEvents)
		}
		var event testEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go test event: %w", err)
		}
		if event.Action != "output" {
			continue
		}
		for _, line := range strings.Split(event.Output, "\n") {
			name, sample, ok, err := parseBenchmarkLine(line)
			if err != nil {
				return nil, err
			}
			if ok {
				parsed[name] = append(parsed[name], sample)
			}
		}
	}
	if len(parsed) == 0 {
		return nil, errors.New("go test output contains no benchmark metrics")
	}
	return parsed, nil
}

func parseBenchmarkLine(line string) (string, MetricSample, bool, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "Benchmark") {
		return "", MetricSample{}, false, nil
	}
	// Go emits a name-only warm-up line before the measured benchmark line.
	if len(fields) == 1 {
		return "", MetricSample{}, false, nil
	}
	if len(fields) < 8 || len(fields)%2 != 0 {
		return "", MetricSample{}, false, fmt.Errorf("malformed benchmark line %q", line)
	}
	iterations, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || iterations == 0 {
		return "", MetricSample{}, false, fmt.Errorf("invalid benchmark iteration count in %q", line)
	}
	sample := MetricSample{Iterations: iterations}
	seen := map[string]bool{}
	for index := 2; index+1 < len(fields); index += 2 {
		value, parseErr := strconv.ParseFloat(fields[index], 64)
		unit := fields[index+1]
		if parseErr != nil || !finite(value) || value < 0 {
			return "", MetricSample{}, false, fmt.Errorf("invalid benchmark metric in %q", line)
		}
		switch unit {
		case "ns/op":
			sample.NSPerOp, seen[unit] = value, true
		case "B/op":
			sample.BytesPerOp, seen[unit] = value, true
		case "allocs/op":
			sample.AllocsPerOp, seen[unit] = value, true
		}
	}
	for _, unit := range []string{"ns/op", "B/op", "allocs/op"} {
		if !seen[unit] {
			return "", MetricSample{}, false, fmt.Errorf("benchmark line omits %s: %q", unit, line)
		}
	}
	if sample.NSPerOp <= 0 {
		return "", MetricSample{}, false, fmt.Errorf("benchmark line has non-positive ns/op: %q", line)
	}
	return stripCPUSuffix(fields[0]), sample, true, nil
}

func stripCPUSuffix(name string) string {
	index := strings.LastIndexByte(name, '-')
	if index < 0 || index == len(name)-1 {
		return name
	}
	for _, character := range name[index+1:] {
		if character < '0' || character > '9' {
			return name
		}
	}
	return name[:index]
}

func medianMetrics(samples []MetricSample) MetricValues {
	ns := make([]float64, len(samples))
	bytesPerOp := make([]float64, len(samples))
	allocs := make([]float64, len(samples))
	for index, sample := range samples {
		ns[index] = sample.NSPerOp
		bytesPerOp[index] = sample.BytesPerOp
		allocs[index] = sample.AllocsPerOp
	}
	return MetricValues{NSPerOp: median(ns), BytesPerOp: median(bytesPerOp), AllocsPerOp: median(allocs)}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	values = append([]float64(nil), values...)
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}
