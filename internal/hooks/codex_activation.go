package hooks

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

const (
	codexActivationPath          = ".codex/config.toml"
	codexActivationRestorePrefix = "# reconc-restore-base64: "
)

type codexActivationPlan struct {
	target   string
	before   []byte
	after    []byte
	mode     os.FileMode
	action   string
	snapshot managedArtifactSnapshot
}

// RenderCodexActivation returns config.toml content with one marker-owned
// features.hooks=true entry. An explicit user-owned false requires force. If
// force replaces it, the exact original line is embedded so uninstall can
// restore it byte-for-byte.
func RenderCodexActivation(existing string, force bool) (string, error) {
	cleaned, managed, err := RemoveCodexActivation(existing)
	if err != nil {
		return "", err
	}
	lines := strings.SplitAfter(cleaned, "\n")
	activation, err := parseTOMLSectionBoolean(cleaned, "features", "hooks")
	if err != nil {
		return "", err
	}
	if activation.present {
		if activation.enabled {
			return cleaned, nil
		}
		if !force && !managed {
			return "", fmt.Errorf("features.hooks is explicitly false; rerun with --force to enable Codex hooks")
		}
		restore := base64.StdEncoding.EncodeToString([]byte(lines[activation.lineIndex]))
		enabledLine := "hooks = true"
		if activation.dotted {
			enabledLine = "features.hooks = true"
		}
		lines[activation.lineIndex] = codexActivationBlock(codexActivationRestorePrefix + restore + "\n" + enabledLine)
		return strings.Join(lines, ""), nil
	}

	block := codexActivationBlock("hooks = true")
	if activation.sectionStart != -1 {
		if !strings.HasSuffix(lines[activation.sectionStart], "\n") {
			lines[activation.sectionStart] += "\n"
		}
		lines = append(lines, "")
		copy(lines[activation.sectionStart+2:], lines[activation.sectionStart+1:])
		lines[activation.sectionStart+1] = block
		return strings.Join(lines, ""), nil
	}
	separator := ""
	if cleaned != "" && !strings.HasSuffix(cleaned, "\n") {
		separator = "\n"
	}
	if strings.TrimSpace(cleaned) != "" {
		separator += "\n"
	}
	return cleaned + separator + "[features]\n" + block, nil
}

// RemoveCodexActivation removes only Reconc's marker-owned activation block.
// If installation replaced an explicit false, the exact original line is
// restored after strict validation.
func RemoveCodexActivation(content string) (string, bool, error) {
	if strings.Count(content, CodexActivationBlockStart) > 1 || strings.Count(content, CodexActivationBlockEnd) > 1 {
		return "", false, fmt.Errorf("duplicate reconc Codex activation block markers")
	}
	start := strings.Index(content, CodexActivationBlockStart)
	end := strings.Index(content, CodexActivationBlockEnd)
	if start == -1 && end == -1 {
		return content, false, nil
	}
	if start == -1 || end == -1 || end < start {
		return "", false, fmt.Errorf("incomplete reconc Codex activation block")
	}
	bodyStart := start + len(CodexActivationBlockStart)
	body := content[bodyStart:end]
	replacement := ""
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, codexActivationRestorePrefix) {
			continue
		}
		if replacement != "" {
			return "", false, fmt.Errorf("duplicate Codex activation restore records")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, codexActivationRestorePrefix))
		if err != nil || !validDisabledHooksLine(decoded) {
			return "", false, fmt.Errorf("invalid Codex activation restore record")
		}
		replacement = string(decoded)
	}
	end += len(CodexActivationBlockEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:start] + replacement + content[end:], true, nil
}

func codexActivationBlock(body string) string {
	return CodexActivationBlockStart + "\n" + strings.TrimSuffix(body, "\n") + "\n" + CodexActivationBlockEnd + "\n"
}

func validDisabledHooksLine(line []byte) bool {
	if len(line) == 0 || bytes.ContainsRune(line, '\x00') {
		return false
	}
	trimmed := strings.TrimSpace(stripTOMLComment(string(line)))
	parts := strings.SplitN(trimmed, "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) != "false" {
		return false
	}
	key := strings.TrimSpace(parts[0])
	return key == "hooks" || key == "features.hooks"
}

type tomlSectionBooleanValue struct {
	enabled      bool
	present      bool
	dotted       bool
	lineIndex    int
	sectionStart int
}

func parseTOMLSectionBoolean(content, section, key string) (tomlSectionBooleanValue, error) {
	result := tomlSectionBooleanValue{lineIndex: -1, sectionStart: -1}
	currentSection := ""
	sectionSeen := false
	for lineNumber, raw := range strings.SplitAfter(content, "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if strings.HasPrefix(line, "[[") || strings.HasSuffix(line, "]]") {
				currentSection = ""
				continue
			}
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if currentSection == section {
				if sectionSeen || result.dotted {
					return tomlSectionBooleanValue{}, fmt.Errorf("duplicate [%s] table", section)
				}
				sectionSeen = true
				result.sectionStart = lineNumber
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		dotted := currentSection == "" && name == section+"."+key
		inSection := currentSection == section && name == key
		if currentSection == "" && name == key {
			return tomlSectionBooleanValue{}, fmt.Errorf("line %d places %s at the TOML root; expected [%s]", lineNumber+1, key, section)
		}
		if !dotted && !inSection {
			continue
		}
		if result.present || dotted && sectionSeen {
			return tomlSectionBooleanValue{}, fmt.Errorf("duplicate %s.%s", section, key)
		}
		result.present = true
		result.dotted = dotted
		result.lineIndex = lineNumber
		switch strings.TrimSpace(parts[1]) {
		case "true":
			result.enabled = true
		case "false":
			result.enabled = false
		default:
			return tomlSectionBooleanValue{}, fmt.Errorf("%s.%s must be a boolean", section, key)
		}
	}
	return result, nil
}

func stripTOMLComment(line string) string {
	var quote byte
	escaped := false
	for index := 0; index < len(line); index++ {
		value := line[index]
		if quote == '"' && escaped {
			escaped = false
			continue
		}
		if quote == '"' && value == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if value == quote {
				quote = 0
			}
			continue
		}
		if value == '"' || value == '\'' {
			quote = value
			continue
		}
		if value == '#' {
			return line[:index]
		}
	}
	return line
}

func planCodexActivation(root string, force bool) (*codexActivationPlan, error) {
	target := filepath.Join(root, filepath.FromSlash(codexActivationPath))
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	plan := &codexActivationPlan{target: target, mode: 0o644, action: "created"}
	snapshot, err := readManagedArtifactSnapshot(target)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: "read " + codexActivationPath, Cause: err}
	}
	plan.snapshot = snapshot
	if snapshot.exists {
		plan.before = snapshot.body
		plan.mode = snapshot.info.Mode().Perm()
		plan.action = "updated"
	}
	after, err := RenderCodexActivation(string(plan.before), force)
	if err != nil {
		return nil, &rerrors.PolicySourceError{Message: codexActivationPath + ": " + err.Error()}
	}
	plan.after = []byte(after)
	if bytes.Equal(plan.before, plan.after) {
		plan.action = "unchanged"
	}
	return plan, nil
}

func applyCodexActivation(plan *codexActivationPlan) (string, error) {
	if plan == nil {
		return "", nil
	}
	if err := revalidateManagedArtifactSnapshot(plan.target, plan.snapshot); err != nil {
		return "", &rerrors.PolicySourceError{Message: "revalidate " + codexActivationPath, Cause: err}
	}
	if plan.action == "unchanged" {
		return plan.action, nil
	}
	if err := os.MkdirAll(filepath.Dir(plan.target), 0o755); err != nil {
		return "", &rerrors.PolicySourceError{Message: "create Codex activation directory", Cause: err}
	}
	changed, err := writeGeneratedArtifact(plan.target, string(plan.after), false)
	if err != nil {
		return "", err
	}
	if changed == "unchanged" {
		return "unchanged", nil
	}
	return plan.action, nil
}
