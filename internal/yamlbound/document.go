// Package yamlbound owns the bounded YAML-to-mapping admission boundary shared
// by policy parsing and schema validation.
package yamlbound

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
	rerrors "reconc.dev/reconc/internal/errors"
)

const (
	MaxDepth         = 32
	MaxNodes         = 131072
	MaxExpandedNodes = 262144
	MaxAliases       = 1024
	MaxScalarBytes   = 4 << 20
)

type bounds struct {
	nodes         int
	expandedNodes int
	scalarBytes   int
	aliases       int
	activeAliases map[*yaml.Node]bool
}

// DecodeMapping decodes exactly one bounded YAML mapping. It expands aliases
// only after proving the retained and expanded node budgets.
func DecodeMapping(body []byte, context string) (*yaml.Node, map[string]interface{}, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, map[string]interface{}{}, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, &rerrors.RuleValidationError{Message: "invalid yaml in " + context, Cause: err}
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, nil, &rerrors.RuleValidationError{Message: "YAML input must contain exactly one document in " + context}
		}
		return nil, nil, &rerrors.RuleValidationError{Message: "invalid trailing yaml in " + context, Cause: err}
	}
	limit := bounds{activeAliases: make(map[*yaml.Node]bool)}
	if err := walk(&document, 0, &limit, context); err != nil {
		return nil, nil, err
	}
	var decoded interface{}
	if err := document.Decode(&decoded); err != nil {
		return nil, nil, &rerrors.RuleValidationError{Message: "invalid yaml in " + context, Cause: err}
	}
	if decoded == nil {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if len(document.Content) == 1 {
			root = document.Content[0]
		}
		return root, map[string]interface{}{}, nil
	}
	mapping, ok := decoded.(map[string]interface{})
	if !ok || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil, &rerrors.RuleValidationError{Message: "expected a YAML mapping in " + context}
	}
	return document.Content[0], mapping, nil
}

func walk(node *yaml.Node, depth int, limit *bounds, context string) error {
	if node == nil {
		return nil
	}
	if depth > MaxDepth {
		return limitError(context, "yaml nesting depth", depth, MaxDepth, "levels")
	}
	limit.nodes++
	if limit.nodes > MaxNodes {
		return limitError(context, "yaml nodes", limit.nodes, MaxNodes, "nodes")
	}
	limit.expandedNodes++
	if limit.expandedNodes > MaxExpandedNodes {
		return limitError(context, "expanded yaml nodes", limit.expandedNodes, MaxExpandedNodes, "nodes")
	}
	if node.Kind == yaml.AliasNode {
		limit.aliases++
		if limit.aliases > MaxAliases {
			return limitError(context, "yaml aliases", limit.aliases, MaxAliases, "aliases")
		}
		if limit.activeAliases[node.Alias] {
			return &rerrors.RuleValidationError{Message: "recursive yaml alias in " + context}
		}
		limit.activeAliases[node.Alias] = true
		if err := walk(node.Alias, depth+1, limit, context); err != nil {
			return err
		}
		delete(limit.activeAliases, node.Alias)
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		limit.scalarBytes += len(node.Value)
		if limit.scalarBytes > MaxScalarBytes {
			return limitError(context, "decoded scalar bytes", limit.scalarBytes, MaxScalarBytes, "bytes")
		}
	}
	for _, child := range node.Content {
		if err := walk(child, depth+1, limit, context); err != nil {
			return err
		}
	}
	return nil
}

func limitError(context, field string, actual, maximum int, unit string) error {
	return &rerrors.RuleValidationError{Message: fmt.Sprintf(
		"%s %s actual=%d %s exceeds maximum=%d %s",
		context, field, actual, unit, maximum, unit,
	)}
}
