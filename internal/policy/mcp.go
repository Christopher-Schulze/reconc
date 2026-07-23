package policy

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MCPUnclassifiedMode controls calls that a host identifies as MCP but that
// have no exact configured selector.
type MCPUnclassifiedMode string

const (
	MCPUnclassifiedHost MCPUnclassifiedMode = "host"
	MCPUnclassifiedDeny MCPUnclassifiedMode = "deny"
)

// MCPPlatform is the host namespace used in an exact MCP selector.
type MCPPlatform string

const (
	MCPPlatformCursor   MCPPlatform = "cursor"
	MCPPlatformOpenCode MCPPlatform = "opencode"
	MCPPlatformKilo     MCPPlatform = "kilo"
)

// MCPEffect is the only behavior an operator may assign to an exact MCP tool.
type MCPEffect string

const (
	MCPEffectRepositoryRead  MCPEffect = "repository_read"
	MCPEffectRepositoryWrite MCPEffect = "repository_write"
	MCPEffectCommand         MCPEffect = "command"
	MCPEffectExternal        MCPEffect = "external"
)

// MCPToolPolicy classifies one exact host-emitted MCP identity. SourcePath is
// compiler provenance; it is not accepted in authoring config.
type MCPToolPolicy struct {
	Platform          MCPPlatform `json:"platform" yaml:"platform"`
	ServerFingerprint string      `json:"server_fingerprint,omitempty" yaml:"server_fingerprint,omitempty"`
	Tool              string      `json:"tool" yaml:"tool"`
	Effect            MCPEffect   `json:"effect" yaml:"effect"`
	PathFields        []string    `json:"path_fields,omitempty" yaml:"path_fields,omitempty"`
	CommandField      string      `json:"command_field,omitempty" yaml:"command_field,omitempty"`
	SourcePath        string      `json:"source_path,omitempty" yaml:"-"`
}

// MCPPolicy is the deterministic compiler and runtime contract for MCP calls.
type MCPPolicy struct {
	Unclassified MCPUnclassifiedMode `json:"unclassified" yaml:"unclassified"`
	Tools        []MCPToolPolicy     `json:"tools" yaml:"tools"`
}

var mcpFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Valid reports whether the mode is part of the public contract.
func (m MCPUnclassifiedMode) Valid() bool {
	return m == MCPUnclassifiedHost || m == MCPUnclassifiedDeny
}

// Valid reports whether the platform is part of the public contract.
func (p MCPPlatform) Valid() bool {
	return p == MCPPlatformCursor || p == MCPPlatformOpenCode || p == MCPPlatformKilo
}

// Valid reports whether the effect is part of the public contract.
func (e MCPEffect) Valid() bool {
	switch e {
	case MCPEffectRepositoryRead, MCPEffectRepositoryWrite, MCPEffectCommand, MCPEffectExternal:
		return true
	default:
		return false
	}
}

// StableKey is the collision-free exact selector identity used for sorting and
// runtime lookup.
func (t MCPToolPolicy) StableKey() string {
	return string(t.Platform) + "\x00" + t.ServerFingerprint + "\x00" + t.Tool
}

// Validate checks the complete cross-field MCP contract.
func (p MCPPolicy) Validate() error {
	if !p.Unclassified.Valid() {
		return fmt.Errorf("mcp.unclassified must be host or deny")
	}
	seen := make(map[string]struct{}, len(p.Tools))
	for index, tool := range p.Tools {
		if err := tool.Validate(); err != nil {
			return fmt.Errorf("mcp.tools[%d]: %w", index, err)
		}
		key := tool.StableKey()
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("mcp.tools[%d]: duplicate platform, server_fingerprint, and tool selector", index)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// Validate checks one exact MCP selector and its effect-specific fields.
func (t MCPToolPolicy) Validate() error {
	if !t.Platform.Valid() {
		return fmt.Errorf("platform must be cursor, opencode, or kilo")
	}
	if t.Tool == "" || strings.TrimSpace(t.Tool) != t.Tool {
		return fmt.Errorf("tool must be an exact non-empty identity without surrounding whitespace")
	}
	if t.ServerFingerprint != "" && !mcpFingerprintPattern.MatchString(t.ServerFingerprint) {
		return fmt.Errorf("server_fingerprint must be sha256: followed by 64 lowercase hexadecimal characters")
	}
	if !t.Effect.Valid() {
		return fmt.Errorf("effect must be repository_read, repository_write, command, or external")
	}
	seenPointers := make(map[string]struct{}, len(t.PathFields))
	for index, pointer := range t.PathFields {
		if pointer == "" || !ValidJSONPointer(pointer) {
			return fmt.Errorf("path_fields[%d] must be a non-root RFC 6901 JSON Pointer", index)
		}
		if _, duplicate := seenPointers[pointer]; duplicate {
			return fmt.Errorf("path_fields[%d] duplicates an earlier JSON Pointer", index)
		}
		seenPointers[pointer] = struct{}{}
	}
	if t.CommandField != "" && !ValidJSONPointer(t.CommandField) {
		return fmt.Errorf("command_field is not a valid RFC 6901 JSON Pointer")
	}
	switch t.Effect {
	case MCPEffectRepositoryRead, MCPEffectRepositoryWrite:
		if len(t.PathFields) == 0 {
			return fmt.Errorf("path_fields must be non-empty for repository effects")
		}
		if t.CommandField != "" {
			return fmt.Errorf("command_field is forbidden for repository effects")
		}
	case MCPEffectCommand:
		if t.CommandField == "" {
			return fmt.Errorf("command_field is required for command effects")
		}
		if len(t.PathFields) != 0 {
			return fmt.Errorf("path_fields is forbidden for command effects")
		}
	case MCPEffectExternal:
		if len(t.PathFields) != 0 || t.CommandField != "" {
			return fmt.Errorf("path_fields and command_field are forbidden for external effects")
		}
	}
	return nil
}

// SortedMCPTools returns a defensive canonical-order copy.
func SortedMCPTools(tools []MCPToolPolicy) []MCPToolPolicy {
	out := append([]MCPToolPolicy(nil), tools...)
	for index := range out {
		out[index].PathFields = append([]string(nil), out[index].PathFields...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StableKey() < out[j].StableKey()
	})
	return out
}

// ValidJSONPointer validates RFC 6901 syntax without interpreting a value.
func ValidJSONPointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	if pointer[0] != '/' {
		return false
	}
	for index := 1; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1') {
			return false
		}
		index++
	}
	return true
}

// ResolveJSONPointer selects one value without recursive or heuristic search.
func ResolveJSONPointer(root interface{}, pointer string) (interface{}, bool) {
	if !ValidJSONPointer(pointer) {
		return nil, false
	}
	if pointer == "" {
		return root, true
	}
	current := root
	for _, encoded := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch container := current.(type) {
		case map[string]interface{}:
			next, ok := container[token]
			if !ok {
				return nil, false
			}
			current = next
		case []interface{}:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || strconv.Itoa(index) != token || index >= len(container) {
				return nil, false
			}
			current = container[index]
		default:
			return nil, false
		}
	}
	return current, true
}
