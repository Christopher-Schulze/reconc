package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
)

var hookRuntimeSupportProbe = func() bool {
	return true
}

type hookRuntimeCheckResult struct {
	Status string
	Detail string
}

func inspectHookRuntimeCompatibility(discovery ingest.DiscoveryResult) hookRuntimeCheckResult {
	result := hookRuntimeCheckResult{
		Status: doctorStatusOK,
		Detail: "no agent hook configs installed",
	}
	if !discovery.Discovered {
		result.Status = doctorStatusWarn
		result.Detail = "cannot inspect hook configs without a discovered reconc repo"
		return result
	}

	type hookTarget struct {
		kind string
		path string
	}
	targets := []hookTarget{
		{kind: hooks.KindClaudeCode, path: hooks.ClaudeCodeSettingsPath},
		{kind: hooks.KindCodex, path: hooks.CodexHooksPath},
		{kind: hooks.KindCursor, path: hooks.CursorHooksPath},
		{kind: hooks.KindOpenCode, path: hooks.OpenCodePluginPath},
		{kind: hooks.KindAntigravity, path: hooks.AntigravityHooksPath},
	}

	parts := make([]string, 0, len(targets))
	found := false
	hasWarn := false
	hasRuntimeConfig := false
	runtimeKinds := make([]string, 0, len(targets))
	for _, target := range targets {
		full := filepath.Join(discovery.RepoRoot, target.path)
		data, err := os.ReadFile(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			found = true
			hasWarn = true
			parts = append(parts, target.kind+": unreadable ("+err.Error()+")")
			continue
		}
		found = true
		text := string(data)
		if target.kind == hooks.KindOpenCode {
			hasOpenCodeRuntime := strings.Contains(text, "reconc hook runtime ") ||
				strings.Contains(text, `["hook", "runtime", event, repo]`)
			switch {
			case hasOpenCodeRuntime &&
				strings.Contains(text, "opencode-pre-tool-use") &&
				strings.Contains(text, "opencode-post-tool-use") &&
				strings.Contains(text, "opencode-stop"):
				hasRuntimeConfig = true
				runtimeKinds = append(runtimeKinds, target.kind)
				parts = append(parts, target.kind+": runtime plugin present")
			case strings.Contains(text, "reconc "):
				hasWarn = true
				parts = append(parts, target.kind+": plugin has no complete `reconc hook runtime` coverage; reinstall with `reconc hook install "+target.kind+" --force`")
			default:
				parts = append(parts, target.kind+": plugin present, no reconc-managed hooks")
			}
			continue
		}
		if target.kind == hooks.KindAntigravity {
			hasAntigravityRuntime := strings.Contains(text, "reconc hook runtime ") ||
				strings.Contains(text, "tools/reconc/bin/hook") ||
				strings.Contains(text, "tools/reconc/dist/reconc-")
			switch {
			case hasAntigravityRuntime &&
				strings.Contains(text, "antigravity-pre-invocation") &&
				strings.Contains(text, "antigravity-pre-tool-use") &&
				strings.Contains(text, "antigravity-post-tool-use") &&
				strings.Contains(text, "antigravity-post-invocation") &&
				strings.Contains(text, "antigravity-stop"):
				hasRuntimeConfig = true
				runtimeKinds = append(runtimeKinds, target.kind)
				parts = append(parts, target.kind+": runtime hooks present")
			case strings.Contains(text, "reconc "):
				hasWarn = true
				parts = append(parts, target.kind+": hook config has no complete `reconc hook runtime` coverage; reinstall with `reconc hook install "+target.kind+" --force`")
			default:
				parts = append(parts, target.kind+": config present, no reconc-managed hooks")
			}
			continue
		}

		switch {
		case !json.Valid(data):
			hasWarn = true
			parts = append(parts, target.kind+": invalid JSON; reinstall with `reconc hook install "+target.kind+" --force`")
		case strings.Contains(text, "tools/reconc/bin/hook") || strings.Contains(text, "reconc hook runtime "):
			hasRuntimeConfig = true
			runtimeKinds = append(runtimeKinds, target.kind)
			parts = append(parts, target.kind+": runtime hooks present")
		case strings.Contains(text, "reconc "):
			hasWarn = true
			parts = append(parts, target.kind+": hook config has no `reconc hook runtime` entry; reinstall with `reconc hook install "+target.kind+" --force`")
		default:
			parts = append(parts, target.kind+": config present, no reconc-managed hooks")
		}
	}

	if !found {
		return result
	}

	if hasRuntimeConfig && !hookRuntimeSupportProbe() {
		result.Status = doctorStatusWarn
		result.Detail = fmt.Sprintf("%s hook config(s) reference `reconc hook runtime`, but this binary is older than %s; upgrade reconc or reinstall hooks after upgrading",
			strings.Join(runtimeKinds, ", "), hooks.MinRuntimeSupportedVersion)
		return result
	}

	if hasWarn {
		result.Status = doctorStatusWarn
	}
	result.Detail = strings.Join(parts, "; ")
	return result
}
