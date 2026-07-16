//go:build windows

package grokacp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	windowsPipeRoot    = `\\.\pipe\`
	grokPipeNamePrefix = "grok-leader-"
)

var findWindowsLeaderPipes = listWindowsLeaderPipes

// leaderSocketCandidates enumerates Grok's named pipes. Grok derives the pipe
// suffix from its logical socket path with Rust's fixed SipHash-1-3 mapping;
// enumerating the private grok-leader-* namespace avoids duplicating that
// language-specific path hash. Registration plus session identity selects the
// correct leader when several instances exist.
func leaderSocketCandidates() []string {
	if override := strings.TrimSpace(os.Getenv(leaderSocketEnv)); strings.HasPrefix(strings.ToLower(override), strings.ToLower(windowsPipeRoot)) {
		return []string{override}
	}
	candidates, err := findWindowsLeaderPipes()
	if err != nil {
		return nil
	}
	sort.Strings(candidates)
	return candidates
}

func listWindowsLeaderPipes() ([]string, error) {
	pattern, err := windows.UTF16PtrFromString(windowsPipeRoot + grokPipeNamePrefix + "*")
	if err != nil {
		return nil, fmt.Errorf("encode Grok leader pipe pattern: %w", err)
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(pattern, &data)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enumerate Grok leader pipes: %w", err)
	}
	defer func() { _ = windows.FindClose(handle) }()

	candidates := []string{}
	for {
		name := windows.UTF16ToString(data.FileName[:])
		if strings.HasPrefix(strings.ToLower(name), grokPipeNamePrefix) {
			candidates = append(candidates, windowsPipeRoot+name)
		}
		err = windows.FindNextFile(handle, &data)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("enumerate Grok leader pipes: %w", err)
		}
	}
	return candidates, nil
}

func dialLeader(endpoint string, deadline time.Time) (*leaderConn, error) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial Grok leader %s: %w", endpoint, err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set Grok leader deadline: %w", err)
	}
	return &leaderConn{conn: conn}, nil
}
