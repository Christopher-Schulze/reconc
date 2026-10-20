package usercli

import (
	"sort"

	"reconc.dev/reconc/internal/semver"
)

func allDigits(value string) bool {
	return semver.IsNumericIdentifier(value)
}

func sortedReleaseAssets(assets []ReleaseAsset) []ReleaseAsset {
	out := append([]ReleaseAsset(nil), assets...)
	sort.Slice(out, func(left, right int) bool {
		return out[left].Name < out[right].Name
	})
	return out
}
