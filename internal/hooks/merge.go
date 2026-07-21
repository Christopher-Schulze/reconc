package hooks

import (
	"fmt"
	"strings"
)

// mergeReconcHooks merges reconcPart['hooks'] into dest['hooks'].
// For each current event key (SessionStart, PreToolUse, etc.), removes
// existing reconc-owned hook entries (runtime commands that call reconc)
// and appends the current generator's entries. For stale event keys that
// no longer exist in the generator, reconc-owned entries are removed while
// user-owned entries are preserved. Non-hook keys in dest are untouched.
// Non-reconc hook entries that the user may have added by hand are
// preserved.
// MergeOptions controls the hook-config merge behaviour. It distinguishes
// canonical reconc entries from user-modified reconc entries.
type MergeOptions struct {
	// KeepUserEdits preserves ModifiedReconc entries (entries whose
	// command contains a reconc runtime invocation but doesn't match
	// the generator's current canonical string). Default false --
	// the merge drops them but reports them via Removed so the
	// caller can surface a stderr warning.
	KeepUserEdits bool
}

// MergeDiff describes what mergeReconcHooks did per event. Used by
// the Install layer to emit informative warnings when the merge had
// to clobber user customisations.
type MergeDiff struct {
	// Removed is a list of "event:command" strings that were classified
	// as ModifiedReconc and dropped (unless KeepUserEdits is true).
	Removed []string
	// Kept is a list of modified-reconc entries preserved because
	// KeepUserEdits was set.
	Kept []string
}

func mergeReconcHooks(dest, reconcPart map[string]interface{}, opts MergeOptions) MergeDiff {
	var diff MergeDiff
	reconcHooks, ok := reconcPart["hooks"].(map[string]interface{})
	if !ok {
		return diff
	}
	destHooks, ok := dest["hooks"].(map[string]interface{})
	if !ok {
		destHooks = map[string]interface{}{}
		dest["hooks"] = destHooks
	}

	for event, newEntriesRaw := range reconcHooks {
		newEntries, _ := newEntriesRaw.([]interface{})

		// Validate the destination event's type before treating it as an
		// array. If the user hand-edited their
		// settings into a non-array shape (e.g. wrapped in an object
		// by mistake), we MUST NOT silently replace it -- surface the
		// event and its observed type via the MergeDiff so the caller
		// can warn. Currently we still replace it (otherwise the
		// install does nothing), but the warning makes the behaviour
		// visible.
		var existingEntries []interface{}
		if raw, ok := destHooks[event]; ok && raw != nil {
			arr, isArr := raw.([]interface{})
			if !isArr {
				diff.Removed = append(diff.Removed,
					event+": (non-array "+describeJSONType(raw)+" overwritten)")
			} else {
				existingEntries = arr
			}
		}

		// Build the per-event canonical signature SET for classification.
		// We include args because Claude Code exec-form hooks use the same
		// command path for every event and distinguish routes by argv, and
		// an event may carry several canonical reconc entries (SessionStart
		// runs session-start plus compaction-recovery): comparing against
		// only the first signature misclassifies every further canonical
		// entry as user-modified and emits a false replacement warning.
		canonical := hookSignatureSet(newEntries)

		filtered := make([]interface{}, 0, len(existingEntries))
		for _, e := range existingEntries {
			switch classifyHookEntry(e, canonical) {
			case NonReconc:
				filtered = append(filtered, e)
			case CanonicalReconc:
				// Drop silently; about to be re-added from newEntries.
			case ModifiedReconc:
				cmd := firstHookCommand([]interface{}{e})
				if opts.KeepUserEdits {
					filtered = append(filtered, e)
					diff.Kept = append(diff.Kept, event+": "+cmd)
				} else {
					diff.Removed = append(diff.Removed, event+": "+cmd)
				}
			}
		}
		filtered = append(filtered, newEntries...)
		destHooks[event] = filtered
	}
	for event, existingRaw := range destHooks {
		if _, stillGenerated := reconcHooks[event]; stillGenerated {
			continue
		}
		existingEntries, ok := existingRaw.([]interface{})
		if !ok {
			continue
		}
		filtered := make([]interface{}, 0, len(existingEntries))
		for _, e := range existingEntries {
			switch classifyHookEntry(e, nil) {
			case NonReconc:
				filtered = append(filtered, e)
			case CanonicalReconc, ModifiedReconc:
				cmd := firstHookCommand([]interface{}{e})
				if opts.KeepUserEdits {
					filtered = append(filtered, e)
					diff.Kept = append(diff.Kept, event+": "+cmd)
				} else {
					diff.Removed = append(diff.Removed, event+": "+cmd)
				}
			}
		}
		if len(filtered) == 0 {
			delete(destHooks, event)
			continue
		}
		destHooks[event] = filtered
	}
	return diff
}

// removeCanonicalReconcHooks strips only entries that exactly match the
// current generator. Reconc-looking but modified entries are ambiguous and
// fail closed before any caller writes the resulting document.
func removeCanonicalReconcHooks(dest, reconcPart map[string]interface{}) (int, error) {
	reconcHooks, ok := reconcPart["hooks"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("generated hook map is missing")
	}
	destHooks, ok := dest["hooks"].(map[string]interface{})
	if !ok {
		return 0, nil
	}
	removed := 0
	for event, existingRaw := range destHooks {
		existingEntries, ok := existingRaw.([]interface{})
		if !ok {
			continue
		}
		canonical := map[string]struct{}{}
		if generatedRaw, exists := reconcHooks[event]; exists {
			generatedEntries, _ := generatedRaw.([]interface{})
			canonical = hookSignatureSet(generatedEntries)
		}
		filtered := make([]interface{}, 0, len(existingEntries))
		for _, entry := range existingEntries {
			switch classifyHookEntry(entry, canonical) {
			case NonReconc:
				filtered = append(filtered, entry)
			case CanonicalReconc:
				removed++
			case ModifiedReconc:
				return 0, fmt.Errorf("event %s contains a modified Reconc entry; refusing removal", event)
			}
		}
		if len(filtered) == 0 {
			delete(destHooks, event)
		} else {
			destHooks[event] = filtered
		}
	}
	if len(destHooks) == 0 {
		delete(dest, "hooks")
	}
	return removed, nil
}

// describeJSONType returns a human-readable label for a
// json.Unmarshal'd value's concrete Go type, mapped to the JSON
// vocabulary users actually recognise. Used in merge warnings so
// "your hooks.SessionStart is an object" lands better than
// "your hooks.SessionStart is a map[string]interface {}".
func describeJSONType(v interface{}) string {
	switch v.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "unknown"
}

// firstHookCommand returns the command string of the first direct
// command or hooks[0].command in the given entries list, or "" if absent.
// Helper for classifier + diff reporting.
func firstHookCommand(entries []interface{}) string {
	if len(entries) == 0 {
		return ""
	}
	m, ok := entries[0].(map[string]interface{})
	if !ok {
		return ""
	}
	if cmd, _ := m["command"].(string); strings.TrimSpace(cmd) != "" {
		return strings.TrimSpace(cmd)
	}
	hookList, ok := m["hooks"].([]interface{})
	if !ok || len(hookList) == 0 {
		return ""
	}
	hm, ok := hookList[0].(map[string]interface{})
	if !ok {
		return ""
	}
	cmd, _ := hm["command"].(string)
	return strings.TrimSpace(cmd)
}

// hookSignatureSet collects the canonical signature of every generated entry
// for one event, so classification recognizes each of them instead of only
// the first.
func hookSignatureSet(entries []interface{}) map[string]struct{} {
	set := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if signature := hookEntrySignature(entry); signature != "" {
			set[signature] = struct{}{}
		}
	}
	return set
}

func hookEntrySignature(entry interface{}) string {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return ""
	}
	if cmd, _ := m["command"].(string); strings.TrimSpace(cmd) != "" {
		return commandSignature(cmd, m["args"])
	}
	hookList, ok := m["hooks"].([]interface{})
	if !ok || len(hookList) == 0 {
		return ""
	}
	hm, ok := hookList[0].(map[string]interface{})
	if !ok {
		return ""
	}
	cmd, _ := hm["command"].(string)
	return commandSignature(cmd, hm["args"])
}

func commandSignature(command string, argsValue interface{}) string {
	parts := []string{strings.TrimSpace(command)}
	if args, ok := argsValue.([]interface{}); ok {
		for _, raw := range args {
			if s, ok := raw.(string); ok {
				parts = append(parts, s)
			} else {
				parts = append(parts, fmt.Sprintf("%v", raw))
			}
		}
	}
	return strings.Join(parts, "\x00")
}

// HookEntryClass classifies a hooks array entry in a JSON settings
// file so the merge logic can treat canonical reconc entries,
// user-edited reconc entries, and unrelated user entries differently.
type HookEntryClass int

const (
	// NonReconc is any entry that does not reference reconc runtime.
	// Preserved on install.
	NonReconc HookEntryClass = iota
	// CanonicalReconc is a reconc-owned entry whose command matches
	// the generator's current canonical form. Replaced silently on
	// install (idempotent).
	CanonicalReconc
	// ModifiedReconc is a reconc-owned entry (command contains
	// a reconc runtime invocation) but differs from the canonical form --
	// likely the user hand-edited it. Replaced on install by default,
	// preserved when --keep-user-edits is set.
	ModifiedReconc
)

// classifyHookEntry returns the classification for a single entry in
// a hooks.<event> array. Looks at Cursor-style direct `command` first,
// then Claude/Codex-style `hooks[0].command`; the generator never emits
// multi-hook entries so this is the correct granularity.
func classifyHookEntry(entry interface{}, canonicalSignatures map[string]struct{}) HookEntryClass {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return NonReconc
	}
	cmd, _ := m["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		hookList, ok := m["hooks"].([]interface{})
		if !ok || len(hookList) == 0 {
			return NonReconc
		}
		hm, ok := hookList[0].(map[string]interface{})
		if !ok {
			return NonReconc
		}
		cmd, _ = hm["command"].(string)
	}
	// Generated hooks may call a repo-local binary directly or wrap the
	// runtime invocation in a shell resolver so it works without PATH. We
	// still classify both shapes as reconc-owned.
	trimmed := strings.TrimSpace(cmd)
	if !strings.Contains(trimmed, "reconc hook runtime ") &&
		!(strings.Contains(trimmed, "tools/reconc/dist/reconc-") && strings.Contains(trimmed, " hook runtime ")) &&
		!strings.Contains(trimmed, "tools/reconc/bin/hook") {
		return NonReconc
	}
	if _, ok := canonicalSignatures[hookEntrySignature(entry)]; ok {
		return CanonicalReconc
	}
	return ModifiedReconc
}
