// Package harness exposes the immutable public harness packs embedded in Reconc.
package harness

import (
	_ "embed"
	"fmt"
	"sync"

	"reconc.dev/reconc/internal/harnesspack"
)

const AdvancedTargetPrefix = "tools/reconc/harness/template"

//go:embed advanced-pack.zip
var advancedArchive []byte

var advancedCache struct {
	sync.Mutex
	productVersion string
	pack           *harnesspack.Pack
}

func Advanced(productVersion string) (*harnesspack.Pack, error) {
	advancedCache.Lock()
	if advancedCache.pack != nil && advancedCache.productVersion == productVersion {
		pack := advancedCache.pack
		advancedCache.Unlock()
		return clonePack(pack), nil
	}
	pack, err := harnesspack.LoadArchive(advancedArchive, productVersion)
	if err != nil {
		advancedCache.Unlock()
		return nil, fmt.Errorf("load embedded advanced harness pack: %w", err)
	}
	advancedCache.productVersion = productVersion
	advancedCache.pack = pack
	advancedCache.Unlock()
	return clonePack(pack), nil
}

func clonePack(pack *harnesspack.Pack) *harnesspack.Pack {
	clone := &harnesspack.Pack{Manifest: pack.Manifest, Files: make([]harnesspack.Data, len(pack.Files))}
	clone.Manifest.Capabilities = append([]string(nil), pack.Manifest.Capabilities...)
	clone.Manifest.Files = append([]harnesspack.File(nil), pack.Manifest.Files...)
	for index, file := range pack.Files {
		clone.Files[index] = harnesspack.Data{File: file.File, Body: append([]byte(nil), file.Body...)}
	}
	return clone
}
