// Package semver parses and orders strict Semantic Versioning values.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// Version is a parsed Semantic Versioning value.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease []string
	Build      []string
	Original   string
}

// Parse accepts strict Semantic Versioning, including optional prerelease and
// build metadata identifiers. Surrounding whitespace is ignored for parity
// with the repository's existing version inputs.
func Parse(value string) (Version, error) {
	clean := strings.TrimSpace(value)
	match := versionPattern.FindStringSubmatch(clean)
	if len(match) != 6 {
		return Version{}, fmt.Errorf("version %q is not supported semantic versioning", value)
	}
	parts := [3]uint64{}
	for index := range parts {
		number, err := strconv.ParseUint(match[index+1], 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("version component overflows: %q", value)
		}
		parts[index] = number
	}
	prerelease, err := parseIdentifiers(match[4], true, value)
	if err != nil {
		return Version{}, err
	}
	build, err := parseIdentifiers(match[5], false, value)
	if err != nil {
		return Version{}, err
	}
	return Version{
		Major: parts[0], Minor: parts[1], Patch: parts[2],
		Prerelease: prerelease, Build: build, Original: clean,
	}, nil
}

func parseIdentifiers(value string, prerelease bool, original string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	identifiers := strings.Split(value, ".")
	if prerelease {
		for _, identifier := range identifiers {
			if IsNumericIdentifier(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return nil, fmt.Errorf("invalid prerelease identifier in %q", original)
			}
		}
	}
	return identifiers, nil
}

// Compare returns -1 when left precedes right, 1 when it follows, and 0 when
// both versions have equal precedence. Build metadata is intentionally ignored.
func Compare(left, right Version) int {
	for _, pair := range [][2]uint64{
		{left.Major, right.Major},
		{left.Minor, right.Minor},
		{left.Patch, right.Patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.Prerelease) == 0 && len(right.Prerelease) == 0 {
		return 0
	}
	if len(left.Prerelease) == 0 {
		return 1
	}
	if len(right.Prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.Prerelease) && index < len(right.Prerelease); index++ {
		if comparison := compareIdentifier(left.Prerelease[index], right.Prerelease[index]); comparison != 0 {
			return comparison
		}
	}
	if len(left.Prerelease) < len(right.Prerelease) {
		return -1
	}
	if len(left.Prerelease) > len(right.Prerelease) {
		return 1
	}
	return 0
}

func compareIdentifier(left, right string) int {
	leftNumeric := IsNumericIdentifier(left)
	rightNumeric := IsNumericIdentifier(right)
	if leftNumeric && rightNumeric {
		if len(left) < len(right) {
			return -1
		}
		if len(left) > len(right) {
			return 1
		}
		return strings.Compare(left, right)
	}
	if leftNumeric {
		return -1
	}
	if rightNumeric {
		return 1
	}
	return strings.Compare(left, right)
}

// IsNumericIdentifier reports whether value contains only decimal digits.
func IsNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
