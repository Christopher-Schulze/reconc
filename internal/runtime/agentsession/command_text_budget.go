package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
)

// Include old and orphaned objects in the bound: active-session retention may
// preserve them, so repeated post-tool events must not grow disk without limit.
func checkCommandTextBudget(state SessionState) error {
	directory := filepath.Dir(sessionStatePath(state.RepoRoot, state.SessionID))
	entries, err := boundedio.ReadDirNoSymlink(directory, 4096)
	if err != nil {
		return fmt.Errorf("inspect complete command storage: %w", err)
	}
	prefix := sessionFileKey(state.SessionID) + ".command-"
	existing := make(map[string]struct{})
	remaining := int64(maxCompleteEvidenceBytes)
	for _, entry := range entries {
		digest, present := strings.CutPrefix(entry.Name(), prefix)
		if !present || !strings.HasSuffix(digest, ".txt") || !evidenceHexIdentity(strings.TrimSuffix(digest, ".txt"), 64) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect complete command object: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("complete command object is not a regular file")
		}
		remaining -= info.Size()
		existing[entry.Name()] = struct{}{}
	}
	for reference, command := range state.commandTexts {
		digest, valid := commandTextDigest(reference)
		if !valid || !commandReferenceRetained(state, reference) {
			continue
		}
		if _, present := existing[filepath.Base(commandTextPath(state, digest))]; !present {
			remaining -= int64(len(command))
		}
	}
	if remaining < 0 {
		return errCompleteCommandCapacity
	}
	return nil
}
