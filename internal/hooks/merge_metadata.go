package hooks

import "reflect"

func filterReconcEntry(entry interface{}, canonical map[string]struct{}, generated []interface{}, keepModified bool) (interface{}, []string, []string) {
	changed := canonicalHookMetadataChanged(entry, canonical, generated)
	label := "Reconc hook settings differ from the generated entry"
	if changed && keepModified {
		return entry, nil, []string{label}
	}
	preserved, removed, kept := filterReconcProcesses(entry, canonical, keepModified)
	if changed {
		removed = append(removed, label)
	}
	return preserved, removed, kept
}

func canonicalHookMetadataChanged(entry interface{}, canonical map[string]struct{}, generated []interface{}) bool {
	group, ok := entry.(map[string]interface{})
	if !ok {
		return false
	}
	hooks, nested := group["hooks"].([]interface{})
	if !nested {
		_, owned := canonical[hookEntrySignature(entry)]
		return owned && !containsExactHookEntry(generated, entry)
	}
	foreign := false
	for _, raw := range hooks {
		foreign = foreign || !reconcCommandOwned(hookEntrySignature(raw))
	}
	for _, raw := range hooks {
		if _, owned := canonical[hookEntrySignature(raw)]; !owned {
			continue
		}
		if !generatedProcessMetadataMatches(group, raw, generated, foreign) {
			return true
		}
	}
	return false
}

func generatedProcessMetadataMatches(group map[string]interface{}, process interface{}, generated []interface{}, groupPreserved bool) bool {
	for _, raw := range generated {
		expected, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		hooks, ok := expected["hooks"].([]interface{})
		if ok && containsExactHookEntry(hooks, process) &&
			(groupPreserved || reflect.DeepEqual(hookGroupMetadata(group), hookGroupMetadata(expected))) {
			return true
		}
	}
	return false
}

func hookGroupMetadata(group map[string]interface{}) map[string]interface{} {
	metadata := make(map[string]interface{}, len(group))
	for key, value := range group {
		if key != "hooks" {
			metadata[key] = value
		}
	}
	return metadata
}
