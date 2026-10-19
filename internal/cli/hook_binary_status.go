package cli

import (
	"fmt"
	"io"

	"reconc.dev/reconc/internal/hooks"
)

func writeHookBinaryStatus(output io.Writer, binary *hooks.WrapperBinaryStatus) error {
	if binary == nil {
		return nil
	}
	if binary.Path != "" {
		if _, err := fmt.Fprintf(output, "  runtime binary: %s (source=%s version=%s)\n", binary.Path, binary.Source, binary.Version); err != nil {
			return err
		}
	}
	if binary.Error != "" {
		_, err := fmt.Fprintf(output, "  runtime binary inspection: %s\n", binary.Error)
		return err
	}
	return nil
}
