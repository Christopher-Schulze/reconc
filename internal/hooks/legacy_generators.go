package hooks

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// legacyGeneratorsJSON records SHA-256 digests of what the hook generators of
// published releases wrote, produced by scripts/build/legacy-hook-generators.
// An upgrade recognizes that output as Reconc's own: install replaces it
// silently and uninstall removes it, while any other difference stays a user
// edit.
//
//go:embed legacy_generators.json
var legacyGeneratorsJSON []byte

type legacyGeneratorIndex struct {
	artifacts map[string]map[string]struct{}
	values    map[string]map[string]map[string]struct{}
}

// legacyGenerators parses the registry once. An unreadable registry
// recognizes nothing, which keeps the strict current-generator behavior.
var legacyGenerators = sync.OnceValue(func() legacyGeneratorIndex {
	index := legacyGeneratorIndex{artifacts: map[string]map[string]struct{}{}, values: map[string]map[string]map[string]struct{}{}}
	var document struct {
		Artifacts map[string][]string            `json:"artifacts"`
		Values    map[string]map[string][]string `json:"values"`
	}
	if json.Unmarshal(legacyGeneratorsJSON, &document) != nil {
		return index
	}
	for kind, digests := range document.Artifacts {
		index.artifacts[kind] = digestSet(digests)
	}
	for kind, keys := range document.Values {
		index.values[kind] = map[string]map[string]struct{}{}
		for key, digests := range keys {
			index.values[kind][key] = digestSet(digests)
		}
	}
	return index
})

// legacyGeneratedArtifact reports whether data is exactly an artifact a
// published release generated for kind, byte for byte or, for JSON, as the
// same document.
func legacyGeneratedArtifact(kind string, data []byte) bool {
	index := legacyGenerators()
	if _, ok := index.artifacts[kind][legacyDigest(data)]; ok {
		return true
	}
	var document interface{}
	if json.Unmarshal(data, &document) != nil {
		return false
	}
	return legacyGeneratedValue(kind, "", document)
}

// legacyGeneratedValue reports whether value equals a value a published
// release generated for kind under key: a hook event's entry, the owned
// top-level "reconc" value, or the whole document for the empty key.
func legacyGeneratedValue(kind, key string, value interface{}) bool {
	canonical, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, ok := legacyGenerators().values[kind][key][legacyDigest(canonical)]
	return ok
}

func digestSet(digests []string) map[string]struct{} {
	set := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		set[digest] = struct{}{}
	}
	return set
}

func legacyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
