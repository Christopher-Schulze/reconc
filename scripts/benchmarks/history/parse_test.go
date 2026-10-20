package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	var output strings.Builder
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
		if output.Len() > maxBenchmarkOutput-len(event.Output) {
			return nil, fmt.Errorf("benchmark output exceeds %d decoded bytes", maxBenchmarkOutput)
		}
		output.WriteString(event.Output)
	}
	return parseBenchmarkText(output.String())
}
