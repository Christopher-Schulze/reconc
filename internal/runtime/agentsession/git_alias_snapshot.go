package agentsession

import (
	"bytes"
	"strings"
)

// gitAliasSnapshot is one bounded, hermetic view of the repository's literal
// Git aliases. The map is immutable after capture; shell analysis receives a
// defensive working copy because same-command git config operations are
// allowed to update the analysis-local view.
type gitAliasSnapshot struct {
	aliases  map[string]gitAlias
	identity string
	complete bool
}

func captureGitAliasSnapshot(repoRoot string) gitAliasSnapshot {
	body, exitCode, err := runGitInspection(repoRoot, "config", "--null", "--get-regexp", "^alias\\.")
	if err != nil || exitCode != 0 && exitCode != 1 {
		return gitAliasSnapshot{}
	}
	aliases, ok := parseGitAliasSnapshot(body)
	if !ok {
		return gitAliasSnapshot{}
	}
	return gitAliasSnapshot{
		aliases:  aliases,
		identity: hashBytes(body),
		complete: true,
	}
}

func (snapshot gitAliasSnapshot) identityValue() (string, bool) {
	if !snapshot.complete {
		return "", false
	}
	return snapshot.identity, true
}

func (snapshot gitAliasSnapshot) workingAliases() map[string]gitAlias {
	return cloneGitAliases(snapshot.aliases)
}

func parseGitAliasSnapshot(body []byte) (map[string]gitAlias, bool) {
	aliases := make(map[string]gitAlias)
	if len(body) == 0 {
		return aliases, true
	}
	records := bytes.Split(body, []byte{0})
	if len(records) == 0 || len(records[len(records)-1]) != 0 {
		return nil, false
	}
	for _, record := range records[:len(records)-1] {
		if len(record) == 0 {
			return nil, false
		}
		key, value, found := strings.Cut(string(record), "\n")
		if !found {
			return nil, false
		}
		name, found := parseGitAliasName(key)
		if !found {
			return nil, false
		}
		aliases[name] = gitAlias{value: value}
	}
	return aliases, true
}
