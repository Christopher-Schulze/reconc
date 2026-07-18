package grokacp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
)

const maxACPMessageBytes = 16 << 20

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcOutcome struct {
	result json.RawMessage
	err    error
}

type acpClient struct {
	writer   io.WriteCloser
	onUpdate func(json.RawMessage)

	writeMu  sync.Mutex
	stateMu  sync.Mutex
	nextID   int64
	pending  map[string]chan rpcOutcome
	done     chan struct{}
	finalErr error
}

func newACPClient(reader io.Reader, writer io.WriteCloser, onUpdate func(json.RawMessage)) *acpClient {
	client := &acpClient{
		writer:   writer,
		onUpdate: onUpdate,
		nextID:   1,
		pending:  map[string]chan rpcOutcome{},
		done:     make(chan struct{}),
	}
	go client.readLoop(reader)
	return client
}

func (c *acpClient) request(ctx context.Context, method string, params interface{}, result interface{}) error {
	paramsBody, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s params: %w", method, err)
	}
	c.stateMu.Lock()
	if c.finalErr != nil {
		err := c.finalErr
		c.stateMu.Unlock()
		return err
	}
	id := c.nextID
	c.nextID++
	key := strconv.FormatInt(id, 10)
	response := make(chan rpcOutcome, 1)
	c.pending[key] = response
	c.stateMu.Unlock()

	message := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsBody,
	}
	if err := c.writeJSON(message); err != nil {
		c.removePending(key)
		return err
	}

	select {
	case <-ctx.Done():
		c.removePending(key)
		return ctx.Err()
	case <-c.done:
		select {
		case outcome := <-response:
			return decodeRPCOutcome(method, result, outcome)
		default:
		}
		c.removePending(key)
		return c.terminalError()
	case outcome := <-response:
		return decodeRPCOutcome(method, result, outcome)
	}
}

func (c *acpClient) readLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxACPMessageBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		var message rpcMessage
		if err := json.Unmarshal(line, &message); err != nil {
			c.finish(fmt.Errorf("decode Grok ACP message: %w", err))
			return
		}
		if message.Method != "" {
			if len(message.ID) != 0 && string(message.ID) != "null" {
				if err := c.writeServerMethodError(message.ID, message.Method); err != nil {
					c.finish(err)
					return
				}
				continue
			}
			if message.Method == "session/update" && c.onUpdate != nil {
				c.onUpdate(message.Params)
			}
			continue
		}
		if len(message.ID) == 0 {
			continue
		}
		key := string(message.ID)
		c.stateMu.Lock()
		response := c.pending[key]
		delete(c.pending, key)
		c.stateMu.Unlock()
		if response == nil {
			continue
		}
		if message.Error != nil {
			response <- rpcOutcome{err: fmt.Errorf("grok ACP error %d: %s", message.Error.Code, message.Error.Message)}
			continue
		}
		response <- rpcOutcome{result: message.Result}
	}
	if err := scanner.Err(); err != nil {
		c.finish(fmt.Errorf("read Grok ACP stream: %w", err))
		return
	}
	c.finish(io.EOF)
}

func (c *acpClient) writeServerMethodError(id json.RawMessage, method string) error {
	message := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcError        `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Error: rpcError{
			Code:    -32601,
			Message: "unsupported ACP client method: " + method,
		},
	}
	return c.writeJSON(message)
}

func (c *acpClient) writeJSON(value interface{}) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Grok ACP request: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := writeFull(c.writer, append(body, '\n')); err != nil {
		return fmt.Errorf("write Grok ACP request: %w", err)
	}
	return nil
}

func (c *acpClient) removePending(key string) {
	c.stateMu.Lock()
	delete(c.pending, key)
	c.stateMu.Unlock()
}

func (c *acpClient) finish(err error) {
	c.stateMu.Lock()
	if c.finalErr != nil {
		c.stateMu.Unlock()
		return
	}
	c.finalErr = err
	pending := c.pending
	c.pending = map[string]chan rpcOutcome{}
	close(c.done)
	c.stateMu.Unlock()
	for _, response := range pending {
		response <- rpcOutcome{err: err}
	}
}

func (c *acpClient) terminalError() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.finalErr == nil {
		return io.EOF
	}
	return c.finalErr
}

func decodeRPCOutcome(method string, result interface{}, outcome rpcOutcome) error {
	if outcome.err != nil {
		return outcome.err
	}
	if result == nil || len(outcome.result) == 0 || string(outcome.result) == "null" {
		return nil
	}
	if err := json.Unmarshal(outcome.result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}
