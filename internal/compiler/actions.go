package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
)

const legacyMCPIDPrefix = "legacy-mcp-"

func compileCanonicalActions(parsed *parser.ParsedPolicy) (*action.CompiledPlan, error) {
	if parsed == nil {
		return nil, &rerrors.RuleValidationError{Message: "parsed policy is nil"}
	}
	plan := action.Plan{FormatVersion: action.PlanFormatVersion}
	if parsed.Actions != nil {
		plan = action.Plan{
			FormatVersion: parsed.Actions.FormatVersion,
			Tools:         append([]action.Tool(nil), parsed.Actions.Tools...),
			Rules:         append([]action.Rule(nil), parsed.Actions.Rules...),
			Budgets:       append([]action.Budget(nil), parsed.Actions.Budgets...),
			Approvals:     append([]action.ApprovalDisclosure(nil), parsed.Actions.Approvals...),
			Detectors:     append([]action.DetectorPolicy(nil), parsed.Actions.Detectors...),
			Defaults:      parsed.Actions.Defaults,
		}
	}
	if parsed.MCP != nil {
		if err := lowerLegacyMCP(&plan, *parsed.MCP); err != nil {
			return nil, &rerrors.RuleValidationError{Message: err.Error()}
		}
	}
	compiled, err := action.CompilePlan(plan)
	if err != nil {
		return nil, &rerrors.RuleValidationError{Message: "actions: " + err.Error()}
	}
	if err := actioninspect.ValidateCompiledPlan(compiled); err != nil {
		return nil, &rerrors.RuleValidationError{Message: "actions: " + err.Error()}
	}
	return compiled, nil
}

func lowerLegacyMCP(plan *action.Plan, legacy policy.MCPPolicy) error {
	if plan == nil {
		return fmt.Errorf("canonical action plan is nil")
	}
	if err := legacy.Validate(); err != nil {
		return fmt.Errorf("legacy MCP policy is invalid: %w", err)
	}
	hostDefault := action.DecisionAllow
	if legacy.Unclassified == policy.MCPUnclassifiedDeny {
		hostDefault = action.DecisionBlock
	}
	if plan.Defaults.HostUnmatched != "" && plan.Defaults.HostUnmatched != hostDefault {
		return fmt.Errorf("actions.defaults.host_unmatched conflicts with legacy mcp.unclassified")
	}
	plan.Defaults.HostUnmatched = hostDefault
	for _, tool := range policy.SortedMCPTools(legacy.Tools) {
		plan.Tools = append(plan.Tools, lowerLegacyMCPTool(tool))
	}
	return nil
}

func lowerLegacyMCPTool(tool policy.MCPToolPolicy) action.Tool {
	identity := string(tool.Platform) + "\x00" + tool.ServerFingerprint + "\x00" + tool.Tool
	digest := sha256.Sum256([]byte(identity))
	return action.Tool{
		ID:                legacyMCPIDPrefix + hex.EncodeToString(digest[:])[:48],
		Transport:         action.TransportHostMCP,
		Platform:          action.Platform(tool.Platform),
		ServerFingerprint: tool.ServerFingerprint,
		Tool:              tool.Tool,
		Effect: action.Effect{
			Kind:         action.EffectKind(tool.Effect),
			PathFields:   append([]string(nil), tool.PathFields...),
			CommandField: tool.CommandField,
		},
		Origin:         action.OriginLegacyMCP,
		SourceIdentity: tool.SourcePath,
	}
}

func decodeLegacyMCP(raw interface{}) (*policy.MCPPolicy, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode legacy MCP contract: %w", err)
	}
	var contract policy.MCPPolicy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return nil, fmt.Errorf("decode legacy MCP contract: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("legacy MCP contract contains trailing JSON")
	}
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	contract.Tools = policy.SortedMCPTools(contract.Tools)
	return &contract, nil
}
