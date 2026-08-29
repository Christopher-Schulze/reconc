// Package customruntime defines the non-executable declarative bridge for
// repository-owned third-party agent and automation adapters.
package customruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/schema"
)

const (
	ManifestSchemaURL        = schema.CustomRuntimeManifestURL
	NeutralRequestSchemaURL  = schema.NeutralHookRequestURL
	NeutralResponseSchemaURL = schema.NeutralHookResponseURL
	LivenessSchemaURL        = schema.CustomRuntimeLivenessURL
	ManifestFormatVersion    = "reconc-custom-runtime/v2"
	LegacyManifestFormatV1   = "reconc-custom-runtime/v1"
	RequestFormatVersion     = "reconc-neutral-hook-request/v1"
	ResponseFormatVersion    = "reconc-neutral-hook-response/v1"
	LivenessFormatVersion    = "reconc-custom-runtime-liveness/v1"
	MaxManifestBytes         = 256 << 10
	MaxRoutes                = 32
	maxNameBytes             = 48
	maxDisplayNameBytes      = 120
	maxPointerBytes          = 256
)

type Event string

const (
	EventSessionStart       Event = "session-start"
	EventUserPromptSubmit   Event = "user-prompt-submit"
	EventPreToolUse         Event = "pre-tool-use"
	EventPermissionRequest  Event = "permission-request"
	EventPermissionResult   Event = "permission-result"
	EventPostToolUse        Event = "post-tool-use"
	EventPostToolUseFailure Event = "post-tool-use-failure"
	EventMCPBefore          Event = "mcp-before"
	EventMCPAfter           Event = "mcp-after"
	EventStop               Event = "stop"
	EventInterrupt          Event = "interrupt"
	EventSessionEnd         Event = "session-end"
	EventNotification       Event = "notification"
	EventSubagentStart      Event = "subagent-start"
	EventSubagentStop       Event = "subagent-stop"
	EventPreCompaction      Event = "pre-compaction"
	EventPostCompaction     Event = "post-compaction"
	EventWorkspaceOpen      Event = "workspace-open"
)

type Support string

const (
	SupportNative      Support = "native"
	SupportAdapted     Support = "adapted"
	SupportInferred    Support = "inferred"
	SupportUnsupported Support = "unsupported"
)

type FailurePolicy string

const (
	FailureBlock FailurePolicy = "block"
	FailureAllow FailurePolicy = "allow"
	FailureHost  FailurePolicy = "host"
)

type ResponseMode string

const (
	ResponseDecision         ResponseMode = "decision"
	ResponseObservation      ResponseMode = "observation"
	ResponseStopContinuation ResponseMode = "stop-continuation"
)

// HostGuarantees is a declaration that conformance and status can check. A
// missing guarantee never silently becomes an enforcement claim.
type HostGuarantees struct {
	PreExecution         bool `json:"pre_execution"`
	SynchronousResponse  bool `json:"synchronous_response"`
	AuthoritativeOutcome bool `json:"authoritative_outcome"`
	Continuation         bool `json:"continuation"`
	ContinuationAck      bool `json:"continuation_ack"`
	MCPIdentity          bool `json:"mcp_identity"`
}

// FieldMappings selects only the host fields needed by the neutral runtime.
// Every value is an exact RFC 6901 JSON Pointer.
type FieldMappings struct {
	SessionID            string `json:"session_id"`
	ToolName             string `json:"tool_name,omitempty"`
	ToolInput            string `json:"tool_input,omitempty"`
	ToolResponse         string `json:"tool_response,omitempty"`
	ToolUseID            string `json:"tool_use_id,omitempty"`
	Error                string `json:"error,omitempty"`
	IsInterrupt          string `json:"is_interrupt,omitempty"`
	StopHookActive       string `json:"stop_hook_active,omitempty"`
	StrictContinuation   string `json:"strict_continuation,omitempty"`
	ExitCode             string `json:"exit_code,omitempty"`
	MCPTool              string `json:"mcp_tool,omitempty"`
	MCPServerFingerprint string `json:"mcp_server_fingerprint,omitempty"`
	MCPOutcome           string `json:"mcp_outcome,omitempty"`
}

type Route struct {
	HostEvent        string         `json:"host_event"`
	Event            Event          `json:"event"`
	Support          Support        `json:"support"`
	Response         ResponseMode   `json:"response"`
	ErrorPolicy      FailurePolicy  `json:"error_policy"`
	TimeoutPolicy    FailurePolicy  `json:"timeout_policy"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	MaxOutputBytes   int            `json:"max_output_bytes"`
	MaxContinuations int            `json:"max_continuations,omitempty"`
	Guarantees       HostGuarantees `json:"guarantees"`
	Fields           FieldMappings  `json:"fields"`
}

type Manifest struct {
	Schema        string  `json:"$schema"`
	FormatVersion string  `json:"format_version"`
	Name          string  `json:"name"`
	DisplayName   string  `json:"display_name"`
	Routes        []Route `json:"routes"`
}

type Summary struct {
	Name           string   `json:"name"`
	Runtime        string   `json:"runtime"`
	ManifestDigest string   `json:"manifest_digest"`
	RouteCount     int      `json:"route_count"`
	DegradedRoutes []string `json:"degraded_routes"`
}

func (manifest Manifest) Runtime() string { return "custom:" + manifest.Name }

func (manifest Manifest) LivenessRuntime() string { return "custom-" + manifest.Name }

func LivenessEvent(hostEvent string) string {
	normalized := strings.Builder{}
	for _, character := range strings.ToLower(hostEvent) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			normalized.WriteRune(character)
		} else {
			normalized.WriteByte('-')
		}
	}
	value := strings.Trim(normalized.String(), "-")
	if value == "" {
		value = "event"
	}
	if len(value) > 47 {
		value = strings.TrimRight(value[:47], "-")
	}
	digest := sha256.Sum256([]byte(hostEvent))
	return value + "-" + hex.EncodeToString(digest[:8])
}

func (manifest Manifest) Validate() error { return validateManifest(manifest) }

func (manifest Manifest) Route(hostEvent string) (Route, bool) {
	index := sort.Search(len(manifest.Routes), func(index int) bool {
		return manifest.Routes[index].HostEvent >= hostEvent
	})
	if index < len(manifest.Routes) && manifest.Routes[index].HostEvent == hostEvent {
		return manifest.Routes[index], true
	}
	return Route{}, false
}

func (manifest Manifest) Digest() (string, error) {
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode custom runtime manifest identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (manifest Manifest) Summary() (Summary, error) {
	degraded := []string{}
	for _, route := range manifest.Routes {
		if len(route.DegradedReasons()) > 0 {
			degraded = append(degraded, route.HostEvent)
		}
	}
	digest, err := manifest.Digest()
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		Name: manifest.Name, Runtime: manifest.Runtime(), ManifestDigest: digest,
		RouteCount: len(manifest.Routes), DegradedRoutes: degraded,
	}, nil
}

func ValidateSummary(summary Summary) error {
	if !validName(summary.Name, maxNameBytes) || summary.Runtime != "custom:"+summary.Name {
		return fmt.Errorf("custom runtime summary identity is invalid")
	}
	if _, reserved := reservedRuntimeNames[summary.Name]; reserved || strings.HasPrefix(summary.Name, "custom-") {
		return fmt.Errorf("custom runtime summary uses a reserved identity")
	}
	if !validSHA256Digest(summary.ManifestDigest) || summary.RouteCount < 1 || summary.RouteCount > MaxRoutes || len(summary.DegradedRoutes) > summary.RouteCount {
		return fmt.Errorf("custom runtime summary digest or route counts are invalid")
	}
	for index, route := range summary.DegradedRoutes {
		if !validHostEvent(route) || index > 0 && summary.DegradedRoutes[index-1] >= route {
			return fmt.Errorf("custom runtime degraded routes must be unique and sorted")
		}
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func (route Route) DegradedReasons() []string {
	reasons := []string{}
	if route.Support == SupportUnsupported {
		return []string{"host declares this route unsupported"}
	}
	if routeNeedsPreExecution(route.Event) && !route.Guarantees.PreExecution {
		reasons = append(reasons, "pre-execution delivery is not guaranteed")
	}
	if routeNeedsSynchronousResponse(route.Event) && !route.Guarantees.SynchronousResponse {
		reasons = append(reasons, "synchronous response enforcement is not guaranteed")
	}
	if (route.Event == EventPostToolUse || route.Event == EventMCPAfter) && !route.Guarantees.AuthoritativeOutcome {
		reasons = append(reasons, "authoritative outcome is not guaranteed")
	}
	if route.Event == EventStop && !route.Guarantees.Continuation {
		reasons = append(reasons, "Stop continuation is not guaranteed")
	}
	if route.Event == EventStop && route.MaxContinuations > 0 && !route.Guarantees.ContinuationAck {
		reasons = append(reasons, "Stop continuation acknowledgement is not guaranteed")
	}
	if (route.Event == EventMCPBefore || route.Event == EventMCPAfter) && !route.Guarantees.MCPIdentity {
		reasons = append(reasons, "exact MCP identity is not guaranteed")
	}
	return reasons
}

func (route Route) Enforcing() bool {
	return len(route.DegradedReasons()) == 0 && route.Response != ResponseObservation
}

func routeNeedsPreExecution(event Event) bool {
	return event == EventPreToolUse || event == EventPermissionRequest || event == EventMCPBefore
}

func routeNeedsSynchronousResponse(event Event) bool {
	return routeNeedsPreExecution(event) || event == EventStop
}

func validEvent(event Event) bool {
	switch event {
	case EventSessionStart, EventUserPromptSubmit, EventPreToolUse, EventPermissionRequest,
		EventPermissionResult, EventPostToolUse, EventPostToolUseFailure, EventMCPBefore,
		EventMCPAfter, EventStop, EventInterrupt, EventSessionEnd, EventNotification,
		EventSubagentStart, EventSubagentStop, EventPreCompaction, EventPostCompaction,
		EventWorkspaceOpen:
		return true
	default:
		return false
	}
}

func validName(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}

var reservedRuntimeNames = map[string]struct{}{
	"git-pre-commit": {}, "claude-code": {}, "codex": {}, "github-copilot": {},
	"cursor": {}, "opencode": {}, "devin-cli": {}, "antigravity": {}, "kilo": {},
	"grok": {}, "omp": {}, "pi": {}, "zcode": {}, "kimi-code": {},
}

func validateManifest(manifest Manifest) error {
	if !schema.Accepts(schema.CustomRuntimeManifest, manifest.Schema) {
		return fmt.Errorf("custom runtime $schema must be %q", ManifestSchemaURL)
	}
	if !schema.AcceptsFormat(schema.CustomRuntimeManifest, manifest.Schema, manifest.FormatVersion) {
		return fmt.Errorf("custom runtime format_version must match its registered schema contract")
	}
	if !validName(manifest.Name, maxNameBytes) {
		return fmt.Errorf("custom runtime name must be a lowercase identifier of at most %d bytes", maxNameBytes)
	}
	if _, reserved := reservedRuntimeNames[manifest.Name]; reserved || strings.HasPrefix(manifest.Name, "custom-") {
		return fmt.Errorf("custom runtime name %q is reserved and cannot impersonate a built-in runtime", manifest.Name)
	}
	if manifest.DisplayName == "" || len(manifest.DisplayName) > maxDisplayNameBytes || strings.TrimSpace(manifest.DisplayName) != manifest.DisplayName {
		return fmt.Errorf("custom runtime display_name must be 1..%d trimmed bytes", maxDisplayNameBytes)
	}
	if len(manifest.Routes) == 0 || len(manifest.Routes) > MaxRoutes {
		return fmt.Errorf("custom runtime must contain 1..%d routes", MaxRoutes)
	}
	for index := range manifest.Routes {
		if err := validateRoute(manifest, manifest.Routes[index]); err != nil {
			return fmt.Errorf("custom runtime route[%d]: %w", index, err)
		}
		if index > 0 && manifest.Routes[index-1].HostEvent >= manifest.Routes[index].HostEvent {
			return fmt.Errorf("custom runtime routes must have unique lexically sorted host_event values")
		}
	}
	return nil
}

func validateRoute(manifest Manifest, route Route) error {
	if !validHostEvent(route.HostEvent) || !validEvent(route.Event) {
		return fmt.Errorf("host_event or neutral event is invalid")
	}
	if route.Support != SupportNative && route.Support != SupportAdapted && route.Support != SupportInferred && route.Support != SupportUnsupported {
		return fmt.Errorf("support is invalid")
	}
	if route.ErrorPolicy != FailureBlock && route.ErrorPolicy != FailureAllow && route.ErrorPolicy != FailureHost {
		return fmt.Errorf("error_policy is invalid")
	}
	if route.TimeoutPolicy != FailureBlock && route.TimeoutPolicy != FailureAllow && route.TimeoutPolicy != FailureHost {
		return fmt.Errorf("timeout_policy is invalid")
	}
	minimumOutputBytes := 256
	if manifest.FormatVersion == ManifestFormatVersion {
		minimumOutputBytes = 512
	}
	metadata, err := MarshalResponse(NeutralResponse{
		Schema: schema.Resolve(schema.NeutralHookResponse), FormatVersion: ResponseFormatVersion,
		Runtime: manifest.Runtime(), HostEvent: route.HostEvent, Event: route.Event,
		Decision: DecisionUnsupported, ExitCode: 2,
	})
	if err != nil {
		return fmt.Errorf("encode neutral response metadata: %w", err)
	}
	metadataBytes := len(metadata)
	if metadataBytes > minimumOutputBytes {
		minimumOutputBytes = metadataBytes
	}
	if route.TimeoutSeconds < 1 || route.TimeoutSeconds > 600 ||
		route.MaxOutputBytes < minimumOutputBytes || route.MaxOutputBytes > 64<<10 {
		return fmt.Errorf("route budget must use timeout_seconds 1..600 and max_output_bytes %d..65536", minimumOutputBytes)
	}
	if route.MaxContinuations < 0 || route.MaxContinuations > 100 {
		return fmt.Errorf("max_continuations must be 0..100")
	}
	wantResponse := ResponseObservation
	if routeNeedsPreExecution(route.Event) {
		wantResponse = ResponseDecision
	}
	if route.Event == EventStop {
		wantResponse = ResponseStopContinuation
	}
	if route.Response != wantResponse {
		return fmt.Errorf("event %q requires response %q", route.Event, wantResponse)
	}
	if route.Response == ResponseObservation && (route.ErrorPolicy == FailureBlock || route.TimeoutPolicy == FailureBlock) {
		return fmt.Errorf("observation-only routes cannot claim block failure policy")
	}
	if route.Event != EventStop && route.MaxContinuations != 0 {
		return fmt.Errorf("max_continuations is valid only for Stop")
	}
	if route.MaxContinuations > 0 && !route.Guarantees.Continuation {
		return fmt.Errorf("max_continuations requires a continuation guarantee")
	}
	if err := validateMappings(route.Fields); err != nil {
		return err
	}
	if route.Event == EventPreToolUse || route.Event == EventPermissionRequest || route.Event == EventPostToolUse || route.Event == EventPostToolUseFailure {
		if route.Fields.ToolName == "" || route.Fields.ToolInput == "" {
			return fmt.Errorf("event %q requires tool_name and tool_input mappings", route.Event)
		}
	}
	if route.Event == EventMCPBefore || route.Event == EventMCPAfter {
		if route.Fields.MCPTool == "" {
			return fmt.Errorf("event %q requires an mcp_tool mapping", route.Event)
		}
	}
	return nil
}

func validHostEvent(value string) bool {
	if value == "" || len(value) > 96 || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || index > 0 && strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validateMappings(fields FieldMappings) error {
	values := fieldMappingPointers(fields)
	if fields.SessionID == "" {
		return fmt.Errorf("fields.session_id is required")
	}
	seen := map[string]struct{}{}
	for _, pointer := range values {
		if pointer == "" {
			continue
		}
		if len(pointer) > maxPointerBytes || !validJSONPointer(pointer) {
			return fmt.Errorf("field mapping %q must be an RFC 6901 JSON Pointer", pointer)
		}
		if _, duplicate := seen[pointer]; duplicate {
			return fmt.Errorf("field mappings contain duplicate pointer %q", pointer)
		}
		seen[pointer] = struct{}{}
	}
	return nil
}

func fieldMappingPointers(fields FieldMappings) []string {
	return []string{
		fields.SessionID, fields.ToolName, fields.ToolInput, fields.ToolResponse,
		fields.ToolUseID, fields.Error, fields.IsInterrupt, fields.StopHookActive,
		fields.StrictContinuation, fields.ExitCode, fields.MCPTool,
		fields.MCPServerFingerprint, fields.MCPOutcome,
	}
}
