//go:build linux

package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// /proc/<pid>/stat starts with "pid (comm) state ppid"; comm may contain
// spaces and parentheses, so the fields are read after the last ")".
func liveHookParentProcessID(pid int) (int, error) {
	body, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	text := string(body)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return 0, fmt.Errorf("process %d has an unreadable status", pid)
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("process %d has an unreadable status", pid)
	}
	return strconv.Atoi(fields[1])
}
