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
// features.hooks=true entry. An explicit user-owned false requires force. Any
// replacement embeds the exact original expression so uninstall can restore
// it byte-for-byte.
func RenderCodexActivation(existing string, force bool) (string, error) {
	cleaned, managed, err := RemoveCodexActivation(existing)
	if err != nil {
		return "", err
	}
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
		return replaceCodexActivationValue(cleaned, activation)
	}
	if activation.inlineTable.valid() {
		return extendInlineCodexFeatures(cleaned, activation)
	}
	if activation.sectionInsert >= 0 {
		return insertCodexActivation(cleaned, activation.sectionInsert, codexActivationBlock("hooks = true")), nil
	}
	if activation.sectionExists {
		return insertCodexActivation(cleaned, activation.rootInsert, codexActivationBlock("features.hooks = true")), nil
	}
	separator := ""
	if cleaned != "" && !strings.HasSuffix(cleaned, "\n") {
		separator = "\n"
	}
	if strings.TrimSpace(cleaned) != "" {
		separator += "\n"
	}
	return cleaned + separator + "[features]\n" + codexActivationBlock("hooks = true"), nil
}

// RemoveCodexActivation removes only Reconc's marker-owned activation block.
// If installation replaced an explicit false or extended an inline table, the
// exact original expression is restored after strict validation.
func RemoveCodexActivation(content string) (string, bool, error) {
	block, found, err := findCodexActivationBlock([]byte(content))
	if err != nil {
		return "", false, err
	}
	if !found {
		return content, false, nil
	}
	replacement := ""
	for _, encoded := range block.restores {
		if replacement != "" {
			return "", false, fmt.Errorf("duplicate Codex activation restore records")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || bytes.ContainsRune(decoded, '\x00') || !validCodexActivationRestore(decoded) {
			return "", false, fmt.Errorf("invalid Codex activation restore record")
		}
		replacement = string(decoded)
	}
	return content[:block.span.start] + replacement + content[block.span.end:], true, nil
}

func codexActivationBlock(body string) string {
	return CodexActivationBlockStart + "\n" + strings.TrimSuffix(body, "\n") + "\n" + CodexActivationBlockEnd + "\n"
}

func replaceCodexActivationValue(content string, activation tomlSectionBooleanValue) (string, error) {
	data := []byte(content)
	if string(data[activation.value.start:activation.value.end]) != "false" {
		return "", fmt.Errorf("features.hooks source is not the parsed false boolean")
	}
	original := data[activation.expression.start:activation.expression.end]
	relativeStart := activation.value.start - activation.expression.start
	relativeEnd := activation.value.end - activation.expression.start
	enabled := append([]byte(nil), original[:relativeStart]...)
	enabled = append(enabled, "true"...)
	enabled = append(enabled, original[relativeEnd:]...)
	return replaceCodexActivationExpression(content, activation.expression, original, enabled), nil
}

func extendInlineCodexFeatures(content string, activation tomlSectionBooleanValue) (string, error) {
	data := []byte(content)
	if activation.inlineTable.end <= activation.inlineTable.start || data[activation.inlineTable.end-1] != '}' {
		return "", fmt.Errorf("cannot locate the end of the inline features table")
	}
	original := data[activation.expression.start:activation.expression.end]
	insert := activation.inlineTable.end - 1 - activation.expression.start
	inner := bytes.TrimSpace(data[activation.inlineTable.start+1 : activation.inlineTable.end-1])
	addition := []byte("hooks = true")
	if activation.inlineEntries && len(inner) != 0 && inner[len(inner)-1] == ',' {
		addition = []byte(" hooks = true")
	} else if activation.inlineEntries {
		addition = []byte(", hooks = true")
	}
	enabled := append([]byte(nil), original[:insert]...)
	enabled = append(enabled, addition...)
	enabled = append(enabled, original[insert:]...)
	return replaceCodexActivationExpression(content, activation.expression, original, enabled), nil
}

func replaceCodexActivationExpression(content string, expression tomlSourceRange, original, enabled []byte) string {
	restore := base64.StdEncoding.EncodeToString(original)
	block := codexActivationBlock(codexActivationRestorePrefix + restore + "\n" + string(enabled))
	return content[:expression.start] + block + content[expression.end:]
}

func insertCodexActivation(content string, offset int, block string) string {
	separator := ""
	if offset > 0 && content[offset-1] != '\n' {
		separator = "\n"
	}
	return content[:offset] + separator + block + content[offset:]
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
	if plan.action != "unchanged" {
		if err := os.MkdirAll(filepath.Dir(plan.target), 0o755); err != nil {
			return "", &rerrors.PolicySourceError{Message: "create Codex activation directory", Cause: err}
		}
	}
	published, err := publishManagedArtifact(plan.target, plan.after, plan.mode, plan.snapshot)
	if err != nil {
		return "", &rerrors.PolicySourceError{Message: "write " + codexActivationPath, Cause: err}
	}
	if published == "unchanged" {
		return "unchanged", nil
	}
	if plan.action == "unchanged" {
		return "", &rerrors.PolicySourceError{Message: "verify " + codexActivationPath, Cause: fmt.Errorf("unexpected publication action %q", published)}
	}
	return plan.action, nil
}
