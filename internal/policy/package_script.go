package policy

import (
	"fmt"
	"strings"
)

// PackageScriptCommand is the normalized contract accepted by the native
// package_scripts assurance gate.
type PackageScriptCommand struct {
	Runner string
	Script string
}

// ParsePackageScriptCommand validates one manager-script command without
// invoking a shell or package-manager executable.
func ParsePackageScriptCommand(command string) (PackageScriptCommand, error) {
	fields := strings.Fields(command)
	if len(fields) != 3 || fields[1] != "run" {
		return PackageScriptCommand{}, fmt.Errorf("package_scripts command must use '<bun|npm|pnpm|yarn> run <script>': %q", command)
	}
	runner := strings.ToLower(fields[0])
	if runner != "bun" && runner != "npm" && runner != "pnpm" && runner != "yarn" {
		return PackageScriptCommand{}, fmt.Errorf("package_scripts command uses unsupported runner %q", fields[0])
	}
	if strings.HasPrefix(fields[2], "-") {
		return PackageScriptCommand{}, fmt.Errorf("package_scripts command has invalid script name: %q", command)
	}
	return PackageScriptCommand{Runner: runner, Script: fields[2]}, nil
}
