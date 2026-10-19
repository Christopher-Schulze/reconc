package retention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
)

// sessionStateOwners lists the session ids whose state file exists. A state
// file references its full-command text objects and its evidence segment
// directory, so those artifacts are removed only together with it. A false
// result means ownership is unknown and every dependent must stay protected.
func sessionStateOwners(project string, report *Report) (map[string]bool, bool) {
	dir := filepath.Join(project, "sessions")
	entries, err := boundedio.ReadDirNoSymlink(dir, maxRetentionDirectoryEntries)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, true
	}
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("read session owners %s: %v", dir, err))
		return nil, false
	}
	return sessionStateOwnersFromEntries(entries), true
}

func sessionStateOwnersFromEntries(entries []os.DirEntry) map[string]bool {
	owners := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() && validSessionArtifactName(entry.Name()) {
			owners[strings.TrimSuffix(entry.Name(), ".json")] = true
		}
	}
	return owners
}

func sessionStateID(name string) (string, bool) {
	if !validSessionArtifactName(name) {
		return "", false
	}
	return strings.TrimSuffix(name, ".json"), true
}

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
