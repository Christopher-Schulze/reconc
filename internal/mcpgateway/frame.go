package mcpgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"reconc.dev/reconc/internal/action"
)

const maxFrameNumberBytes = 1024

const maxRetainedFrameBufferBytes = 256 << 10

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
	parsed, err := parseFrameJSON(frame)
	if err != nil {
		return err
	}
	return o.outboundFrame(parsed)
}

func (o *protocolObserver) outboundFrame(frame validatedFrame) error {
	if frame.method == "" || len(frame.id) == 0 {
		return nil
	}
	o.mu.Lock()
	pendingMethod := o.pendingMethods[frame.method] != 0
	o.mu.Unlock()
	if !pendingMethod {
		return nil
	}
	id, err := canonicalProtocolID(frame.id)
	if err != nil {
		return err
	}
	paramsKey, err := correlationParamsKey(frame.params)
	if err != nil {
		return err
	}
	request := protocolRequestKey(frame.method, paramsKey)
	o.mu.Lock()
	if _, duplicate := o.byID[id]; duplicate {
		o.mu.Unlock()
		return fmt.Errorf("duplicate outbound JSON-RPC request ID")
	}
	calls := o.pending[request]
	if len(calls) == 0 {
		o.mu.Unlock()
		return fmt.Errorf("outbound %s request does not match its observed parameters", frame.method)
	}
	call := calls[0]
	if len(calls) == 1 {
		delete(o.pending, request)
	} else {
		o.pending[request] = calls[1:]
	}
	o.pendingMethods[frame.method]--
	if o.pendingMethods[frame.method] == 0 {
		delete(o.pendingMethods, frame.method)
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
	parsed, err := parseFrameJSON(frame)
	if err != nil {
		return err
	}
	return o.inboundFrame(parsed)
}

func (o *protocolObserver) inboundFrame(frame validatedFrame) error {
	if len(frame.id) == 0 {
		return nil
	}
	id, err := canonicalProtocolID(frame.id)
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
	if len(frame.err) != 0 && !bytes.Equal(frame.err, []byte("null")) {
		response.err = fmt.Errorf("downstream JSON-RPC error")
	} else if len(frame.result) == 0 {
		response.err = fmt.Errorf("downstream JSON-RPC response omitted result")
	} else {
		response.result = bytes.Clone(frame.result)
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
	mu          sync.Mutex
	reader      *bufio.Reader
	closer      io.Closer
	observer    func(validatedFrame) error
	transformer func(validatedFrame) ([]byte, error)
	rejector    func(validatedFrame, error) (bool, error)
	current     *bytes.Reader
	currentBody []byte
	reusable    []byte
	failed      error
}

func newStrictFrameReader(reader io.ReadCloser, observer func(validatedFrame) error) *strictFrameReader {
	return &strictFrameReader{reader: bufio.NewReaderSize(reader, 64<<10), closer: reader, observer: observer}
}

func newStrictTransformingFrameReader(
	reader io.ReadCloser,
	transformer func(validatedFrame) ([]byte, error),
	rejector func(validatedFrame, error) (bool, error),
) *strictFrameReader {
	return &strictFrameReader{
		reader: bufio.NewReaderSize(reader, 64<<10), closer: reader,
		transformer: transformer, rejector: rejector,
	}
}

func (r *strictFrameReader) Read(output []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return 0, r.failed
	}
	for r.current == nil || r.current.Len() == 0 {
		r.releaseCurrent()
		frame, err := readFrame(r.reader, r.reusable)
		r.reusable = nil
		if err != nil {
			r.failed = err
			return 0, err
		}
		body := frame[:len(frame)-1]
		parsed, err := parseFrameJSON(body)
		if err != nil {
			r.failed = err
			return 0, err
		}
		if r.observer != nil {
			if err := r.observer(parsed); err != nil {
				r.failed = err
				return 0, err
			}
		}
		if r.transformer != nil {
			transformed, transformErr := r.transformer(parsed)
			if transformErr != nil {
				if r.rejector != nil {
					handled, rejectErr := r.rejector(parsed, transformErr)
					if rejectErr != nil {
						err = errors.Join(transformErr, rejectErr)
						r.failed = err
						return 0, err
					}
					if handled {
						r.currentBody = frame
						r.releaseCurrent()
						continue
					}
				}
				r.failed = transformErr
				return 0, transformErr
			}
			if bytes.Equal(body, transformed) {
				transformed = body
			}
			body = transformed
			if len(body)+1 > MaxProtocolFrameBytes {
				err = fmt.Errorf("transformed MCP frame exceeds %d bytes", MaxProtocolFrameBytes)
				r.failed = err
				return 0, err
			}
			if !bytes.Equal(body, parsed.raw) {
				if _, err = parseFrameJSON(body); err != nil {
					r.failed = err
					return 0, err
				}
			}
			if len(body) == len(parsed.raw) && len(body) > 0 && &body[0] == &parsed.raw[0] {
				frame = frame[:len(body)+1]
			} else {
				frame = append(bytes.Clone(body), '\n')
			}
		}
		r.currentBody = frame
		r.current = bytes.NewReader(frame)
	}
	return r.current.Read(output)
}

func (r *strictFrameReader) Close() error {
	err := r.closer.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseCurrent()
	clear(r.reusable)
	r.reusable = nil
	return err
}

func (r *strictFrameReader) releaseCurrent() {
	if r.currentBody == nil {
		return
	}
	clear(r.currentBody)
	if cap(r.currentBody) <= maxRetainedFrameBufferBytes {
		r.reusable = r.currentBody[:0]
	}
	r.currentBody = nil
}

func readFrame(reader *bufio.Reader, frame []byte) ([]byte, error) {
	frame = frame[:0]
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
	observer func(validatedFrame) error
	pending  []byte
	failed   error
}

func (w *strictFrameWriter) writeUnobservedFrame(frame []byte) error {
	parsed, err := parseFrameJSON(frame)
	if err != nil {
		return err
	}
	if parsed.method != "" || len(parsed.id) == 0 || len(parsed.err) == 0 {
		return fmt.Errorf("local MCP response is not a JSON-RPC error")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed != nil {
		return w.failed
	}
	framed := append(bytes.Clone(frame), '\n')
	count, writeErr := w.writer.Write(framed)
	if count < 0 || count > len(framed) {
		writeErr = errors.Join(writeErr, fmt.Errorf("MCP writer returned an invalid byte count"))
	}
	if writeErr == nil && count != len(framed) {
		writeErr = io.ErrShortWrite
	}
	clear(framed)
	if writeErr != nil {
		w.failed = writeErr
	}
	return writeErr
}

func newStrictFrameWriter(writer io.WriteCloser, observer func(validatedFrame) error) *strictFrameWriter {
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
		parsed, err := parseFrameJSON(frame)
		if err != nil {
			w.failed = err
			return written, err
		}
		if w.observer != nil {
			if err := w.observer(parsed); err != nil {
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
		clear(w.pending)
		if cap(w.pending) > maxRetainedFrameBufferBytes {
			w.pending = nil
		} else {
			w.pending = w.pending[:0]
		}
	}
	return written, nil
}

func (w *strictFrameWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) != 0 && w.failed == nil {
		w.failed = fmt.Errorf("MCP output ended with a partial frame")
	}
	clear(w.pending)
	w.pending = nil
	return errors.Join(w.failed, w.closer.Close())
}

type validatedFrame struct {
	raw    []byte
	id     json.RawMessage
	method string
	params json.RawMessage
	result json.RawMessage
	err    json.RawMessage
}

type frameScanner struct {
	decoder *jsontext.Decoder
	items   int
}

func validateFrameJSON(frame []byte) error {
	_, err := parseFrameJSON(frame)
	return err
}

func parseFrameJSON(frame []byte) (validatedFrame, error) {
	if len(frame) == 0 || len(frame) > MaxProtocolFrameBytes {
		return validatedFrame{}, fmt.Errorf("MCP frame is empty, oversized, or invalid UTF-8")
	}
	if bytes.ContainsAny(frame, "\r\n") {
		return validatedFrame{}, fmt.Errorf("MCP frame contains an embedded line delimiter")
	}
	scanner := frameScanner{decoder: jsontext.NewDecoder(bytes.NewReader(frame))}
	if scanner.decoder.PeekKind() != jsontext.KindBeginObject {
		return validatedFrame{}, fmt.Errorf("MCP frame root must be a JSON object")
	}
	if _, err := scanner.readToken(); err != nil {
		return validatedFrame{}, err
	}
	parsed := validatedFrame{raw: frame}
	for scanner.decoder.PeekKind() != jsontext.KindEndObject {
		name, err := scanner.readToken()
		if err != nil {
			return validatedFrame{}, err
		}
		if name.Kind() != jsontext.KindString {
			return validatedFrame{}, fmt.Errorf("decode MCP frame: object name is not a string")
		}
		field := name.String()
		if len(field) > action.MaxJSONStringBytes {
			return validatedFrame{}, fmt.Errorf("MCP frame string exceeds the byte boundary")
		}
		start, err := frameValueStart(frame, int(scanner.decoder.InputOffset()))
		if err != nil {
			return validatedFrame{}, err
		}
		kind, text, err := scanner.scanValue(1)
		if err != nil {
			return validatedFrame{}, err
		}
		end := int(scanner.decoder.InputOffset())
		raw := json.RawMessage(frame[start:end])
		switch field {
		case "id":
			parsed.id = raw
		case "method":
			if kind == jsontext.KindString {
				parsed.method = text
			}
		case "params":
			parsed.params = raw
		case "result":
			parsed.result = raw
		case "error":
			parsed.err = raw
		}
	}
	if _, err := scanner.readToken(); err != nil {
		return validatedFrame{}, err
	}
	if _, err := scanner.decoder.ReadToken(); err != io.EOF {
		if err == nil {
			return validatedFrame{}, fmt.Errorf("MCP frame contains trailing data")
		}
		return validatedFrame{}, fmt.Errorf("decode MCP frame: %w", err)
	}
	return parsed, nil
}

func (s *frameScanner) readToken() (jsontext.Token, error) {
	token, err := s.decoder.ReadToken()
	if err != nil {
		return jsontext.Token{}, fmt.Errorf("decode MCP frame: %w", err)
	}
	s.items++
	if s.items > action.MaxJSONItems+4096 {
		return jsontext.Token{}, fmt.Errorf("MCP frame exceeds the JSON item boundary")
	}
	if token.Kind() == jsontext.KindString && len(token.String()) > action.MaxJSONStringBytes {
		return jsontext.Token{}, fmt.Errorf("MCP frame string exceeds the byte boundary")
	}
	if token.Kind() == jsontext.KindNumber && len(token.String()) > maxFrameNumberBytes {
		return jsontext.Token{}, fmt.Errorf("MCP frame number exceeds the byte boundary")
	}
	return token, nil
}

func (s *frameScanner) scanValue(depth int) (jsontext.Kind, string, error) {
	kind := s.decoder.PeekKind()
	if kind == jsontext.KindBeginObject || kind == jsontext.KindBeginArray {
		if depth >= action.MaxJSONDepth+8 {
			return jsontext.KindInvalid, "", fmt.Errorf("MCP frame exceeds the JSON depth boundary")
		}
		if _, err := s.readToken(); err != nil {
			return jsontext.KindInvalid, "", err
		}
		end := jsontext.KindEndObject
		if kind == jsontext.KindBeginArray {
			end = jsontext.KindEndArray
		}
		for s.decoder.PeekKind() != end {
			if kind == jsontext.KindBeginObject {
				name, err := s.readToken()
				if err != nil {
					return jsontext.KindInvalid, "", err
				}
				if name.Kind() != jsontext.KindString {
					return jsontext.KindInvalid, "", fmt.Errorf("decode MCP frame: object name is not a string")
				}
			}
			if _, _, err := s.scanValue(depth + 1); err != nil {
				return jsontext.KindInvalid, "", err
			}
		}
		_, err := s.readToken()
		return kind, "", err
	}
	token, err := s.readToken()
	if err != nil {
		return jsontext.KindInvalid, "", err
	}
	return token.Kind(), token.String(), nil
}

func frameValueStart(frame []byte, offset int) (int, error) {
	for offset < len(frame) && (frame[offset] == ' ' || frame[offset] == '\t') {
		offset++
	}
	if offset >= len(frame) || frame[offset] != ':' {
		return 0, fmt.Errorf("decode MCP frame: object member separator is absent")
	}
	offset++
	for offset < len(frame) && (frame[offset] == ' ' || frame[offset] == '\t') {
		offset++
	}
	return offset, nil
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
