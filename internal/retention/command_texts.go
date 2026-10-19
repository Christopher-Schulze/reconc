package retention

import (
	"path/filepath"
	"strings"
)

func commandTextSessionID(name string) (string, bool) {
	id, digestName, present := strings.Cut(name, ".command-")
	if !present || !validSessionArtifactName(id+".json") || !strings.HasSuffix(digestName, ".txt") {
		return "", false
	}
	_, valid := lowercaseDigestFileStem(strings.TrimSuffix(digestName, ".txt") + ".json")
	return id, valid
}

func protectCommandText(item *candidate, project string, protected map[string]bool, protectAll bool) {
	id, ok := commandTextSessionID(filepath.Base(item.path))
	if !ok {
		return
	}
	item.active = item.active || protectAll || protected[id]
	item.leasePath = filepath.Join(project, "locks", id+".lock")
}

func protectedSessionNames(ids map[string]bool) map[string]bool {
	names := make(map[string]bool, len(ids))
	for id, protected := range ids {
		names[id+".json"] = protected
	}
	return names
}
