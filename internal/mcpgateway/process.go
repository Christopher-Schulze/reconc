package mcpgateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/actionstate"
)

type ownedProcess struct {
	command    *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	boundary   processBoundary
	done       chan struct{}
	stderrDone chan struct{}
	waitMu     sync.Mutex
	waitErr    error
	stderr     *boundedStderr
	close      sync.Once
	closeErr   error
}

type boundedStderr struct {
	mu       sync.Mutex
	retained []byte
	total    uint64
}

func startOwnedProcess(observed actionstate.ObservedServer, arguments, environment []string) (*ownedProcess, error) {
	command := exec.Command(observed.ExecutablePath, arguments...)
	command.Dir = observed.WorkingDirectory
	command.Env = make([]string, len(environment))
	copy(command.Env, environment)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open downstream stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open downstream stdout: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("open downstream stderr: %w", err)
	}
	boundary, err := prepareProcessBoundary(command)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderrPipe.Close()
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = boundary.Close()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderrPipe.Close()
		return nil, fmt.Errorf("start downstream MCP server: %w", err)
	}
	command.Env = nil
	if err := boundary.Attach(command.Process); err != nil {
		processKillErr := command.Process.Kill()
		boundaryKillErr := boundary.Kill()
		waitErr := command.Wait()
		closeErr := boundary.Close()
		return nil, errors.Join(
			fmt.Errorf("attach downstream process ownership: %w", err),
			cleanupProcessError("kill unattached downstream process", processKillErr),
			cleanupProcessError("kill unattached downstream process boundary", boundaryKillErr),
			cleanupProcessError("wait for unattached downstream process", waitErr),
			cleanupProcessError("close unattached downstream process boundary", closeErr),
		)
	}
	process := &ownedProcess{
		command: command, stdin: stdin, stdout: stdout, boundary: boundary,
		done: make(chan struct{}), stderrDone: make(chan struct{}), stderr: &boundedStderr{},
	}
	go func() {
		defer close(process.stderrDone)
		process.stderr.drain(stderrPipe)
	}()
	go func() {
		waitErr := command.Wait()
		process.waitMu.Lock()
		process.waitErr = waitErr
		process.waitMu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func cleanupProcessError(operation string, err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (p *ownedProcess) Close() error {
	if p == nil {
		return nil
	}
	p.close.Do(func() {
		alreadyExited := channelClosed(p.done)
		p.closeErr = errors.Join(closeProcessPipe(p.stdin), closeProcessPipe(p.stdout))
		select {
		case <-p.done:
			if alreadyExited {
				p.closeErr = errors.Join(p.closeErr, p.processError())
			}
			p.closeErr = errors.Join(p.closeErr, p.boundary.Close())
			p.waitForStderr()
			return
		case <-time.After(ChildKillGrace):
		}
		p.closeErr = errors.Join(p.closeErr, p.boundary.Terminate())
		select {
		case <-p.done:
			p.closeErr = errors.Join(p.closeErr, p.boundary.Close())
			p.waitForStderr()
			return
		case <-time.After(ChildKillGrace):
		}
		p.closeErr = errors.Join(p.closeErr, p.boundary.Kill())
		select {
		case <-p.done:
			p.waitForStderr()
		case <-time.After(ChildKillGrace):
			p.closeErr = errors.Join(p.closeErr, fmt.Errorf("downstream process did not exit after forced termination"))
		}
		p.closeErr = errors.Join(p.closeErr, p.boundary.Close())
	})
	return p.closeErr
}

func closeProcessPipe(closer io.Closer) error {
	if closer == nil {
		return nil
	}
	err := closer.Close()
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func (p *ownedProcess) waitForStderr() {
	if p.stderrDone == nil {
		return
	}
	select {
	case <-p.stderrDone:
	case <-time.After(ChildKillGrace):
		p.closeErr = errors.Join(p.closeErr, fmt.Errorf("downstream stderr drain did not terminate"))
	}
}

func (p *ownedProcess) Wait() error {
	if p == nil {
		return fmt.Errorf("downstream process is unavailable")
	}
	<-p.done
	return p.processError()
}

func (p *ownedProcess) processError() error {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return processWaitError(p.waitErr)
}

func (p *ownedProcess) StderrSummary(ctx context.Context) string {
	if p == nil || p.stderr == nil {
		return ""
	}
	return p.stderr.summary(ctx)
}

func (s *boundedStderr) drain(reader io.ReadCloser) {
	defer reader.Close()
	buffer := make([]byte, 32<<10)
	for {
		read, err := reader.Read(buffer)
		if read > 0 {
			s.mu.Lock()
			if uint64(read) > math.MaxUint64-s.total {
				s.total = math.MaxUint64
			} else {
				s.total += uint64(read)
			}
			remaining := MaxStderrBytes - len(s.retained)
			if remaining > 0 {
				keep := min(read, remaining)
				s.retained = append(s.retained, buffer[:keep]...)
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *boundedStderr) summary(ctx context.Context) string {
	s.mu.Lock()
	body := bytes.Clone(s.retained)
	total := s.total
	s.mu.Unlock()
	if total == 0 {
		return ""
	}
	categories := []string{}
	if utf8Text := strings.ToValidUTF8(string(body), ""); utf8Text != "" {
		scanner, err := actioninspect.NewTextScanner()
		if err == nil {
			found, scanErr := scanner.PrivateCategories(ctx, utf8Text, uint64(max(1, len(utf8Text))))
			if scanErr == nil {
				for _, category := range found {
					categories = append(categories, string(category))
				}
			}
		}
	}
	sort.Strings(categories)
	classification := "none"
	if len(categories) > 0 {
		classification = strings.Join(categories, ",")
	}
	return fmt.Sprintf("downstream stderr redacted: bytes=%d retained=%d classifications=%s", total, len(body), classification)
}

func processWaitError(err error) error {
	if err == nil {
		return nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return fmt.Errorf("downstream MCP server exited with code %d", exitError.ExitCode())
	}
	return fmt.Errorf("wait for downstream MCP server: %w", err)
}
