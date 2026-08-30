// Package boundedexec captures subprocess output without allowing a child
// process to grow the parent process memory without bound.
package boundedexec

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

var ErrOutputLimit = errors.New("subprocess output limit exceeded")

// Buffer is a concurrency-safe output sink that reports truncation after it
// has accepted the caller's configured maximum bytes.
type Buffer struct {
	mutex     sync.Mutex
	body      bytes.Buffer
	limit     int
	truncated bool
}

func (output *Buffer) Write(value []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	remaining := output.limit - output.body.Len()
	if remaining > len(value) {
		remaining = len(value)
	}
	if remaining > 0 {
		_, _ = output.body.Write(value[:remaining])
	}
	if remaining < len(value) {
		output.truncated = true
	}
	return len(value), nil
}

// Bytes returns a stable copy of the retained prefix.
func (output *Buffer) Bytes() []byte {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return append([]byte(nil), output.body.Bytes()...)
}

// String returns the retained prefix as text.
func (output *Buffer) String() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.body.String()
}

// Truncated reports whether bytes beyond the configured limit were dropped.
func (output *Buffer) Truncated() bool {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.truncated
}

// NewBuffer creates a bounded subprocess output sink.
func NewBuffer(limit int) (*Buffer, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("subprocess output limit must be positive")
	}
	return &Buffer{limit: limit}, nil
}

// CombinedOutput is the bounded equivalent of exec.Cmd.CombinedOutput.
func CombinedOutput(command *exec.Cmd, limit int) ([]byte, error) {
	if command.Stdout != nil || command.Stderr != nil {
		return nil, fmt.Errorf("bounded combined output requires unset stdout and stderr")
	}
	output, err := NewBuffer(limit)
	if err != nil {
		return nil, err
	}
	command.Stdout = output
	command.Stderr = output
	runErr := command.Run()
	if output.Truncated() {
		runErr = errors.Join(runErr, fmt.Errorf("%w: maximum %d bytes", ErrOutputLimit, limit))
	}
	return output.Bytes(), runErr
}

// Output is the bounded equivalent of exec.Cmd.Output. Stderr is separately
// bounded and attached to ExitError when the caller did not configure it.
func Output(command *exec.Cmd, limit int) ([]byte, error) {
	if command.Stdout != nil {
		return nil, fmt.Errorf("bounded output requires unset stdout")
	}
	stdout, err := NewBuffer(limit)
	if err != nil {
		return nil, err
	}
	command.Stdout = stdout
	var stderr *Buffer
	if command.Stderr == nil {
		stderr, err = NewBuffer(limit)
		if err != nil {
			return nil, err
		}
		command.Stderr = stderr
	}
	runErr := command.Run()
	if exitErr, ok := runErr.(*exec.ExitError); ok && stderr != nil {
		exitErr.Stderr = stderr.Bytes()
	}
	truncated := stdout.Truncated() || stderr != nil && stderr.Truncated()
	if truncated {
		runErr = errors.Join(runErr, fmt.Errorf("%w: maximum %d bytes per stream", ErrOutputLimit, limit))
	}
	return stdout.Bytes(), runErr
}
