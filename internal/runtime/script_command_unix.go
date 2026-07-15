//go:build !windows

package runtime

import (
	"context"
	"fmt"
	"os/exec"

	"reconc.dev/reconc/internal/execfile"
)

func scriptCommand(ctx context.Context, path string, args []string) (*exec.Cmd, error) {
	if !execfile.Is(path) {
		return nil, fmt.Errorf("script is not executable (no +x bit): %s", path)
	}
	return exec.CommandContext(ctx, path, args...), nil
}
