package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// ContentIdentity names exact schema bytes independently of product releases.
// Exact JSON string occurrences of the root $id are normalized to an empty
// string before hashing, including a self-identifying $schema const. All other
// bytes, including references to other schemas and formatting, remain bound.
func ContentIdentity(artifact Artifact, version string, body []byte) (string, error) {
	if !validArtifact(artifact) || !canonicalVersion(version) {
		return "", fmt.Errorf("invalid artifact or schema version")
	}
	var document struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return "", fmt.Errorf("decode schema identity: %w", err)
	}
	if document.ID == "" {
		return "", fmt.Errorf("schema $id is empty")
	}
	encoded, err := json.Marshal(document.ID)
	if err != nil {
		return "", fmt.Errorf("encode schema identity: %w", err)
	}
	if !bytes.Contains(body, encoded) {
		return "", fmt.Errorf("schema $id must use canonical JSON string encoding")
	}
	normalized := bytes.ReplaceAll(body, encoded, []byte(`""`))
	return fmt.Sprintf("urn:reconc:schema:%s:v%s:sha256:%x", artifact, version, sha256.Sum256(normalized)), nil
}

func validContentIdentity(contract Contract) bool {
	prefix := "urn:reconc:schema:" + string(contract.Artifact) + ":v" + contract.SchemaVersion + ":sha256:"
	return strings.HasPrefix(contract.DefaultURL, prefix) &&
		validRegistryDigest(strings.TrimPrefix(contract.DefaultURL, prefix))
}

// PublicationURL locates schema bytes in a release without changing their $id.
// Historical identities keep their original publication; content identities
// require the explicitly selected release tag.
func PublicationURL(contract Contract, releaseTag string) (string, error) {
	if contract.IntroductionTag != "" {
		return contract.DefaultURL, nil
	}
	if !validContentIdentity(contract) || !validReleaseTag(releaseTag) {
		return "", fmt.Errorf("content-addressed schema publication requires an explicit stable release tag")
	}
	return taggedSchemaURL(releaseTag, contract.LocalPath), nil
}
