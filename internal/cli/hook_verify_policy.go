package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type liveHookPolicyProbe struct {
	reader *os.File
	writer *os.File
}

func prepareLiveHookPolicyProbe(command *exec.Cmd) (*liveHookPolicyProbe, error) {
	// Go cannot pass ExtraFiles on Windows. Preserve transport capture there
	// while leaving policy provenance explicitly unproven.
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	command.ExtraFiles = []*os.File{writer}
	command.Env = append(os.Environ(), "RECONC_HOOK_TIMING_FD=3")
	return &liveHookPolicyProbe{reader: reader, writer: writer}, nil
}

func (p *liveHookPolicyProbe) decision() (string, error) {
	if p == nil {
		return "unproven", nil
	}
	closeErr := p.writer.Close()
	p.writer = nil
	if closeErr != nil {
		return "unproven", closeErr
	}
	// A descendant retaining fd 3 must not keep verification waiting after the
	// wrapper exited. Only the small runtime-generated metadata is accepted.
	if err := p.reader.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		return "unproven", err
	}
	body, err := io.ReadAll(io.LimitReader(p.reader, 129))
	if err != nil || len(body) > 128 {
		return "unproven", fmt.Errorf("policy metadata unavailable or oversized")
	}
	if len(body) == 0 {
		return "unproven", nil
	}
	return parseLiveHookPolicyProbe(body)
}

func (p *liveHookPolicyProbe) close() error {
	if p == nil {
		return nil
	}
	var err error
	if p.writer != nil {
		err = p.writer.Close()
		p.writer = nil
	}
	return errors.Join(err, p.reader.Close())
}

func parseLiveHookPolicyProbe(body []byte) (string, error) {
	lines := strings.Split(string(body), "\n")
	if len(lines) != 3 || lines[2] != "" || !strings.HasPrefix(lines[0], "duration_ns=") {
		return "unproven", fmt.Errorf("invalid policy metadata framing")
	}
	duration, err := strconv.ParseInt(strings.TrimPrefix(lines[0], "duration_ns="), 10, 64)
	if err != nil || duration < 0 || duration > int64(time.Minute) {
		return "unproven", fmt.Errorf("invalid policy metadata duration")
	}
	switch lines[1] {
	case "policy_decision=pass":
		return "pass", nil
	case "policy_decision=block":
		return "block", nil
	case "policy_decision=unproven":
		return "unproven", nil
	default:
		return "unproven", fmt.Errorf("invalid policy metadata decision")
	}
}
