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
	target  string
	before  []byte
	after   []byte
	mode    os.FileMode
	action  string
	existed bool
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
	sectionStart := -1
	currentSection := ""
	hooksIndex := -1
	hooksEnabled := false
	for index, raw := range lines {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && !strings.HasPrefix(line, "[[") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			currentSection = section
			if section == "features" {
				if sectionStart != -1 {
					return "", fmt.Errorf("duplicate [features] table")
				}
				sectionStart = index
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != "hooks" {
			continue
		}
		if currentSection != "features" {
			if currentSection == "" {
				return "", fmt.Errorf("hooks must be declared inside [features]")
			}
			continue
		}
		if hooksIndex != -1 {
			return "", fmt.Errorf("duplicate features.hooks")
		}
		switch strings.TrimSpace(parts[1]) {
		case "true":
			hooksEnabled = true
		case "false":
			hooksEnabled = false
		default:
			return "", fmt.Errorf("features.hooks must be a boolean")
		}
		hooksIndex = index
	}
	if hooksIndex != -1 {
		if hooksEnabled {
			return cleaned, nil
		}
		if !force && !managed {
			return "", fmt.Errorf("features.hooks is explicitly false; rerun with --force to enable Codex hooks")
		}
		restore := base64.StdEncoding.EncodeToString([]byte(lines[hooksIndex]))
		lines[hooksIndex] = codexActivationBlock(codexActivationRestorePrefix + restore + "\nhooks = true")
		return strings.Join(lines, ""), nil
	}

	block := codexActivationBlock("hooks = true")
	if sectionStart != -1 {
		lines = append(lines, "")
		copy(lines[sectionStart+2:], lines[sectionStart+1:])
		lines[sectionStart+1] = block
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
	trimmed := strings.TrimSpace(strings.SplitN(string(line), "#", 2)[0])
	parts := strings.SplitN(trimmed, "=", 2)
	return len(parts) == 2 && strings.TrimSpace(parts[0]) == "hooks" && strings.TrimSpace(parts[1]) == "false"
}

func planCodexActivation(root string, force bool) (*codexActivationPlan, error) {
	target := filepath.Join(root, filepath.FromSlash(codexActivationPath))
	if err := requireManagedTargetWithin(root, target); err != nil {
		return nil, err
	}
	plan := &codexActivationPlan{target: target, mode: 0o644, action: "created"}
	info, err := os.Lstat(target)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, &rerrors.PolicySourceError{Message: codexActivationPath + " is not a real regular file"}
		}
		if info.Size() > maxManagedArtifactBytes {
			return nil, &rerrors.PolicySourceError{Message: fmt.Sprintf("%s exceeds the %d-byte managed-artifact limit", codexActivationPath, maxManagedArtifactBytes)}
		}
		plan.before, err = readManagedArtifact(target)
		if err != nil {
			return nil, &rerrors.PolicySourceError{Message: "read " + codexActivationPath, Cause: err}
		}
		plan.mode = info.Mode().Perm()
		plan.action = "updated"
		plan.existed = true
	} else if !os.IsNotExist(err) {
		return nil, &rerrors.PolicySourceError{Message: "inspect " + codexActivationPath, Cause: err}
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
	current, err := readManagedArtifact(plan.target)
	if !plan.existed && os.IsNotExist(err) {
		current = nil
		err = nil
	}
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "revalidate " + codexActivationPath, Cause: err}
	}
	if !bytes.Equal(current, plan.before) {
		return "", &rerrors.PolicySourceError{Message: codexActivationPath + " changed after install preflight; retry"}
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
