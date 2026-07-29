// Package safename validates user-selectable names that become filenames.
package safename

import (
	"fmt"
	"regexp"
	"strings"
)

const MaxLength = 64

var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Normalize trims and validates one portable lowercase kebab-case name.
func Normalize(kind, value string) (string, error) {
	name := strings.TrimSpace(value)
	if !pattern.MatchString(name) {
		return "", fmt.Errorf("%s name %q must match [a-z0-9][a-z0-9-]{0,%d}", kind, value, MaxLength-1)
	}
	return name, nil
}
