//go:build windows

package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/execfile"
)

func scriptCommand(ctx context.Context, path string, args []string) (*exec.Cmd, error) {
	if !execfile.Is(path) {
		return nil, fmt.Errorf("script is not a regular file: %s", path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com":
		return exec.CommandContext(ctx, path, args...), nil
	case "", ".sh":
		commandArgs := append([]string{path}, args...)
		return exec.CommandContext(ctx, "sh", commandArgs...), nil
	default:
		return nil, fmt.Errorf("unsupported Windows script type %q: use .sh, an extensionless shell script, .exe, or .com", filepath.Ext(path))
	}
}
