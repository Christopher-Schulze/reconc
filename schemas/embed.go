// Package schemas embeds the published policy-authoring schemas needed by the
// offline CLI. The JSON files remain the canonical source and release assets.
package schemas

import (
	"embed"
	"fmt"
)

//go:embed v2/policy-config.schema.json v4/policy-config.schema.json
var policyConfigFiles embed.FS

// PolicyConfig returns a detached published policy-config schema document.
func PolicyConfig(version string) ([]byte, error) {
	path := "v" + version + "/policy-config.schema.json"
	if version != "2" && version != "4" {
		return nil, fmt.Errorf("embedded policy-config schema v%s is unavailable", version)
	}
	return policyConfigFiles.ReadFile(path)
}
