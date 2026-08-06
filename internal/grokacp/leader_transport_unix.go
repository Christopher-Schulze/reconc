//go:build !windows

package grokacp

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

const maxGrokHomeEntries = 4096

// leaderSocketCandidates lists the Unix sockets a running Grok leader may be
// bound to, most specific first. Hooks dispatched by a leader-hosted session
// inherit the leader's environment, so GROK_LEADER_SOCKET is authoritative
// when set. Otherwise every leader<suffix>.sock under the Grok home is a
// candidate because non-default relay URLs derive suffixed socket names.
func leaderSocketCandidates() ([]string, error) {
	if override := strings.TrimSpace(os.Getenv(leaderSocketEnv)); override != "" {
		exists, err := socketExists(override)
		if err != nil {
			return nil, err
		}
		if exists {
			return []string{override}, nil
		}
		return nil, nil
	}
	home := strings.TrimSpace(os.Getenv(grokHomeEnv))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".grok")
	}
	entries, err := boundedio.ReadDir(home, maxGrokHomeEntries)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	matches := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "leader") && strings.HasSuffix(entry.Name(), ".sock") {
			matches = append(matches, filepath.Join(home, entry.Name()))
		}
	}
	sort.Strings(matches)
	defaultSocket := filepath.Join(home, "leader.sock")
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		exists, err := socketExists(match)
		if err != nil {
			return nil, err
		}
		if match == defaultSocket && exists {
			candidates = append([]string{match}, candidates...)
			continue
		}
		if exists {
			candidates = append(candidates, match)
		}
	}
	return candidates, nil
}

func socketExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSocket != 0, nil
}

func dialLeader(endpoint string, deadline time.Time) (*leaderConn, error) {
	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.Dial("unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial Grok leader %s: %w", endpoint, err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set Grok leader deadline: %w", err)
	}
	return &leaderConn{conn: conn}, nil
}
