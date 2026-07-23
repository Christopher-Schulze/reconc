package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type hostContractFixture struct {
	Host           string                       `json:"host"`
	HostVersion    string                       `json:"host_version"`
	SourceURL      string                       `json:"source_url"`
	SourceRevision string                       `json:"source_revision"`
	CaptureDate    string                       `json:"capture_date"`
	Events         map[string]hostContractEvent `json:"events"`
}

type hostContractEvent struct {
	Payload map[string]interface{} `json:"payload"`
	Result  interface{}            `json:"result"`
}

func TestOfficialHostContractFixturesCoverEveryInstalledBinding(t *testing.T) {
	for _, kind := range []string{KindCursor, KindOpenCode, KindKilo} {
		fixture := readHostContractFixture(t, kind)
		if fixture.Host != kind || fixture.HostVersion == "" || fixture.SourceURL == "" ||
			len(fixture.SourceRevision) != 40 || fixture.CaptureDate == "" {
			t.Fatalf("%s fixture metadata is incomplete: %#v", kind, fixture)
		}
		want := installedNativeEvents(t, kind)
		got := make([]string, 0, len(fixture.Events))
		for event := range fixture.Events {
			got = append(got, event)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s fixture events = %v, want %v", kind, got, want)
		}
	}
}

func TestOfficialHostContractFixturesPreserveSecurityRelevantStates(t *testing.T) {
	cursor := readHostContractFixture(t, KindCursor)
	failure := cursor.Events["postToolUseFailure"].Payload
	if failure["error_message"] == nil || failure["tool_use_id"] == nil {
		t.Fatalf("Cursor failure fixture lacks authoritative failure identity: %#v", failure)
	}
	if _, fabricated := cursor.Events["afterShellExecution"].Payload["exit_code"]; fabricated {
		t.Fatal("Cursor passive afterShellExecution fixture fabricates an exit status")
	}
	mcpResult := cursor.Events["afterMCPExecution"].Payload["tool_response"]
	if mcpResult == nil {
		t.Fatal("Cursor MCP fixture has no result contract")
	}

	for _, kind := range []string{KindOpenCode, KindKilo} {
		fixture := readHostContractFixture(t, kind)
		after := fixture.Events["tool.execute.after"].Result.(map[string]interface{})
		metadata := after["metadata"].(map[string]interface{})
		if metadata["exit"] != float64(0) {
			t.Fatalf("%s shell fixture lacks authoritative metadata.exit: %#v", kind, after)
		}
		idle := fixture.Events["session.idle"].Result.(map[string]interface{})
		request := idle["request"].(map[string]interface{})
		if request["sessionID"] != "ses_fixture" {
			t.Fatalf("%s promptAsync request fixture = %#v", kind, request)
		}
		if request["messageID"] != "msg_reconc_00000000000000000000000000000000" {
			t.Fatalf("%s promptAsync request lacks injected-message identity: %#v", kind, request)
		}
		parts := request["parts"].([]interface{})
		if len(parts) != 1 || parts[0].(map[string]interface{})["type"] != "text" {
			t.Fatalf("%s promptAsync parts fixture = %#v", kind, parts)
		}
		if _, nested := request["path"]; nested {
			t.Fatalf("%s promptAsync fixture uses obsolete path/body request shape: %#v", kind, request)
		}
		response := idle["response"].(map[string]interface{})
		if response["status"] != float64(204) || response["ok"] != true {
			t.Fatalf("%s promptAsync acceptance fixture = %#v", kind, idle)
		}
	}
}

func TestNegativeContractFixtureEnumeratesBoundedFailureClasses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "contracts", "negative.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID           string `json:"id"`
			PayloadBytes int    `json:"payload_bytes"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fixture.Cases))
	for _, item := range fixture.Cases {
		got = append(got, item.ID)
	}
	want := []string{
		"missing-session-identity",
		"malformed-field-type",
		"unknown-field",
		"oversized-payload",
		"invalid-utf8",
		"runtime-timeout",
		"duplicate-call-identity",
		"truncated-adapter-output",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("negative contract classes = %v, want %v", got, want)
	}
	for _, item := range fixture.Cases {
		if item.ID == "oversized-payload" && item.PayloadBytes != (64<<20)+1 {
			t.Fatalf("oversized payload fixture = %d, want 64 MiB + 1", item.PayloadBytes)
		}
	}
}

func readHostContractFixture(t *testing.T, kind string) hostContractFixture {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "contracts", kind+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture hostContractFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func installedNativeEvents(t *testing.T, kind string) []string {
	t.Helper()
	platform, ok := PlatformForKind(kind)
	if !ok {
		t.Fatalf("platform %s missing", kind)
	}
	events := map[string]bool{}
	for _, capability := range platform.Capabilities {
		if capability.Support == SupportUnsupported || capability.Fallback != "" {
			continue
		}
		for _, binding := range capability.Bindings {
			if binding.RuntimeEvent != "" {
				events[binding.NativeEvent] = true
			}
		}
	}
	out := make([]string, 0, len(events))
	for event := range events {
		out = append(out, event)
	}
	sort.Strings(out)
	return out
}
