package cli

import (
	"fmt"
	"io"

	skillbundle "reconc.dev/reconc/skills/reconc"
)

func runSkillManifest(args []string, stdout io.Writer) error {
	if len(args) == 1 && isHelpFlag(args[0]) {
		_, err := fmt.Fprintln(stdout, "Usage: reconc skill-manifest --json")
		return err
	}
	jsonOut := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOut = true
			continue
		}
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc skill-manifest: unknown argument %q", arg)}
	}
	if !jsonOut {
		return &CLIError{ExitCode: 1, Message: "reconc skill-manifest: --json is required"}
	}
	body, err := skillbundle.EncodeManifest()
	if err != nil {
		return fmt.Errorf("encode embedded skill manifest: %w", err)
	}
	_, err = stdout.Write(body)
	return err
}
