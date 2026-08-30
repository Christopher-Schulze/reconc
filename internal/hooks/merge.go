package hooks

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/shellcommand"
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
	// Force authorizes replacement of incompatible user-owned JSON shapes.
	// Without it, the merge reports Blocked conflicts and leaves those values
	// untouched so the installer can fail before publication.
	Force bool
}

// MergeDiff describes what mergeReconcHooks did per event. Used by
// the Install layer to emit informative warnings when the merge had
// to clobber user customisations.
type MergeDiff struct {
	// Removed lists modified Reconc commands and force-authorized incompatible
	// JSON values that were replaced.
	Removed []string
	// Kept is a list of modified-reconc entries preserved because
	// KeepUserEdits was set.
	Kept []string
	// Blocked lists incompatible user-owned shapes that require explicit force.
	Blocked []string
}

func mergeReconcHooks(dest, reconcPart map[string]interface{}, opts MergeOptions) MergeDiff {
	reconcHooks, ok := reconcPart["hooks"].(map[string]interface{})
	if !ok {
		return MergeDiff{}
	}
	var diff MergeDiff
	destHooks, ok := dest["hooks"].(map[string]interface{})
	if !ok {
		if raw, exists := dest["hooks"]; exists {
			issue := "hooks: (non-object " + describeJSONType(raw) + " preserved)"
			if !opts.Force {
				return MergeDiff{Blocked: []string{issue}}
			}
			diff.Removed = append(diff.Removed, strings.Replace(issue, "preserved", "overwritten", 1))
		}
		destHooks = map[string]interface{}{}
		dest["hooks"] = destHooks
	}
	merged := mergeReconcHookMaps(destHooks, reconcHooks, opts)
	diff.Removed = append(diff.Removed, merged.Removed...)
	diff.Kept = append(diff.Kept, merged.Kept...)
	diff.Blocked = append(diff.Blocked, merged.Blocked...)
	return finalizeMergeDiff(diff)
}

func mergeReconcNestedEventHooks(dest, reconcPart map[string]interface{}, opts MergeOptions) MergeDiff {
	reconcHooks, ok := reconcPart["hooks"].(map[string]interface{})
	if !ok {
		return MergeDiff{}
	}
	reconcEvents, ok := reconcHooks["events"].(map[string]interface{})
	if !ok {
		return MergeDiff{}
	}
	var diff MergeDiff
	destHooks, ok := dest["hooks"].(map[string]interface{})
	if !ok {
		if raw, exists := dest["hooks"]; exists {
			issue := "hooks: (non-object " + describeJSONType(raw) + " preserved)"
			if !opts.Force {
				return MergeDiff{Blocked: []string{issue}}
			}
			diff.Removed = append(diff.Removed, strings.Replace(issue, "preserved", "overwritten", 1))
		}
		destHooks = map[string]interface{}{}
		dest["hooks"] = destHooks
	}
	destEvents, ok := destHooks["events"].(map[string]interface{})
	if !ok {
		if raw, exists := destHooks["events"]; exists {
			issue := "hooks.events: (non-object " + describeJSONType(raw) + " preserved)"
			if !opts.Force {
				return MergeDiff{Blocked: []string{issue}}
			}
			diff.Removed = append(diff.Removed, strings.Replace(issue, "preserved", "overwritten", 1))
		}
		destEvents = map[string]interface{}{}
		destHooks["events"] = destEvents
	}
	if _, configured := destHooks["enabled"]; !configured {
		destHooks["enabled"] = true
	}
	nested := mergeReconcNestedEventMaps(destEvents, reconcEvents, opts)
	diff.Removed = append(diff.Removed, nested.Removed...)
	diff.Kept = append(diff.Kept, nested.Kept...)
	diff.Blocked = append(diff.Blocked, nested.Blocked...)
	return finalizeMergeDiff(diff)
}

func mergeReconcNestedEventMaps(destEvents, reconcEvents map[string]interface{}, opts MergeOptions) MergeDiff {
	var diff MergeDiff
	for event, generatedRaw := range reconcEvents {
		generatedEntries, _ := generatedRaw.([]interface{})
		var existingEntries []interface{}
		shapeIssue := ""
		if existingRaw, configured := destEvents[event]; configured {
			existingEntries, shapeIssue = hookEntryArray(existingRaw)
		}
		if shapeIssue != "" {
			issue := event + ": " + shapeIssue
			if !opts.Force {
				diff.Blocked = append(diff.Blocked, issue)
				continue
			}
			diff.Removed = append(diff.Removed, strings.Replace(issue, "preserved", "overwritten", 1))
		}
		canonical := hookSignatureSet(generatedEntries)
		filtered := make([]interface{}, 0, len(existingEntries))
		for _, entry := range existingEntries {
			if containsExactHookEntry(generatedEntries, entry) {
				continue
			}
			preserved, removed, kept := filterReconcProcesses(entry, canonical, opts.KeepUserEdits)
			for _, command := range removed {
				diff.Removed = append(diff.Removed, event+": "+command)
			}
			for _, command := range kept {
				diff.Kept = append(diff.Kept, event+": "+command)
			}
			if preserved != nil {
				filtered = append(filtered, preserved)
			}
		}
		filtered = append(filtered, generatedEntries...)
		destEvents[event] = filtered
	}
	for event, existingRaw := range destEvents {
		if _, generated := reconcEvents[event]; generated {
			continue
		}
		existingEntries, ok := existingRaw.([]interface{})
		if !ok {
			continue
		}
		filtered := make([]interface{}, 0, len(existingEntries))
		for _, entry := range existingEntries {
			preserved, removed, kept := filterReconcProcesses(entry, nil, opts.KeepUserEdits)
			for _, command := range removed {
				diff.Removed = append(diff.Removed, event+": "+command)
			}
			for _, command := range kept {
				diff.Kept = append(diff.Kept, event+": "+command)
			}
			if preserved != nil {
				filtered = append(filtered, preserved)
			}
		}
		if len(filtered) == 0 {
			delete(destEvents, event)
		} else {
			destEvents[event] = filtered
		}
	}
	return finalizeMergeDiff(diff)
}

func hookEntryArray(raw interface{}) ([]interface{}, string) {
	entries, ok := raw.([]interface{})
	if !ok {
		return nil, "(non-array " + describeJSONType(raw) + " preserved)"
	}
	return entries, ""
}

// filterReconcProcesses removes canonical and replaceable Reconc commands from
// one entry while retaining foreign nested commands in their original order.
func filterReconcProcesses(entry interface{}, canonical map[string]struct{}, keepModified bool) (interface{}, []string, []string) {
	group, ok := entry.(map[string]interface{})
	if !ok {
		return entry, nil, nil
	}
	rawHooks, ok := group["hooks"].([]interface{})
	if !ok {
		signature := hookEntrySignature(entry)
		if _, exact := canonical[signature]; exact {
			return nil, nil, nil
		}
		if !reconcCommandOwned(signature) {
			return entry, nil, nil
		}
		command := hookCommandLabel(firstHookCommand([]interface{}{entry}), group["args"])
		if keepModified {
			return entry, nil, []string{command}
		}
		return nil, []string{command}, nil
	}
	foreign := make([]interface{}, 0, len(rawHooks))
	removed := []string{}
	kept := []string{}
	for _, raw := range rawHooks {
		hook, hookOK := raw.(map[string]interface{})
		if !hookOK {
			foreign = append(foreign, raw)
			continue
		}
		hookCommand, _ := hook["command"].(string)
		signature := commandSignature(hookCommand, hook["args"])
		if _, exact := canonical[signature]; exact {
			continue
		}
		if !reconcCommandOwned(signature) {
			foreign = append(foreign, raw)
			continue
		}
		command := hookCommandLabel(hookCommand, hook["args"])
		if keepModified {
			foreign = append(foreign, raw)
			kept = append(kept, command)
		} else {
			removed = append(removed, command)
		}
	}
	if len(foreign) == len(rawHooks) && len(kept) == 0 {
		return entry, nil, nil
	}
	if len(foreign) == 0 {
		return nil, removed, kept
	}
	preserved := make(map[string]interface{}, len(group))
	for key, value := range group {
		preserved[key] = value
	}
	preserved["hooks"] = foreign
	return preserved, removed, kept
}

func hookCommandLabel(command string, argsValue interface{}) string {
	parts := []string{strings.TrimSpace(command)}
	if args, ok := argsValue.([]interface{}); ok {
		for _, raw := range args {
			parts = append(parts, fmt.Sprint(raw))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func mergeReconcHookMaps(destHooks, reconcHooks map[string]interface{}, opts MergeOptions) MergeDiff {
	var diff MergeDiff

	for event, newEntriesRaw := range reconcHooks {
		newEntries, _ := newEntriesRaw.([]interface{})

		var existingEntries []interface{}
		if raw, ok := destHooks[event]; ok {
			arr, isArr := raw.([]interface{})
			if !isArr {
				issue := event + ": (non-array " + describeJSONType(raw) + " preserved)"
				if !opts.Force {
					diff.Blocked = append(diff.Blocked, issue)
					continue
				}
				diff.Removed = append(diff.Removed, strings.Replace(issue, "preserved", "overwritten", 1))
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
			if containsExactHookEntry(newEntries, e) {
				continue
			}
			preserved, removed, kept := filterReconcProcesses(e, canonical, opts.KeepUserEdits)
			for _, command := range removed {
				diff.Removed = append(diff.Removed, event+": "+command)
			}
			for _, command := range kept {
				diff.Kept = append(diff.Kept, event+": "+command)
			}
			if preserved != nil {
				filtered = append(filtered, preserved)
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
			preserved, removed, kept := filterReconcProcesses(e, nil, opts.KeepUserEdits)
			for _, command := range removed {
				diff.Removed = append(diff.Removed, event+": "+command)
			}
			for _, command := range kept {
				diff.Kept = append(diff.Kept, event+": "+command)
			}
			if preserved != nil {
				filtered = append(filtered, preserved)
			}
		}
		if len(filtered) == 0 {
			delete(destHooks, event)
			continue
		}
		destHooks[event] = filtered
	}
	return finalizeMergeDiff(diff)
}

// finalizeMergeDiff gives both report slices a stable order. They are built by
// ranging over hook-event maps, so without this the same reinstall emits its
// warnings in a different order on every run.
func finalizeMergeDiff(diff MergeDiff) MergeDiff {
	sort.Strings(diff.Removed)
	sort.Strings(diff.Kept)
	sort.Strings(diff.Blocked)
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
	removed, err := removeCanonicalReconcHookMaps(destHooks, reconcHooks)
	if err != nil {
		return 0, err
	}
	if len(destHooks) == 0 {
		delete(dest, "hooks")
	}
	return removed, nil
}

func removeCanonicalReconcNestedEventHooks(dest, reconcPart map[string]interface{}) (int, error) {
	reconcHooks, ok := reconcPart["hooks"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("generated hook settings are missing")
	}
	reconcEvents, ok := reconcHooks["events"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("generated hook event map is missing")
	}
	destHooks, ok := dest["hooks"].(map[string]interface{})
	if !ok {
		return 0, nil
	}
	destEvents, ok := destHooks["events"].(map[string]interface{})
	if !ok {
		return 0, nil
	}
	removed, err := removeCanonicalReconcHookMaps(destEvents, reconcEvents)
	if err != nil {
		return 0, err
	}
	if len(destEvents) == 0 {
		delete(destHooks, "events")
	}
	if len(destHooks) == 1 && destHooks["enabled"] == true {
		delete(dest, "hooks")
	}
	return removed, nil
}

func removeCanonicalReconcHookMaps(destHooks, reconcHooks map[string]interface{}) (int, error) {
	removed := 0
	for event, existingRaw := range destHooks {
		existingEntries, ok := existingRaw.([]interface{})
		if !ok {
			continue
		}
		canonical := map[string]struct{}{}
		var generatedEntries []interface{}
		if generatedRaw, exists := reconcHooks[event]; exists {
			generatedEntries, _ = generatedRaw.([]interface{})
			canonical = hookSignatureSet(generatedEntries)
		}
		filtered := make([]interface{}, 0, len(existingEntries))
		for _, entry := range existingEntries {
			switch classifyHookEntry(entry, canonical) {
			case NonReconc:
				filtered = append(filtered, entry)
			case CanonicalReconc:
				if !containsExactHookEntry(generatedEntries, entry) {
					return 0, fmt.Errorf("event %s contains a modified Reconc entry; refusing removal", event)
				}
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
	return removed, nil
}

func containsExactHookEntry(entries []interface{}, candidate interface{}) bool {
	for _, entry := range entries {
		if reflect.DeepEqual(entry, candidate) {
			return true
		}
	}
	return false
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
		for _, signature := range hookEntrySignatures(entry) {
			set[signature] = struct{}{}
		}
	}
	return set
}

func hookEntrySignatures(entry interface{}) []string {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return nil
	}
	if cmd, _ := m["command"].(string); strings.TrimSpace(cmd) != "" {
		if signature := commandSignature(cmd, m["args"]); signature != "" {
			return []string{signature}
		}
		return nil
	}
	hookList, _ := m["hooks"].([]interface{})
	signatures := make([]string, 0, len(hookList))
	for _, raw := range hookList {
		hook, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		command, _ := hook["command"].(string)
		if signature := commandSignature(command, hook["args"]); signature != "" {
			signatures = append(signatures, signature)
		}
	}
	return signatures
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
	command = strings.TrimSpace(command)
	if strings.ContainsRune(command, '\x00') {
		return ""
	}
	parts := []string{command}
	if args, ok := argsValue.([]interface{}); ok {
		for _, raw := range args {
			var part string
			if s, ok := raw.(string); ok {
				part = s
			} else {
				part = fmt.Sprintf("%v", raw)
			}
			if strings.ContainsRune(part, '\x00') {
				return ""
			}
			parts = append(parts, part)
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

// classifyHookEntry returns a conservative whole-entry classification for
// strict removal decisions. Install merging uses filterReconcProcesses to
// classify every nested command independently and preserve foreign siblings.
func classifyHookEntry(entry interface{}, canonicalSignatures map[string]struct{}) HookEntryClass {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return NonReconc
	}
	if _, ok := canonicalSignatures[hookEntrySignature(entry)]; ok {
		return CanonicalReconc
	}
	// Generated hooks may call a repo-local binary directly or wrap the
	// runtime invocation in a shell resolver so it works without PATH. We
	// still classify both shapes as reconc-owned. Inspect every nested hook:
	// prepending a foreign process to a modified Reconc group must not hide
	// the managed invocation from strict uninstall or reinstall handling.
	if !hookEntryContainsReconcInvocation(m) {
		return NonReconc
	}
	return ModifiedReconc
}

func hookEntryContainsReconcInvocation(entry map[string]interface{}) bool {
	if command, _ := entry["command"].(string); reconcCommandOwned(commandSignature(command, entry["args"])) {
		return true
	}
	hooks, _ := entry["hooks"].([]interface{})
	for _, raw := range hooks {
		hook, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		command, _ := hook["command"].(string)
		if reconcCommandOwned(commandSignature(command, hook["args"])) {
			return true
		}
	}
	return false
}

// shellOwnershipDepth bounds the nested shell analysis used to locate
// executable positions inside a hook command.
const shellOwnershipDepth = 8

// reconcCommandOwned reports whether a hook entry executes a Reconc hook
// wrapper. The signature is the command word followed by its literal argv,
// separated by NUL.
//
// Ownership is decided on executable positions, not on the wrapper path
// appearing anywhere in the text. A user hook that merely names
// tools/reconc/bin/hook in an argument, a message, or an echo would otherwise
// be classified as Reconc-owned and dropped on the next install.
//
// Shell strings whose executable positions cannot be enumerated remain
// user-owned. The variable-dispatched generated resolver is the sole exception:
// it is accepted only when its complete template reconstructs byte-for-byte.
// Marker text alone is never enough to grant replacement rights.
func reconcCommandOwned(signature string) bool {
	if signature == "" {
		return false
	}
	words := strings.Split(signature, "\x00")
	if len(words) > 1 {
		// Exec form: the command word is the executable and the rest is
		// literal argv, so no shell analysis is needed or valid.
		return reconcInvocationWords(words)
	}
	if !reconcSignatureNamesWrapper(signature) {
		return false
	}
	if generatedRuntimeShellCommandOwned(words[0]) || legacyRepoWrapperCommandOwned(words[0]) {
		return true
	}
	invocations, complete := shellcommand.Invocations(words[0], shellOwnershipDepth)
	if !complete {
		return false
	}
	for _, invocation := range invocations {
		if reconcInvocationWords(invocation.Words) {
			return true
		}
	}
	return false
}

// generatedRuntimeShellCommandOwned accepts only the byte-exact current
// resolver template (plus its former login-shell launcher). The resolver uses
// variables in executable positions, so generic static shell analysis cannot
// prove it; reconstructing the whole generated command keeps marker mentions
// and arbitrary dynamic commands outside the ownership boundary.
func generatedRuntimeShellCommandOwned(command string) bool {
	body := command
	launcher := ""
	for _, candidate := range []string{"sh -c '", "sh -lc '"} {
		if strings.HasPrefix(command, candidate) && strings.HasSuffix(command, "'") {
			launcher = candidate
			body = strings.TrimSuffix(strings.TrimPrefix(command, candidate), "'")
			break
		}
	}
	if launcher == "" && strings.HasPrefix(command, "sh -") {
		return false
	}
	const repoPrefix = `repo="`
	const repoSuffix = `"; hook="$repo/tools/reconc/bin/hook"; if [ -x "$hook" ]; then exec "$hook" `
	if !strings.HasPrefix(body, repoPrefix) {
		return false
	}
	repoEnd := strings.Index(strings.TrimPrefix(body, repoPrefix), repoSuffix)
	if repoEnd < 0 {
		return false
	}
	repo := strings.TrimPrefix(body, repoPrefix)[:repoEnd]
	eventStart := len(repoPrefix) + repoEnd + len(repoSuffix)
	const eventSuffix = ` "$repo"; fi;`
	eventEnd := strings.Index(body[eventStart:], eventSuffix)
	if eventEnd < 0 {
		return false
	}
	event := body[eventStart : eventStart+eventEnd]
	if !validRuntimeRoute(event) || body != shellRuntimeCommand(repo, event) {
		return false
	}
	if launcher == "" {
		return true
	}
	return command == launcher+body+"'"
}

// legacyRepoWrapperCommandOwned recognises the complete resolver shape used by
// early Antigravity artifacts. It deliberately does not accept a path fragment
// or a wrapper mention in an argument.
func legacyRepoWrapperCommandOwned(command string) bool {
	for _, launcher := range []string{"sh -c '", "sh -lc '"} {
		if !strings.HasPrefix(command, launcher) || !strings.HasSuffix(command, "'") {
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(command, launcher), "'")
		const prefix = `repo="`
		const middle = `"; exec "$repo/tools/reconc/bin/hook" `
		if !strings.HasPrefix(body, prefix) {
			continue
		}
		repoEnd := strings.Index(strings.TrimPrefix(body, prefix), middle)
		if repoEnd < 0 {
			continue
		}
		repo := strings.TrimPrefix(body, prefix)[:repoEnd]
		eventStart := len(prefix) + repoEnd + len(middle)
		const suffix = ` "$repo"`
		if !strings.HasSuffix(body[eventStart:], suffix) {
			continue
		}
		event := strings.TrimSuffix(body[eventStart:], suffix)
		if validRuntimeRoute(event) && body == fmt.Sprintf(`repo="%s"; exec "$repo/tools/reconc/bin/hook" %s "$repo"`, repo, event) {
			return true
		}
	}
	return false
}

func validRuntimeRoute(route string) bool {
	if route == "" {
		return false
	}
	for _, char := range route {
		if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// reconcSignatureNamesWrapper is the shell-form text prefilter. Exec-form
// signatures bypass it and are classified from their parsed argv.
func reconcSignatureNamesWrapper(signature string) bool {
	return strings.Contains(signature, "reconc hook runtime ") ||
		(strings.Contains(signature, "tools/reconc/dist/reconc-") && strings.Contains(signature, " hook runtime ")) ||
		strings.Contains(signature, "tools/reconc/bin/hook")
}

func reconcInvocationWords(words []string) bool {
	words = interpretedInvocationWords(words)
	if len(words) == 0 {
		return false
	}
	executable := strings.ReplaceAll(words[0], `\`, "/")
	// Any executable inside the managed wrapper directory is Reconc's, including
	// a renamed wrapper: that is a modified Reconc entry the install and
	// uninstall paths must refuse, not a foreign hook they may ignore.
	if strings.Contains(executable, "tools/reconc/bin/") {
		return true
	}
	if len(words) < 3 || words[1] != "hook" || words[2] != "runtime" {
		return false
	}
	return commandBaseName(executable) == "reconc" ||
		strings.Contains(executable, "tools/reconc/dist/reconc-")
}

// interpretedInvocationWords resolves the program a shell interpreter runs on
// behalf of an exec-form entry. ZCode installs its wrapper as `sh
// tools/reconc/bin/hook <route> .`, so the executable position that matters is
// the script argument, not the interpreter.
func interpretedInvocationWords(words []string) []string {
	if len(words) < 2 || strings.HasPrefix(words[1], "-") {
		return words
	}
	switch commandBaseName(strings.ReplaceAll(words[0], `\`, "/")) {
	case "sh", "bash", "dash", "zsh", "ksh":
		return words[1:]
	default:
		return words
	}
}

func commandBaseName(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}
