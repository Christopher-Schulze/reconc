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
)

// leaderSocketCandidates lists the Unix sockets a running Grok leader may be
// bound to, most specific first. Hooks dispatched by a leader-hosted session
// inherit the leader's environment, so GROK_LEADER_SOCKET is authoritative
// when set. Otherwise every leader<suffix>.sock under the Grok home is a
// candidate because non-default relay URLs derive suffixed socket names.
func leaderSocketCandidates() []string {
	if override := strings.TrimSpace(os.Getenv(leaderSocketEnv)); override != "" {
		if socketExists(override) {
			return []string{override}
		}
		return nil
	}
	home := strings.TrimSpace(os.Getenv(grokHomeEnv))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		home = filepath.Join(userHome, ".grok")
	}
	matches, err := filepath.Glob(filepath.Join(home, "leader*.sock"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	defaultSocket := filepath.Join(home, "leader.sock")
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		if match == defaultSocket && socketExists(match) {
			candidates = append([]string{match}, candidates...)
			continue
		}
		if socketExists(match) {
			candidates = append(candidates, match)
		}
	}
	return candidates
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
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
