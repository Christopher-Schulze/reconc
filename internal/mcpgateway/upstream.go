package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

type upstreamWireCall struct {
	id     json.RawMessage
	params json.RawMessage
}

const upstreamCorrelationMetaKey = "io.reconc/gatewayCorrelation"

type upstreamObserver struct {
	mu              sync.Mutex
	active          map[string]struct{}
	byCorrelation   map[string]upstreamWireCall
	correlationByID map[string]string
	drained         chan struct{}
	next            uint64
}

func newUpstreamObserver() *upstreamObserver {
	drained := make(chan struct{})
	close(drained)
	return &upstreamObserver{
		active: make(map[string]struct{}), byCorrelation: make(map[string]upstreamWireCall),
		correlationByID: make(map[string]string), drained: drained,
	}
}

func (o *upstreamObserver) instrumentInbound(frame []byte) ([]byte, error) {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil || envelope.Method == "" || len(envelope.ID) == 0 {
		return bytes.Clone(frame), nil
	}
	id, err := canonicalProtocolID(envelope.ID)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	if _, duplicate := o.active[id]; duplicate {
		o.mu.Unlock()
		return nil, fmt.Errorf("duplicate active upstream JSON-RPC request ID")
	}
	if len(o.active) >= MaxUpstreamRequests {
		o.mu.Unlock()
		return nil, fmt.Errorf("active upstream JSON-RPC requests exceed %d", MaxUpstreamRequests)
	}
	if len(o.active) == 0 {
		o.drained = make(chan struct{})
	}
	o.active[id] = struct{}{}
	if envelope.Method != "tools/call" {
		o.mu.Unlock()
		return bytes.Clone(frame), nil
	}
	if o.next == ^uint64(0) {
		o.deleteActiveLocked(id)
		o.mu.Unlock()
		return nil, fmt.Errorf("upstream correlation identity space is exhausted")
	}
	o.next++
	correlation := strconv.FormatUint(o.next, 10)
	o.mu.Unlock()
	transformed, err := injectUpstreamCorrelation(frame, correlation)
	if err != nil || len(transformed)+1 > MaxProtocolFrameBytes {
		o.mu.Lock()
		o.deleteActiveLocked(id)
		o.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("instrumented upstream MCP frame exceeds %d bytes", MaxProtocolFrameBytes)
	}
	o.mu.Lock()
	o.byCorrelation[correlation] = upstreamWireCall{
		id: bytes.Clone(envelope.ID), params: bytes.Clone(envelope.Params),
	}
	o.correlationByID[id] = correlation
	o.mu.Unlock()
	return transformed, nil
}

func injectUpstreamCorrelation(frame []byte, correlation string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return nil, fmt.Errorf("decode upstream request for correlation: %w", err)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(envelope["params"], &params); err != nil || params == nil {
		return nil, fmt.Errorf("upstream tool params are not an object")
	}
	metadata := make(map[string]json.RawMessage)
	if raw, exists := params["_meta"]; exists && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
			return nil, fmt.Errorf("upstream tool metadata is not an object")
		}
	}
	if _, reserved := metadata[upstreamCorrelationMetaKey]; reserved {
		return nil, fmt.Errorf("upstream tool metadata uses a reserved Reconc correlation key")
	}
	token, err := json.Marshal(correlation)
	if err != nil {
		return nil, fmt.Errorf("encode upstream correlation: %w", err)
	}
	metadata[upstreamCorrelationMetaKey] = token
	params["_meta"], err = json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode instrumented upstream metadata: %w", err)
	}
	envelope["params"], err = json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode instrumented upstream params: %w", err)
	}
	transformed, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode instrumented upstream request: %w", err)
	}
	return transformed, nil
}

func (o *upstreamObserver) outbound(frame []byte) error {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil || len(envelope.ID) == 0 {
		return nil
	}
	id, err := canonicalProtocolID(envelope.ID)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.deleteActiveLocked(id)
	correlation := o.correlationByID[id]
	delete(o.correlationByID, id)
	delete(o.byCorrelation, correlation)
	o.mu.Unlock()
	return nil
}

func (o *upstreamObserver) deleteActiveLocked(id string) {
	if _, exists := o.active[id]; !exists {
		return
	}
	delete(o.active, id)
	if len(o.active) == 0 {
		close(o.drained)
	}
}

func (o *upstreamObserver) waitDrained(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if len(o.active) == 0 {
		o.mu.Unlock()
		return nil
	}
	drained := o.drained
	o.mu.Unlock()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *upstreamObserver) take(params *mcp.CallToolParamsRaw) (upstreamWireCall, error) {
	if o == nil || params == nil {
		return upstreamWireCall{}, fmt.Errorf("upstream wire request is unavailable")
	}
	correlationValue, exists := params.Meta[upstreamCorrelationMetaKey]
	correlation, valid := correlationValue.(string)
	if !exists || !valid || correlation == "" {
		return upstreamWireCall{}, fmt.Errorf("upstream wire request correlation is unavailable")
	}
	delete(params.Meta, upstreamCorrelationMetaKey)
	if len(params.Meta) == 0 {
		params.Meta = nil
	}
	o.mu.Lock()
	call, exists := o.byCorrelation[correlation]
	if exists {
		delete(o.byCorrelation, correlation)
	}
	o.mu.Unlock()
	if !exists {
		return upstreamWireCall{}, fmt.Errorf("upstream wire request correlation is unknown")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return upstreamWireCall{}, fmt.Errorf("encode SDK upstream params: %w", err)
	}
	got, err := canonicalParamsKey(body)
	if err != nil {
		return upstreamWireCall{}, err
	}
	want, err := canonicalParamsKey(call.params)
	if err != nil {
		return upstreamWireCall{}, err
	}
	if got != want {
		return upstreamWireCall{}, fmt.Errorf("strict upstream request does not match SDK params")
	}
	return call, nil
}

func canonicalParamsKey(raw []byte) (string, error) {
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return "", fmt.Errorf("decode strict upstream params: %s", action.JSONErrorKindOf(err))
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("canonicalize upstream params: %w", err)
	}
	return string(canonical), nil
}

type readCloser struct{ io.Reader }

func (r readCloser) Close() error {
	closer, ok := r.Reader.(io.Closer)
	if !ok {
		return nil
	}
	return closer.Close()
}

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }

type upstreamTransport struct{ *mcp.IOTransport }

func (*upstreamTransport) SupportsProtocolVersion(version string) bool {
	return gatewayProtocolSupported(version)
}

func (g *Gateway) serve() error {
	g.upstreamWire = newUpstreamObserver()
	upstream := mcp.NewServer(
		&mcp.Implementation{Name: "reconc", Title: "Reconc MCP Gateway", Version: g.config.Version},
		&mcp.ServerOptions{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{ListChanged: true},
			},
			PageSize: MaxToolsPerPage,
		},
	)
	upstream.AddReceivingMiddleware(gatewayProtocolMiddleware)
	g.upstreamMu.Lock()
	g.toolsMu.RLock()
	contracts := make([]ToolContract, 0, len(g.tools))
	for _, contract := range g.tools {
		contracts = append(contracts, contract)
	}
	g.toolsMu.RUnlock()
	g.upstream = upstream
	if err := g.replaceUpstreamToolsLocked(contracts); err != nil {
		g.upstream = nil
		g.upstreamMu.Unlock()
		return err
	}
	g.upstreamMu.Unlock()
	reader := newStrictTransformingFrameReader(
		readCloser{g.config.Input}, g.upstreamWire.instrumentInbound,
	)
	writer := newStrictFrameWriter(writeCloser{g.config.Output}, g.upstreamWire.outbound)
	session, err := upstream.Connect(
		g.ctx, &upstreamTransport{IOTransport: &mcp.IOTransport{Reader: reader, Writer: writer}}, nil,
	)
	if err != nil {
		return fmt.Errorf("connect upstream MCP session: %w", err)
	}
	g.sessionMu.Lock()
	g.session = session
	g.sessionMu.Unlock()
	type waitResult struct {
		owner string
		err   error
	}
	wait := make(chan waitResult, 3)
	go func() { wait <- waitResult{owner: "upstream", err: session.Wait()} }()
	go func() { wait <- waitResult{owner: "downstream", err: g.downstream.Wait()} }()
	go func() { wait <- waitResult{owner: "process", err: g.process.Wait()} }()
	select {
	case <-g.ctx.Done():
		return g.ctx.Err()
	case err := <-g.fatalErrors:
		if contextErr := g.ctx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	case result := <-wait:
		if result.owner == "upstream" && result.err == nil {
			return nil
		}
		lifecycleErr := gatewayLifecycleEndError(g.ctx, result.owner, result.err)
		if isNormalLifecycleError(lifecycleErr) {
			return lifecycleErr
		}
		g.lifecycleMu.Lock()
		g.closing = true
		g.lifecycleMu.Unlock()
		drainCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		if err := g.upstreamWire.waitDrained(drainCtx); err != nil {
			return errors.Join(lifecycleErr, fmt.Errorf("drain active upstream responses: %w", err))
		}
		return errors.Join(lifecycleErr, closeLifecycleError(session.Close()))
	}
}

func gatewayLifecycleEndError(ctx context.Context, owner string, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return fmt.Errorf("%s MCP lifecycle ended", owner)
	}
	return fmt.Errorf("%s MCP lifecycle ended: %w", owner, err)
}

func gatewayProtocolMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(
		ctx context.Context,
		method string,
		request mcp.Request,
	) (mcp.Result, error) {
		if method == "initialize" {
			params, ok := request.GetParams().(*mcp.InitializeParams)
			if !ok || params == nil || !gatewayProtocolSupported(params.ProtocolVersion) {
				requested := ""
				if ok && params != nil {
					requested = params.ProtocolVersion
				}
				data, err := json.Marshal(mcp.UnsupportedProtocolVersionData{
					Supported: append([]string(nil), supportedGatewayProtocols...),
					Requested: requested,
				})
				if err != nil {
					return nil, fmt.Errorf("encode unsupported protocol response: %w", err)
				}
				return nil, &jsonrpc.Error{
					Code: mcp.CodeUnsupportedProtocolVersion, Message: "unsupported protocol version",
					Data: data,
				}
			}
		}
		return next(ctx, method, request)
	}
}

func (g *Gateway) upstreamSession() *mcp.ServerSession {
	if g == nil {
		return nil
	}
	g.sessionMu.RLock()
	defer g.sessionMu.RUnlock()
	return g.session
}

func (g *Gateway) replaceUpstreamTools(contracts []ToolContract) error {
	if g == nil {
		return fmt.Errorf("upstream MCP server is unavailable")
	}
	g.upstreamMu.Lock()
	defer g.upstreamMu.Unlock()
	return g.replaceUpstreamToolsLocked(contracts)
}

func (g *Gateway) publishUpstreamTools(contracts []ToolContract) error {
	if g == nil {
		return nil
	}
	g.upstreamMu.Lock()
	defer g.upstreamMu.Unlock()
	if g.upstream == nil {
		return nil
	}
	return g.replaceUpstreamToolsLocked(contracts)
}

func (g *Gateway) replaceUpstreamToolsLocked(contracts []ToolContract) error {
	if g == nil || g.upstream == nil {
		return fmt.Errorf("upstream MCP server is unavailable")
	}
	byName := make(map[string]ToolContract, len(contracts))
	for _, contract := range contracts {
		byName[contract.Name] = contract
	}
	for name := range g.upstreamNames {
		if _, keep := byName[name]; !keep {
			g.upstream.RemoveTools(name)
		}
	}
	for _, contract := range contracts {
		tool, err := sdkToolFromContract(contract)
		if err != nil {
			return err
		}
		contractCopy := contract
		if err := addSDKTool(g.upstream, tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return g.handleTool(ctx, request, contractCopy)
		}); err != nil {
			return err
		}
	}
	g.upstreamNames = make(map[string]struct{}, len(contracts))
	for _, contract := range contracts {
		g.upstreamNames[contract.Name] = struct{}{}
	}
	return nil
}

func sdkToolFromContract(contract ToolContract) (*mcp.Tool, error) {
	var wire struct {
		Meta         json.RawMessage      `json:"_meta"`
		Annotations  *mcp.ToolAnnotations `json:"annotations"`
		Description  string               `json:"description"`
		Icons        []mcp.Icon           `json:"icons"`
		InputSchema  json.RawMessage      `json:"inputSchema"`
		Name         string               `json:"name"`
		OutputSchema json.RawMessage      `json:"outputSchema"`
		Title        string               `json:"title"`
	}
	if err := json.Unmarshal(contract.Canonical, &wire); err != nil {
		return nil, fmt.Errorf("decode validated tool %q for SDK: %w", contract.Name, err)
	}
	var metadata mcp.Meta
	if len(wire.Meta) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(wire.Meta))
		decoder.UseNumber()
		if err := decoder.Decode(&metadata); err != nil {
			return nil, fmt.Errorf("decode validated tool %q metadata: %w", contract.Name, err)
		}
	}
	tool := &mcp.Tool{
		Meta: metadata, Annotations: wire.Annotations, Description: wire.Description,
		Icons:       append([]mcp.Icon(nil), wire.Icons...),
		InputSchema: json.RawMessage(bytes.Clone(wire.InputSchema)),
		Name:        wire.Name, Title: wire.Title,
	}
	if len(wire.OutputSchema) != 0 {
		tool.OutputSchema = json.RawMessage(bytes.Clone(wire.OutputSchema))
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		return nil, fmt.Errorf("encode validated tool %q through SDK: %w", contract.Name, err)
	}
	want, err := action.ParseObjectJSON(contract.Canonical)
	if err != nil {
		return nil, fmt.Errorf("decode canonical tool %q: %w", contract.Name, err)
	}
	got, err := action.ParseObjectJSON(encoded)
	if err != nil || !got.Equal(want) {
		return nil, fmt.Errorf("SDK serialization changed validated tool contract %q", contract.Name)
	}
	return tool, nil
}

func addSDKTool(server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandler) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = fmt.Errorf("register validated MCP tool failed")
		}
	}()
	server.AddTool(tool, handler)
	return nil
}

func callToolResult(raw []byte) (*mcp.CallToolResult, error) {
	if err := validateFrameJSON(raw); err != nil {
		return nil, err
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("map validated MCP result into SDK: %w", err)
	}
	var wire struct {
		Meta              json.RawMessage `json:"_meta"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		Content           []struct {
			Meta     json.RawMessage `json:"_meta"`
			Resource *struct {
				Meta json.RawMessage `json:"_meta"`
			} `json:"resource"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("read validated MCP result precision fields: %w", err)
	}
	meta, err := decodeMCPMeta(wire.Meta)
	if err != nil {
		return nil, err
	}
	result.Meta = meta
	if len(wire.StructuredContent) != 0 {
		result.StructuredContent = json.RawMessage(bytes.Clone(wire.StructuredContent))
	}
	if len(wire.Content) != len(result.Content) {
		return nil, fmt.Errorf("validated MCP result content count changed during SDK mapping")
	}
	for index, content := range result.Content {
		contentMeta, decodeErr := decodeMCPMeta(wire.Content[index].Meta)
		if decodeErr != nil {
			return nil, decodeErr
		}
		switch typed := content.(type) {
		case *mcp.TextContent:
			typed.Meta = contentMeta
		case *mcp.ImageContent:
			typed.Meta = contentMeta
		case *mcp.AudioContent:
			typed.Meta = contentMeta
		case *mcp.ResourceLink:
			typed.Meta = contentMeta
		case *mcp.EmbeddedResource:
			typed.Meta = contentMeta
			if typed.Resource != nil && wire.Content[index].Resource != nil {
				resourceMeta, resourceErr := decodeMCPMeta(wire.Content[index].Resource.Meta)
				if resourceErr != nil {
					return nil, resourceErr
				}
				typed.Resource.Meta = resourceMeta
			}
		default:
			return nil, fmt.Errorf("validated MCP result contains unsupported SDK content")
		}
	}
	return &result, nil
}

func decodeMCPMeta(raw json.RawMessage) (mcp.Meta, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var meta mcp.Meta
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&meta); err != nil {
		return nil, fmt.Errorf("decode validated MCP metadata: %w", err)
	}
	return meta, nil
}

func safeGatewayResult(
	outcome string,
	reason action.ReasonCode,
	message, callID, dispatch, delivery string,
) *mcp.CallToolResult {
	type gatewayResultPayload struct {
		FormatVersion  string            `json:"format_version"`
		Outcome        string            `json:"outcome"`
		ReasonCode     action.ReasonCode `json:"reason_code"`
		Message        string            `json:"message"`
		CorrelationID  string            `json:"correlation_id"`
		DispatchStatus string            `json:"dispatch_status"`
		DeliveryStatus string            `json:"delivery_status"`
	}
	payload := gatewayResultPayload{
		FormatVersion: "1", Outcome: outcome, ReasonCode: reason, Message: message,
		CorrelationID: callID, DispatchStatus: dispatch, DeliveryStatus: delivery,
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: message}},
		StructuredContent: payload,
		IsError:           true,
	}
}

func sdkApprovalResult(input actionapproval.MCPInputRequiredResult) (*mcp.CallToolResult, error) {
	body, err := actionapproval.EncodeMCPInputRequired(input)
	if err != nil {
		return nil, err
	}
	return callToolResult(body)
}

func isNormalLifecycleError(err error) bool {
	return err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled)
}
