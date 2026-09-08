package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"reconc.dev/reconc/internal/templates"
)

// ValidateTemplateProvenance binds the embedded dependency list to source_digest.
// Freshness callers must invoke it even when the list is absent: removing the
// list and recomputing lock_digest must never disable warm template checks.
func ValidateTemplateProvenance(payload map[string]interface{}) error {
	sources, ok := payload["sources"].([]interface{})
	if !ok {
		return fmt.Errorf("compiled source provenance must contain a list")
	}
	var dependencies []templates.Dependency
	if raw, present := payload["template_dependencies"]; present {
		data, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("encode template dependencies: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&dependencies); err != nil {
			return fmt.Errorf("decode template dependencies: %w", err)
		}
		if len(dependencies) == 0 {
			return fmt.Errorf("compiled template dependencies must contain a non-empty list")
		}
	}
	digest, err := computeSourceDigestWithTemplates(sources, dependencies)
	if err != nil {
		return err
	}
	if stored, _ := payload["source_digest"].(string); stored != digest {
		return fmt.Errorf("compiled template provenance does not match source_digest; refresh required")
	}
	return nil
}
