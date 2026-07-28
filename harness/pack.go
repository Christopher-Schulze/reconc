// Package harness exposes the immutable public harness packs embedded in Reconc.
package harness

import (
	_ "embed"
	"fmt"

	"reconc.dev/reconc/internal/harnesspack"
)

const AdvancedTargetPrefix = "tools/reconc/harness/template"

//go:embed advanced-pack.zip
var advancedArchive []byte

func Advanced(productVersion string) (*harnesspack.Pack, error) {
	pack, err := harnesspack.LoadArchive(advancedArchive, productVersion)
	if err != nil {
		return nil, fmt.Errorf("load embedded advanced harness pack: %w", err)
	}
	return pack, nil
}
