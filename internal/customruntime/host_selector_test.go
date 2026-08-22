package customruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestStreamingHostSelectionMatchesInterfaceTreeReference(t *testing.T) {
	for _, fixture := range []string{"local-agent", "ci-bot"} {
		manifest := mustManifest(t, "testdata/"+fixture+".json")
		body, err := os.ReadFile("testdata/" + fixture + "-conformance.json")
		if err != nil {
			t.Fatal(err)
		}
		suite, err := DecodeConformanceSuite(body)
		if err != nil {
			t.Fatal(err)
		}
		for _, testCase := range suite.Cases {
			route, ok := manifest.Route(testCase.HostEvent)
			if !ok {
				t.Fatalf("fixture %s has no route %s", fixture, testCase.HostEvent)
			}
			got, gotJSON, err := NormalizeHostPayload(manifest, route, testCase.HostPayload)
			if err != nil {
				t.Fatalf("stream %s/%s: %v", fixture, testCase.Name, err)
			}
			want, wantJSON, err := normalizeHostPayloadInterfaceTreeReference(manifest, route, testCase.HostPayload)
			if err != nil {
				t.Fatalf("reference %s/%s: %v", fixture, testCase.Name, err)
			}
			if !reflect.DeepEqual(got, want) || !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("stream/reference mismatch for %s/%s:\n got=%+v %s\nwant=%+v %s", fixture, testCase.Name, got, gotJSON, want, wantJSON)
			}
		}
	}
}

func TestStreamingHostSelectionResolvesOverlappingPointersOnce(t *testing.T) {
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	route.Fields.ToolInput = "/tool"
	route.Fields.ToolName = "/tool/name"
	body := []byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{"path":"README.md"}},"unknown":{"large":[1,2,3]}}`)
	request, encoded, err := NormalizeHostPayload(manifest, route, body)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := request.Payload["tool_input"].(map[string]interface{})
	if !ok || tool["name"] != "Read" || request.Payload["tool_name"] != "Read" {
		t.Fatalf("overlapping selection = %+v", request.Payload)
	}
	const want = `{"reconc_runtime":"custom:local-agent","session_id":"s","tool_input":{"input":{"path":"README.md"},"name":"Read"},"tool_name":"Read"}`
	if string(encoded) != want {
		t.Fatalf("canonical selected bytes = %s, want %s", encoded, want)
	}
}

func TestSelectedHostFieldBudgetTracksCompleteMappingContract(t *testing.T) {
	if count := len(fieldMappingPointers(FieldMappings{})); count != maxSelectedHostFields {
		t.Fatalf("mapping contract has %d fields, selected-field budget covers %d", count, maxSelectedHostFields)
	}
}

func TestStreamingHostValidationBudgetsAndSyntax(t *testing.T) {
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	deep := `{"context":{"session":"s"},"tool":{"name":"Read","input":{}},"deep":` + strings.Repeat(`{"x":`, maxHostJSONDepth) + `null` + strings.Repeat(`}`, maxHostJSONDepth) + `}`
	invalidUTF8 := append([]byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}},"bad":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{name: "duplicate", body: []byte(`{"context":{"session":"a","session":"b"},"tool":{"name":"Read","input":{}}}`), want: "duplicate key"},
		{name: "invalid UTF-8", body: invalidUTF8, want: "invalid UTF-8"},
		{name: "invalid number", body: []byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}},"n":01}`), want: "invalid character"},
		{name: "trailing", body: []byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}}}{}`), want: "exactly one JSON value"},
		{name: "truncated", body: []byte(`{"context":{"session":"s"}`), want: "unexpected EOF"},
		{name: "depth", body: []byte(deep), want: "exceeds 32 levels"},
		{name: "object members", body: oversizedObjectMemberPayload(), want: "object members"},
		{name: "array items", body: oversizedArrayItemPayload(), want: "array items"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := NormalizeHostPayload(manifest, route, test.body); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStreamingHostSelectionBoundsRetainedAndTotalBytes(t *testing.T) {
	manifest := mustManifest(t, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	selected := []byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{"blob":"` + strings.Repeat("x", maxRetainedHostPayloadBytes) + `"}}}`)
	if _, _, err := NormalizeHostPayload(manifest, route, selected); err == nil || !strings.Contains(err.Error(), "selected host payload exceeds") {
		t.Fatalf("oversized selected payload error = %v", err)
	}

	maximum := benchmarkHostPayload(maxHostPayloadBytes)
	request, _, err := NormalizeHostPayload(manifest, route, maximum)
	if err != nil || request.Payload["session_id"] != "s" {
		t.Fatalf("maximum bounded payload = %+v, %v", request.Payload, err)
	}
	if _, _, err := NormalizeHostPayload(manifest, route, append(maximum, ' ')); err == nil || !strings.Contains(err.Error(), "1..") {
		t.Fatalf("payload above byte bound error = %v", err)
	}
}

func FuzzStreamingHostSelectionParity(f *testing.F) {
	manifest := mustManifest(f, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	for _, seed := range [][]byte{
		[]byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}}}`),
		[]byte(`{"context":{"session":"s"},"tool":{"name":"Read","input":{"items":[1,2,3]}},"unknown":true}`),
		[]byte(`{"context":{"session":"s"},"context":{"session":"duplicate"}}`),
		[]byte(`{"context":{"session":"s"}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		got, gotJSON, gotErr := NormalizeHostPayload(manifest, route, body)
		want, wantJSON, wantErr := normalizeHostPayloadInterfaceTreeReference(manifest, route, body)
		if gotErr != nil || wantErr != nil {
			return
		}
		if !reflect.DeepEqual(got, want) || !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("valid reference mismatch: got=%+v %q err=%v want=%+v %q", got, gotJSON, gotErr, want, wantJSON)
		}
	})
}

func BenchmarkNormalizeHostPayload(b *testing.B) {
	manifest := mustManifest(b, "testdata/local-agent.json")
	route, _ := manifest.Route("before-tool")
	for _, size := range []struct {
		name  string
		bytes int
	}{
		{name: "small", bytes: 256},
		{name: "typical", bytes: 64 << 10},
		{name: "maximum", bytes: maxHostPayloadBytes},
	} {
		body := benchmarkHostPayload(size.bytes)
		b.Run(size.name+"/stream", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, _, err := NormalizeHostPayload(manifest, route, body); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(size.name+"/interface-tree-reference", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, _, err := normalizeHostPayloadInterfaceTreeReference(manifest, route, body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func normalizeHostPayloadInterfaceTreeReference(manifest Manifest, route Route, body []byte) (NeutralRequest, []byte, error) {
	if len(body) == 0 || len(body) > maxHostPayloadBytes {
		return NeutralRequest{}, nil, fmt.Errorf("custom host payload must be 1..%d bytes", maxHostPayloadBytes)
	}
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return NeutralRequest{}, nil, err
	}
	var host map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&host); err != nil {
		return NeutralRequest{}, nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || host == nil {
		return NeutralRequest{}, nil, fmt.Errorf("reference input is not one object")
	}
	selected := selectedHostValues{}
	for _, pointer := range fieldMappingPointers(route.Fields) {
		if pointer == "" {
			continue
		}
		value, ok, err := selectPointerReference(host, pointer)
		if err != nil {
			return NeutralRequest{}, nil, err
		}
		if ok {
			selected[pointer] = value
		}
	}
	neutral, err := buildNeutralPayload(manifest, route, selected)
	if err != nil {
		return NeutralRequest{}, nil, err
	}
	request := NeutralRequest{
		Schema: schema.Resolve(schema.NeutralHookRequest), FormatVersion: RequestFormatVersion,
		Runtime: manifest.Runtime(), HostEvent: route.HostEvent, Event: route.Event, Payload: neutral,
	}
	encoded, err := json.Marshal(neutral)
	return request, encoded, err
}

func selectPointerReference(root interface{}, pointer string) (interface{}, bool, error) {
	if pointer == "" {
		return root, true, nil
	}
	if !validJSONPointer(pointer) {
		return nil, false, fmt.Errorf("invalid JSON Pointer %q", pointer)
	}
	current := root
	for _, raw := range strings.Split(pointer[1:], "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch value := current.(type) {
		case map[string]interface{}:
			selected, exists := value[segment]
			if !exists {
				return nil, false, nil
			}
			current = selected
		case []interface{}:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(value) || strconv.Itoa(index) != segment {
				return nil, false, nil
			}
			current = value[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func oversizedObjectMemberPayload() []byte {
	var builder strings.Builder
	builder.WriteString(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}}`)
	for index := 0; index <= maxHostObjectMembers; index++ {
		builder.WriteString(`,"k`)
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(`":null`)
	}
	builder.WriteByte('}')
	return []byte(builder.String())
}

func oversizedArrayItemPayload() []byte {
	var builder strings.Builder
	builder.WriteString(`{"context":{"session":"s"},"tool":{"name":"Read","input":{}},"items":[`)
	for index := 0; index <= maxHostArrayItems; index++ {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('0')
	}
	builder.WriteString(`]}`)
	return []byte(builder.String())
}

func benchmarkHostPayload(size int) []byte {
	prefix := `{"context":{"session":"s"},"tool":{"name":"Read","input":{}},"padding":"`
	suffix := `"}`
	padding := size - len(prefix) - len(suffix)
	if padding < 0 {
		padding = 0
	}
	return []byte(prefix + strings.Repeat("x", padding) + suffix)
}
