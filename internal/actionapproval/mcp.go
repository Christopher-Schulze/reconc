package actionapproval

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/action"
)

const (
	MCPProtocolVersion        = "2026-07-28"
	MCPApprovalInputID        = "reconc_approval"
	MCPResultTypeInputNeeded  = "input_required"
	MaxMCPApprovalMessage     = MaxApprovalObjectBytes + 1024
	MaxMCPApprovalRetryBytes  = action.MaxArgumentBytes + 2*MaxApprovalObjectBytes
	MaxMCPApprovalStateBytes  = 4096
	MaxMCPApprovalStateLength = 8192
)

type MCPStringSchema struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	MinLength   int    `json:"minLength"`
	MaxLength   int    `json:"maxLength"`
}

type MCPRequestedSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]MCPStringSchema `json:"properties"`
	Required   []string                   `json:"required"`
}

type MCPElicitParams struct {
	Mode            string             `json:"mode"`
	Message         string             `json:"message"`
	RequestedSchema MCPRequestedSchema `json:"requestedSchema"`
}

type MCPElicitRequest struct {
	Method string          `json:"method"`
	Params MCPElicitParams `json:"params"`
}

type MCPInputRequiredResult struct {
	ResultType    string                      `json:"resultType"`
	InputRequests map[string]MCPElicitRequest `json:"inputRequests"`
	RequestState  string                      `json:"requestState"`
}

type MCPApprovalFailure struct {
	FormatVersion  string            `json:"format_version"`
	Outcome        string            `json:"outcome"`
	ReasonCode     action.ReasonCode `json:"reason_code"`
	Message        string            `json:"message"`
	CorrelationID  string            `json:"correlation_id"`
	DispatchStatus string            `json:"dispatch_status"`
	DeliveryStatus string            `json:"delivery_status"`
}

func BuildMCPInputRequired(request Request, requestState string) (MCPInputRequiredResult, error) {
	requestBody, err := EncodeRequest(request)
	if err != nil {
		return MCPInputRequiredResult{}, err
	}
	if !validMCPRequestState(requestState) {
		return MCPInputRequiredResult{}, approvalError(action.ReasonApprovalInvalid, "MCP approval request state is invalid", nil)
	}
	message := "Reconc requires an independent signed receipt for this exact canonical request: " + string(requestBody)
	if len(message) > MaxMCPApprovalMessage {
		return MCPInputRequiredResult{}, approvalError(action.ReasonLimitExceeded, "MCP approval message exceeds its bound", nil)
	}
	return MCPInputRequiredResult{
		ResultType: MCPResultTypeInputNeeded,
		InputRequests: map[string]MCPElicitRequest{
			MCPApprovalInputID: {
				Method: "elicitation/create",
				Params: MCPElicitParams{
					Mode: "form", Message: message,
					RequestedSchema: MCPRequestedSchema{
						Type: "object",
						Properties: map[string]MCPStringSchema{
							"receipt": {
								Type: "string", Title: "Signed Reconc approval receipt",
								Description: "Canonical receipt JSON signed by a configured independent authority.",
								MinLength:   1, MaxLength: MaxApprovalObjectBytes,
							},
						},
						Required: []string{"receipt"},
					},
				},
			},
		},
		RequestState: requestState,
	}, nil
}

func UnsupportedMCPApproval(correlationID string) (MCPApprovalFailure, error) {
	if !validOpaqueBinding(correlationID) {
		return MCPApprovalFailure{}, fmt.Errorf("approval correlation identity is invalid")
	}
	return MCPApprovalFailure{
		FormatVersion: FormatVersion, Outcome: "approval_required",
		ReasonCode:    action.ReasonApprovalRequired,
		Message:       "A signed approval receipt is required; this client cannot complete the input-required flow.",
		CorrelationID: correlationID, DispatchStatus: "not_dispatched", DeliveryStatus: "blocked",
	}, nil
}

// ParseMCPApprovalRetry enforces the 2026-07-28 multi-round-trip shape: a fresh
// JSON-RPC ID, byte-equivalent canonical original params plus inputResponses and
// the exact opaque requestState. It returns only the bounded receipt bytes.
func ParseMCPApprovalRetry(
	originalRPCID []byte,
	retryRPCID []byte,
	originalParams []byte,
	retryParams []byte,
	expectedRequestState string,
) ([]byte, error) {
	if len(originalParams) == 0 || len(originalParams) > MaxMCPApprovalRetryBytes ||
		len(retryParams) == 0 || len(retryParams) > MaxMCPApprovalRetryBytes {
		return nil, approvalError(action.ReasonLimitExceeded, "MCP approval retry params exceed their bound", nil)
	}
	originalID, err := parseMCPRPCID(originalRPCID)
	if err != nil {
		return nil, err
	}
	retryID, err := parseMCPRPCID(retryRPCID)
	if err != nil || originalID.Equal(retryID) {
		return nil, approvalError(action.ReasonProtocolError, "MCP approval retry requires a fresh JSON-RPC ID", err)
	}
	original, err := action.ParseObjectJSON(originalParams)
	if err != nil {
		return nil, approvalError(action.ReasonProtocolError, "decode original MCP tool params", err)
	}
	retry, err := action.ParseObjectJSON(retryParams)
	if err != nil {
		return nil, approvalError(action.ReasonProtocolError, "decode retry MCP tool params", err)
	}
	return parseMCPApprovalResponse(original, retry, expectedRequestState)
}

// CanonicalMCPApprovalBaseParams removes an already-consumed multi-round-trip
// response before another independent approval round is issued. Callers may use
// it only after the current retry fields have been authenticated and consumed.
func CanonicalMCPApprovalBaseParams(params []byte) ([]byte, error) {
	if len(params) == 0 || len(params) > MaxMCPApprovalRetryBytes {
		return nil, approvalError(action.ReasonLimitExceeded, "MCP approval params exceed their bound", nil)
	}
	value, err := action.ParseObjectJSON(params)
	if err != nil {
		return nil, approvalError(action.ReasonProtocolError, "decode MCP approval params", err)
	}
	base, _, _, err := splitMCPApprovalParams(value)
	if err != nil {
		return nil, err
	}
	body, err := base.MarshalJSON()
	if err != nil {
		return nil, approvalError(action.ReasonProtocolError, "canonicalize MCP approval base params", err)
	}
	return body, nil
}

func parseMCPRPCID(raw []byte) (action.Value, error) {
	if len(raw) == 0 || len(raw) > 1024 {
		return action.Value{}, approvalError(action.ReasonProtocolError, "MCP JSON-RPC ID is outside its bound", nil)
	}
	value, err := action.ParseJSON(raw)
	if err != nil || value.Kind() != action.ValueString && value.Kind() != action.ValueNumber {
		return action.Value{}, approvalError(action.ReasonProtocolError, "MCP JSON-RPC ID must be a string or number", err)
	}
	return value, nil
}

func parseMCPApprovalResponse(
	original action.Value,
	retry action.Value,
	expectedState string,
) ([]byte, error) {
	if !validMCPRequestState(expectedState) {
		return nil, approvalError(action.ReasonApprovalInvalid, "expected MCP approval request state is invalid", nil)
	}
	originalBase, originalResponses, originalState, err := splitMCPApprovalParams(original)
	if err != nil || originalResponses != nil || originalState != "" {
		return nil, approvalError(action.ReasonProtocolError, "original MCP params already contain approval retry fields", err)
	}
	retryBase, responses, requestState, err := splitMCPApprovalParams(retry)
	if err != nil {
		return nil, err
	}
	if responses == nil || requestState != expectedState || !originalBase.Equal(retryBase) {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP approval retry changed params or request state", nil)
	}
	return extractMCPReceipt(*responses)
}

func splitMCPApprovalParams(value action.Value) (action.Value, *action.Value, string, error) {
	members, ok := value.Members()
	if !ok {
		return action.Value{}, nil, "", approvalError(action.ReasonProtocolError, "MCP tool params must be an object", nil)
	}
	base := make([]action.Member, 0, len(members))
	var responses *action.Value
	requestState := ""
	for _, member := range members {
		switch member.Name {
		case "inputResponses":
			copy := member.Value
			responses = &copy
		case "requestState":
			text, valid := member.Value.Text()
			if !valid {
				return action.Value{}, nil, "", approvalError(action.ReasonProtocolError, "MCP requestState must be a string", nil)
			}
			requestState = text
		default:
			base = append(base, member)
		}
	}
	baseValue, err := action.Object(base)
	return baseValue, responses, requestState, err
}

func extractMCPReceipt(responses action.Value) ([]byte, error) {
	members, ok := responses.Members()
	if !ok || len(members) != 1 || members[0].Name != MCPApprovalInputID {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP inputResponses must contain only reconc_approval", nil)
	}
	response, ok := members[0].Value.Members()
	if !ok || len(response) < 1 || len(response) > 2 || response[0].Name != "action" {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP approval input response is malformed", nil)
	}
	actionValue, ok := response[0].Value.Text()
	if !ok {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP approval action must be a string", nil)
	}
	if actionValue == "decline" {
		return nil, approvalError(action.ReasonApprovalRejected, "MCP user declined approval input", nil)
	}
	if actionValue == "cancel" {
		return nil, approvalError(action.ReasonCancelled, "MCP user cancelled approval input", nil)
	}
	if actionValue != "accept" || len(response) != 2 || response[1].Name != "content" {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP approval action or content is invalid", nil)
	}
	content, ok := response[1].Value.Members()
	if !ok || len(content) != 1 || content[0].Name != "receipt" {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP approval content must contain only receipt", nil)
	}
	receipt, ok := content[0].Value.Text()
	if !ok || len(receipt) == 0 || len(receipt) > MaxApprovalObjectBytes {
		return nil, approvalError(action.ReasonApprovalInvalid, "MCP approval receipt is outside its bound", nil)
	}
	return bytes.Clone([]byte(receipt)), nil
}

func EncodeMCPInputRequired(result MCPInputRequiredResult) ([]byte, error) {
	if err := validateMCPInputRequired(result); err != nil {
		return nil, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxMCPApprovalMessage+MaxMCPApprovalStateLength+4096 {
		return nil, approvalError(action.ReasonLimitExceeded, "MCP input-required result exceeds its bound", nil)
	}
	return body, nil
}

func validateMCPInputRequired(result MCPInputRequiredResult) error {
	request, exists := result.InputRequests[MCPApprovalInputID]
	if result.ResultType != MCPResultTypeInputNeeded || len(result.InputRequests) != 1 || !exists ||
		!validMCPRequestState(result.RequestState) || request.Method != "elicitation/create" ||
		request.Params.Mode != "form" || !strings.HasPrefix(
		request.Params.Message,
		"Reconc requires an independent signed receipt for this exact canonical request: {",
	) || len(request.Params.Message) > MaxMCPApprovalMessage {
		return approvalError(action.ReasonProtocolError, "MCP input-required approval result is invalid", nil)
	}
	schema := request.Params.RequestedSchema
	receipt, exists := schema.Properties["receipt"]
	if schema.Type != "object" || len(schema.Properties) != 1 || !exists ||
		len(schema.Required) != 1 || schema.Required[0] != "receipt" ||
		receipt.Type != "string" || receipt.Title != "Signed Reconc approval receipt" ||
		receipt.Description != "Canonical receipt JSON signed by a configured independent authority." ||
		receipt.MinLength != 1 || receipt.MaxLength != MaxApprovalObjectBytes {
		return approvalError(action.ReasonProtocolError, "MCP approval elicitation schema is invalid", nil)
	}
	return nil
}

func validMCPRequestState(value string) bool {
	if value == "" || len(value) > MaxMCPApprovalStateLength ||
		len(value) > base64.RawURLEncoding.EncodedLen(MaxMCPApprovalStateBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) > 0 && len(decoded) <= MaxMCPApprovalStateBytes &&
		base64.RawURLEncoding.EncodeToString(decoded) == value
}
