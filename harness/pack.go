// Package harness exposes the immutable public harness packs embedded in Reconc.
package harness

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"reconc.dev/reconc/internal/harnesspack"
	"reconc.dev/reconc/internal/semver"
)

const AdvancedTargetPrefix = "tools/reconc/harness/template"

//go:embed advanced-pack.zip
var advancedArchive []byte

var developmentIdentity = regexp.MustCompile(`^dev(?:\+[0-9a-f]{12})?(?:-dirty)?$`)

var advancedCache struct {
	sync.Mutex
	productVersion string
	pack           *harnesspack.Pack
}

func Advanced(productVersion string) (*harnesspack.Pack, error) {
	if !developmentIdentity.MatchString(productVersion) {
		identity := strings.TrimPrefix(strings.TrimPrefix(productVersion, "reconc-v"), "v")
		if _, err := semver.Parse(identity); err != nil {
			return nil, fmt.Errorf("invalid Reconc build identity %q: %w", productVersion, err)
		}
	}
	advancedCache.Lock()
	if advancedCache.pack != nil && advancedCache.productVersion == productVersion {
		pack := advancedCache.pack
		advancedCache.Unlock()
		return clonePack(pack), nil
	}
	pack, err := harnesspack.LoadBundledArchive(advancedArchive)
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
