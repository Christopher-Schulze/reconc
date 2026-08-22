package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/boundedexec"
	policyruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

const (
	hookWorkerFormatVersion = 1
	hookWorkerFrameOverhead = 64 << 10
	hookWorkerMaxIDBytes    = 128
	hookWorkerMaxEventBytes = 128
	hookWorkerMaxRepoBytes  = 16 << 10
	maxHookRuntimeCapture   = 8 << 10
	maxHookWorkerDrainBytes = 2 * (agentsession.MaxPayloadBytes + hookWorkerFrameOverhead)
	hookWorkerReadBuffer    = 64 << 10
	hookWorkerGrowthWindow  = 4
)

var errHookWorkerFrameTooLarge = errors.New("hook worker frame exceeds the bounded protocol limit")
var errHookWorkerFrameDrainFailed = errors.New("hook worker oversized frame could not be drained")

type hookWorkerRequest struct {
	FormatVersion int             `json:"format_version"`
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	Event         string          `json:"event,omitempty"`
	Repo          string          `json:"repo,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type hookWorkerResponse struct {
	FormatVersion int    `json:"format_version"`
	Type          string `json:"type"`
	ID            string `json:"id"`
	Code          int    `json:"code"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
	Error         string `json:"error,omitempty"`
}

type hookWorkerRootCache struct {
	roots     map[string]agentsession.ResolvedRepoRoot
	evaluator *policyruntime.Evaluator
	stopCache *agentsession.StopDecisionCache
}

func runHookWorker(args []string, input io.Reader, output io.Writer) error {
	return runHookWorkerWithFrameLimit(args, input, output, agentsession.MaxPayloadBytes+hookWorkerFrameOverhead)
}

func runHookWorkerWithFrameLimit(args []string, input io.Reader, output io.Writer, frameLimit int) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(output, "Usage: reconc hook worker   (internal; versioned NDJSON over stdin/stdout)")
		return nil
	}
	if len(args) != 0 {
		return &CLIError{ExitCode: 1, Message: "reconc hook worker: accepts no arguments"}
	}
	reader := bufio.NewReaderSize(input, hookWorkerReadBuffer)
	encoder := json.NewEncoder(output)
	rootCache := hookWorkerRootCache{
		roots: make(map[string]agentsession.ResolvedRepoRoot), evaluator: policyruntime.NewEvaluator(),
		stopCache: agentsession.NewStopDecisionCache(),
	}
	for {
		frame, err := readHookWorkerFrameLimit(reader, frameLimit)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(err, errHookWorkerFrameTooLarge) && !errors.Is(err, errHookWorkerFrameDrainFailed) {
				response := hookWorkerResponse{
					FormatVersion: hookWorkerFormatVersion,
					Type:          "error",
					Code:          1,
					Error:         errHookWorkerFrameTooLarge.Error(),
				}
				if encodeErr := encoder.Encode(response); encodeErr != nil {
					return &CLIError{ExitCode: 1, Message: "reconc hook worker: write protocol error: " + encodeErr.Error()}
				}
				continue
			}
			return &CLIError{ExitCode: 1, Message: "reconc hook worker: " + err.Error()}
		}
		request, err := decodeHookWorkerRequest(frame)
		if err != nil {
			responseID := ""
			if validHookWorkerToken(request.ID, hookWorkerMaxIDBytes) {
				responseID = request.ID
			}
			response := hookWorkerResponse{
				FormatVersion: hookWorkerFormatVersion,
				Type:          "error",
				ID:            responseID,
				Code:          1,
				Error:         truncateUTF8(err.Error(), 4096),
			}
			if encodeErr := encoder.Encode(response); encodeErr != nil {
				return &CLIError{ExitCode: 1, Message: "reconc hook worker: write protocol error: " + encodeErr.Error()}
			}
			continue
		}
		response, stop := executeHookWorkerRequest(request, rootCache.resolve, rootCache.evaluator, rootCache.stopCache)
		if err := encoder.Encode(response); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook worker: write response: " + err.Error()}
		}
		if stop {
			return nil
		}
	}
}

func readHookWorkerFrameLimit(reader *bufio.Reader, limit int) ([]byte, error) {
	if limit < 1 {
		return nil, fmt.Errorf("hook worker frame limit must be positive")
	}
	var frame []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(frame)+len(fragment) > limit {
			if err == nil {
				return nil, errHookWorkerFrameTooLarge
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				if drainErr := drainHookWorkerFrame(reader, maxHookWorkerDrainBytes); drainErr != nil {
					return nil, fmt.Errorf("%w: %w", errHookWorkerFrameTooLarge, drainErr)
				}
				return nil, errHookWorkerFrameTooLarge
			}
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("%w: %w: truncated hook worker frame", errHookWorkerFrameTooLarge, errHookWorkerFrameDrainFailed)
			}
			return nil, fmt.Errorf("%w: %w: %v", errHookWorkerFrameTooLarge, errHookWorkerFrameDrainFailed, err)
		}
		if len(frame) == 0 && err == nil {
			return validateHookWorkerFrame(bytes.Clone(fragment))
		}
		frame = appendHookWorkerFragment(frame, fragment, limit, reader.Size()*hookWorkerGrowthWindow+2)
		switch {
		case err == nil:
			return validateHookWorkerFrame(frame)
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(frame) == 0:
			return nil, io.EOF
		case errors.Is(err, io.EOF):
			return nil, errors.New("truncated hook worker frame")
		default:
			return nil, fmt.Errorf("read hook worker frame: %w", err)
		}
	}
}

func appendHookWorkerFragment(frame, fragment []byte, limit, initialCapacity int) []byte {
	required := len(frame) + len(fragment)
	if required <= cap(frame) {
		return append(frame, fragment...)
	}
	capacity := cap(frame) * hookWorkerGrowthWindow
	if capacity == 0 {
		capacity = initialCapacity
	}
	capacity = min(limit, max(required, capacity))
	grown := make([]byte, len(frame), capacity)
	copy(grown, frame)
	return append(grown, fragment...)
}

func validateHookWorkerFrame(frame []byte) ([]byte, error) {
	frame = bytes.TrimSuffix(frame, []byte{'\n'})
	frame = bytes.TrimSuffix(frame, []byte{'\r'})
	if len(frame) == 0 {
		return nil, errors.New("empty hook worker frame")
	}
	if !utf8.Valid(frame) {
		return nil, errors.New("hook worker frame is not valid UTF-8")
	}
	return frame, nil
}

// drainHookWorkerFrame consumes the remainder of one oversized newline frame
// without retaining attacker-controlled bytes. A frame that exceeds the
// independent drain budget terminates the worker; otherwise the next frame is
// left at the reader boundary and can be processed normally.
func drainHookWorkerFrame(reader *bufio.Reader, limit int) error {
	var drained int
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > limit-drained {
			return errHookWorkerFrameDrainFailed
		}
		drained += len(fragment)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return fmt.Errorf("%w: truncated hook worker frame", errHookWorkerFrameDrainFailed)
		default:
			return fmt.Errorf("%w: %v", errHookWorkerFrameDrainFailed, err)
		}
	}
}

func decodeHookWorkerRequest(frame []byte) (hookWorkerRequest, error) {
	var request hookWorkerRequest
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("invalid request frame: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return request, errors.New("request frame contains trailing JSON")
	}
	if request.FormatVersion != hookWorkerFormatVersion {
		return request, fmt.Errorf("unsupported format_version %d", request.FormatVersion)
	}
	if !validHookWorkerToken(request.ID, hookWorkerMaxIDBytes) {
		return request, errors.New("request id must be a bounded printable token")
	}
	switch request.Type {
	case "ping", "shutdown":
		if request.Event != "" || request.Repo != "" || len(request.Payload) != 0 {
			return request, fmt.Errorf("%s frame contains request-only fields", request.Type)
		}
	case "request":
		if !validHookWorkerToken(request.Event, hookWorkerMaxEventBytes) {
			return request, errors.New("request event must be a bounded printable token")
		}
		if strings.TrimSpace(request.Repo) == "" || len(request.Repo) > hookWorkerMaxRepoBytes || !utf8.ValidString(request.Repo) {
			return request, errors.New("request repo must be a bounded UTF-8 path")
		}
		if len(request.Payload) == 0 || len(request.Payload) > agentsession.MaxPayloadBytes {
			return request, errors.New("request payload is empty or exceeds the hook payload limit")
		}
		payload := bytes.TrimSpace(request.Payload)
		if len(payload) < 2 || payload[0] != '{' || payload[len(payload)-1] != '}' || !json.Valid(payload) {
			return request, errors.New("request payload must be one valid JSON object")
		}
	default:
		return request, fmt.Errorf("unsupported frame type %q", request.Type)
	}
	return request, nil
}

func validHookWorkerToken(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func (cache *hookWorkerRootCache) resolve(repo string) (agentsession.ResolvedRepoRoot, error) {
	if root, ok := cache.roots[repo]; ok && root.Revalidate() == nil {
		return root, nil
	}
	root, err := agentsession.ResolveRepoRootRef(repo)
	if err != nil {
		delete(cache.roots, repo)
		return agentsession.ResolvedRepoRoot{}, err
	}
	cache.roots[repo] = root
	return root, nil
}

func executeHookWorkerRequest(
	request hookWorkerRequest,
	resolveRoot func(string) (agentsession.ResolvedRepoRoot, error),
	evaluator *policyruntime.Evaluator,
	stopCache *agentsession.StopDecisionCache,
) (hookWorkerResponse, bool) {
	response := hookWorkerResponse{
		FormatVersion: hookWorkerFormatVersion,
		Type:          "response",
		ID:            request.ID,
	}
	if request.Type == "ping" {
		return response, false
	}
	if request.Type == "shutdown" {
		response.Type = "shutdown"
		return response, true
	}
	stdout, err := boundedexec.NewBuffer(maxHookRuntimeCapture)
	if err != nil {
		response.Code = 1
		response.Error = truncateUTF8(err.Error(), 4096)
		return response, false
	}
	stderr, err := boundedexec.NewBuffer(maxHookRuntimeCapture)
	if err != nil {
		response.Code = 1
		response.Error = truncateUTF8(err.Error(), 4096)
		return response, false
	}
	err = runHookRuntimeWithResolverEvaluatorAndStopCache(
		[]string{request.Event, request.Repo},
		bytes.NewReader(request.Payload),
		stdout,
		stderr,
		resolveRoot,
		evaluator,
		stopCache,
	)
	response.Code = ExitCode(err)
	response.Stdout = strings.TrimSuffix(stdout.String(), "\n")
	response.Stderr = strings.TrimSuffix(stderr.String(), "\n")
	if stdout.Truncated() || stderr.Truncated() {
		response.Code = 1
		response.Error = fmt.Sprintf("hook runtime output exceeded %d bytes per stream", maxHookRuntimeCapture)
	}
	if err != nil && err.Error() != "" {
		if response.Stderr != "" {
			response.Stderr += "\n"
		}
		response.Stderr += truncateUTF8(err.Error(), 4096)
	}
	return response, false
}
