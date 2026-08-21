package mcpgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
)

const maxFrameNumberBytes = 1024

type observedResponse struct {
	result json.RawMessage
	err    error
}

type pendingProtocolCall struct {
	request   string
	response  chan observedResponse
	completed func()
}

type protocolObserver struct {
	sendMu         sync.Mutex
	mu             sync.Mutex
	pending        map[string][]*pendingProtocolCall
	pendingMethods map[string]int
	byID           map[string]*pendingProtocolCall
}

func newProtocolObserver() *protocolObserver {
	return &protocolObserver{
		pending:        make(map[string][]*pendingProtocolCall),
		pendingMethods: make(map[string]int),
		byID:           make(map[string]*pendingProtocolCall),
	}
}

func (o *protocolObserver) begin(
	method string,
	params any,
	completed func(),
) (*pendingProtocolCall, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode observed %s params: %w", method, err)
	}
	paramsKey, err := correlationParamsKey(body)
	if err != nil {
		return nil, err
	}
	request := protocolRequestKey(method, paramsKey)
	call := &pendingProtocolCall{
		request: request, response: make(chan observedResponse, 1), completed: completed,
	}
	o.sendMu.Lock()
	o.mu.Lock()
	o.pending[request] = append(o.pending[request], call)
	o.pendingMethods[method]++
	o.mu.Unlock()
	return call, nil
}

func (o *protocolObserver) cancel(call *pendingProtocolCall) {
	if o == nil || call == nil {
		return
	}
	o.mu.Lock()
	releaseSend := false
	for request, calls := range o.pending {
		for index, pending := range calls {
			if pending != call {
				continue
			}
			method := protocolRequestMethod(request)
			o.pendingMethods[method]--
			if o.pendingMethods[method] == 0 {
				delete(o.pendingMethods, method)
			}
			if len(calls) == 1 {
				delete(o.pending, request)
			} else {
				o.pending[request] = append(calls[:index], calls[index+1:]...)
			}
			releaseSend = true
			break
		}
	}
	for id, pending := range o.byID {
		if pending == call {
			delete(o.byID, id)
		}
	}
	o.mu.Unlock()
	if releaseSend {
		o.sendMu.Unlock()
	}
}

func (o *protocolObserver) wait(ctx context.Context, call *pendingProtocolCall) (json.RawMessage, error) {
	result, err := waitObserved(ctx, call)
	if err != nil {
		o.cancel(call)
	}
	return result, err
}

func (o *protocolObserver) outbound(frame []byte) error {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil || envelope.Method == "" || len(envelope.ID) == 0 {
		return nil
	}
	o.mu.Lock()
	pendingMethod := o.pendingMethods[envelope.Method] != 0
	o.mu.Unlock()
	if !pendingMethod {
		return nil
	}
	id, err := canonicalProtocolID(envelope.ID)
	if err != nil {
		return err
	}
	paramsKey, err := correlationParamsKey(envelope.Params)
	if err != nil {
		return err
	}
	request := protocolRequestKey(envelope.Method, paramsKey)
	o.mu.Lock()
	if _, duplicate := o.byID[id]; duplicate {
		o.mu.Unlock()
		return fmt.Errorf("duplicate outbound JSON-RPC request ID")
	}
	calls := o.pending[request]
	if len(calls) == 0 {
		o.mu.Unlock()
		return fmt.Errorf("outbound %s request does not match its observed parameters", envelope.Method)
	}
	call := calls[0]
	if len(calls) == 1 {
		delete(o.pending, request)
	} else {
		o.pending[request] = calls[1:]
	}
	o.pendingMethods[envelope.Method]--
	if o.pendingMethods[envelope.Method] == 0 {
		delete(o.pendingMethods, envelope.Method)
	}
	o.byID[id] = call
	o.mu.Unlock()
	o.sendMu.Unlock()
	return nil
}

func protocolRequestKey(method, params string) string { return method + "\x00" + params }

func protocolRequestMethod(request string) string {
	if index := strings.IndexByte(request, 0); index >= 0 {
		return request[:index]
	}
	return request
}

func correlationParamsKey(raw []byte) (string, error) {
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return "", fmt.Errorf("decode observed request params: %s", action.JSONErrorKindOf(err))
	}
	members, _ := value.Members()
	type memberBody struct {
		name string
		body []byte
	}
	filtered := make([]memberBody, 0, len(members))
	for _, member := range members {
		if member.Name == "_meta" {
			continue
		}
		body, encodeErr := member.Value.MarshalJSON()
		if encodeErr != nil {
			return "", fmt.Errorf("canonicalize observed request params: %w", encodeErr)
		}
		filtered = append(filtered, memberBody{name: member.Name, body: body})
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].name < filtered[j].name })
	var canonical bytes.Buffer
	canonical.WriteByte('{')
	for index, member := range filtered {
		if index != 0 {
			canonical.WriteByte(',')
		}
		name, encodeErr := json.Marshal(member.name)
		if encodeErr != nil {
			return "", fmt.Errorf("canonicalize observed request field: %w", encodeErr)
		}
		canonical.Write(name)
		canonical.WriteByte(':')
		canonical.Write(member.body)
	}
	canonical.WriteByte('}')
	return canonical.String(), nil
}

func (o *protocolObserver) inbound(frame []byte) error {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil || len(envelope.ID) == 0 {
		return nil
	}
	id, err := canonicalProtocolID(envelope.ID)
	if err != nil {
		return err
	}
	o.mu.Lock()
	call := o.byID[id]
	delete(o.byID, id)
	o.mu.Unlock()
	if call == nil {
		return nil
	}
	if call.completed != nil {
		call.completed()
	}
	response := observedResponse{}
	if len(envelope.Error) != 0 && !bytes.Equal(envelope.Error, []byte("null")) {
		response.err = fmt.Errorf("downstream JSON-RPC error")
	} else if len(envelope.Result) == 0 {
		response.err = fmt.Errorf("downstream JSON-RPC response omitted result")
	} else {
		response.result = bytes.Clone(envelope.Result)
	}
	call.response <- response
	return nil
}

func canonicalProtocolID(raw []byte) (string, error) {
	value, err := action.ParseJSON(raw)
	if err != nil || value.Kind() != action.ValueString && value.Kind() != action.ValueNumber {
		return "", fmt.Errorf("JSON-RPC request ID is invalid")
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("canonicalize JSON-RPC request ID: %w", err)
	}
	if len(canonical) > MaxProtocolIDBytes {
		return "", fmt.Errorf("JSON-RPC request ID exceeds %d bytes", MaxProtocolIDBytes)
	}
	if value.Kind() == action.ValueNumber && strings.Contains(string(canonical), "e-") {
		return "", fmt.Errorf("JSON-RPC numeric request ID must be an integer")
	}
	return string(canonical), nil
}

type strictFrameReader struct {
	reader      *bufio.Reader
	closer      io.Closer
	observer    func([]byte) error
	transformer func([]byte) ([]byte, error)
	current     *bytes.Reader
	failed      error
}

func newStrictFrameReader(reader io.ReadCloser, observer func([]byte) error) *strictFrameReader {
	return &strictFrameReader{reader: bufio.NewReaderSize(reader, 64<<10), closer: reader, observer: observer}
}

func newStrictTransformingFrameReader(
	reader io.ReadCloser,
	transformer func([]byte) ([]byte, error),
) *strictFrameReader {
	return &strictFrameReader{
		reader: bufio.NewReaderSize(reader, 64<<10), closer: reader, transformer: transformer,
	}
}

func (r *strictFrameReader) Read(output []byte) (int, error) {
	if r.failed != nil {
		return 0, r.failed
	}
	if r.current == nil || r.current.Len() == 0 {
		frame, err := readFrame(r.reader)
		if err != nil {
			r.failed = err
			return 0, err
		}
		body := frame[:len(frame)-1]
		if err := validateFrameJSON(body); err != nil {
			r.failed = err
			return 0, err
		}
		if r.observer != nil {
			if err := r.observer(body); err != nil {
				r.failed = err
				return 0, err
			}
		}
		if r.transformer != nil {
			body, err = r.transformer(body)
			if err != nil {
				r.failed = err
				return 0, err
			}
			if len(body)+1 > MaxProtocolFrameBytes {
				err = fmt.Errorf("transformed MCP frame exceeds %d bytes", MaxProtocolFrameBytes)
				r.failed = err
				return 0, err
			}
			if err = validateFrameJSON(body); err != nil {
				r.failed = err
				return 0, err
			}
			frame = append(bytes.Clone(body), '\n')
		}
		r.current = bytes.NewReader(frame)
	}
	return r.current.Read(output)
}

func (r *strictFrameReader) Close() error { return r.closer.Close() }

func readFrame(reader *bufio.Reader) ([]byte, error) {
	frame := make([]byte, 0, 64<<10)
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > MaxProtocolFrameBytes-len(frame) {
			return nil, fmt.Errorf("MCP frame exceeds %d bytes", MaxProtocolFrameBytes)
		}
		frame = append(frame, part...)
		if err == nil {
			if len(frame) <= 1 {
				return nil, fmt.Errorf("MCP frame is empty")
			}
			return frame, nil
		}
		if err != bufio.ErrBufferFull {
			if err == io.EOF && len(frame) > 0 {
				return nil, fmt.Errorf("MCP frame is not newline terminated")
			}
			return nil, err
		}
	}
}

type strictFrameWriter struct {
	mu       sync.Mutex
	writer   io.Writer
	closer   io.Closer
	observer func([]byte) error
	pending  []byte
	failed   error
}

func newStrictFrameWriter(writer io.WriteCloser, observer func([]byte) error) *strictFrameWriter {
	return &strictFrameWriter{writer: writer, closer: writer, observer: observer}
}

func (w *strictFrameWriter) Write(input []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed != nil {
		return 0, w.failed
	}
	written := 0
	for len(input) > 0 {
		newline := bytes.IndexByte(input, '\n')
		part := input
		if newline >= 0 {
			part = input[:newline+1]
		}
		if len(part) > MaxProtocolFrameBytes-len(w.pending) {
			w.failed = fmt.Errorf("MCP frame exceeds %d bytes", MaxProtocolFrameBytes)
			return written, w.failed
		}
		pendingBefore := len(w.pending)
		w.pending = append(w.pending, part...)
		input = input[len(part):]
		if newline < 0 {
			written += len(part)
			continue
		}
		frame := w.pending[:len(w.pending)-1]
		if err := validateFrameJSON(frame); err != nil {
			w.failed = err
			return written, err
		}
		if w.observer != nil {
			if err := w.observer(frame); err != nil {
				w.failed = err
				return written, err
			}
		}
		count, err := w.writer.Write(w.pending)
		if count < 0 || count > len(w.pending) {
			count = len(w.pending)
			err = errors.Join(err, fmt.Errorf("MCP writer returned an invalid byte count"))
		}
		current := count - pendingBefore
		if current < 0 {
			current = 0
		}
		if current > len(part) {
			current = len(part)
		}
		written += current
		if err == nil && count != len(w.pending) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.failed = err
			return written, err
		}
		w.pending = w.pending[:0]
	}
	return written, nil
}

func (w *strictFrameWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) != 0 && w.failed == nil {
		w.failed = fmt.Errorf("MCP output ended with a partial frame")
	}
	return errors.Join(w.failed, w.closer.Close())
}

type frameContainer struct {
	object    bool
	expectKey bool
	keys      map[string]struct{}
}

func validateFrameJSON(frame []byte) error {
	if len(frame) == 0 || len(frame) > MaxProtocolFrameBytes || !utf8.Valid(frame) {
		return fmt.Errorf("MCP frame is empty, oversized, or invalid UTF-8")
	}
	if bytes.ContainsAny(frame, "\r\n") {
		return fmt.Errorf("MCP frame contains an embedded line delimiter")
	}
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.UseNumber()
	stack := make([]frameContainer, 0, 40)
	items := 0
	rootComplete := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decode MCP frame: %w", err)
		}
		if rootComplete {
			return fmt.Errorf("MCP frame contains trailing data")
		}
		items++
		if items == 1 {
			delimiter, object := token.(json.Delim)
			if !object || delimiter != '{' {
				return fmt.Errorf("MCP frame root must be a JSON object")
			}
		}
		if items > action.MaxJSONItems+4096 {
			return fmt.Errorf("MCP frame exceeds the JSON item boundary")
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{':
				if len(stack) >= action.MaxJSONDepth+8 {
					return fmt.Errorf("MCP frame exceeds the JSON depth boundary")
				}
				stack = append(stack, frameContainer{object: true, expectKey: true, keys: make(map[string]struct{})})
			case '[':
				if len(stack) >= action.MaxJSONDepth+8 {
					return fmt.Errorf("MCP frame exceeds the JSON depth boundary")
				}
				stack = append(stack, frameContainer{})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("MCP frame contains an unmatched delimiter")
				}
				stack = stack[:len(stack)-1]
				rootComplete = markFrameValueConsumed(stack)
			}
		case string:
			if len(value) > action.MaxJSONStringBytes {
				return fmt.Errorf("MCP frame string exceeds the byte boundary")
			}
			if len(stack) > 0 && stack[len(stack)-1].object && stack[len(stack)-1].expectKey {
				top := &stack[len(stack)-1]
				if _, duplicate := top.keys[value]; duplicate {
					return fmt.Errorf("MCP frame contains a duplicate object key")
				}
				top.keys[value] = struct{}{}
				top.expectKey = false
			} else {
				rootComplete = markFrameValueConsumed(stack)
			}
		case json.Number:
			if len(value.String()) > maxFrameNumberBytes {
				return fmt.Errorf("MCP frame number exceeds the byte boundary")
			}
			rootComplete = markFrameValueConsumed(stack)
		default:
			rootComplete = markFrameValueConsumed(stack)
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("MCP frame contains an unterminated container")
	}
	if !rootComplete {
		return fmt.Errorf("MCP frame contains no complete JSON value")
	}
	return nil
}

func markFrameValueConsumed(stack []frameContainer) bool {
	if len(stack) == 0 {
		return true
	}
	if len(stack) > 0 && stack[len(stack)-1].object && !stack[len(stack)-1].expectKey {
		stack[len(stack)-1].expectKey = true
	}
	return false
}

func waitObserved(ctx context.Context, call *pendingProtocolCall) (json.RawMessage, error) {
	if call == nil {
		return nil, fmt.Errorf("protocol observation is unavailable")
	}
	select {
	case response := <-call.response:
		return response.result, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
