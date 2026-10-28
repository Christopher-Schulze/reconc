//go:build linux

package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const maxLiveHookProcessStatusBytes = 4096

// /proc/<pid>/stat starts with "pid (comm) state ppid"; comm may contain
// spaces and parentheses, so the fields are read after the last ")". The file
// reports size zero, so it is read through an explicit byte limit.
func liveHookParentProcessID(pid int) (int, error) {
	file, err := os.Open("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxLiveHookProcessStatusBytes+1))
	if err != nil {
		return 0, err
	}
	if len(body) > maxLiveHookProcessStatusBytes {
		return 0, fmt.Errorf("process %d status exceeds %d bytes", pid, maxLiveHookProcessStatusBytes)
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
