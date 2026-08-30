package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
)

type sdkDownstream struct {
	session      *mcp.ClientSession
	observer     *protocolObserver
	protocol     string
	progressMu   sync.Mutex
	progress     map[string]ProgressSink
	progressNext atomic.Uint64
}

func newSDKDownstream(
	ctx context.Context,
	process *ownedProcess,
	version string,
	onToolListChanged func(context.Context),
) (Downstream, error) {
	if process == nil || process.stdin == nil || process.stdout == nil {
		return nil, fmt.Errorf("downstream process transport is unavailable")
	}
	downstream := &sdkDownstream{
		observer: newProtocolObserver(), progress: make(map[string]ProgressSink),
	}
	reader := newStrictFrameReader(process.stdout, downstream.observeInbound)
	writer := newStrictFrameWriter(process.stdin, downstream.observer.outboundFrame)
	client := mcp.NewClient(
		&mcp.Implementation{Name: "reconc-gateway", Title: "Reconc MCP Gateway", Version: version},
		&mcp.ClientOptions{
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
			Capabilities:   &mcp.ClientCapabilities{},
			MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
			ToolListChangedHandler: func(changeCtx context.Context, _ *mcp.ToolListChangedRequest) {
				if onToolListChanged != nil {
					onToolListChanged(changeCtx)
				}
			},
		},
	)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: reader, Writer: writer}, nil)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, wrapBoundaryError("initialize downstream MCP session", err)
	}
	initialize := session.InitializeResult()
	if initialize == nil || initialize.ProtocolVersion == "" {
		_ = session.Close()
		return nil, fmt.Errorf("downstream MCP protocol version is unavailable")
	}
	downstream.session = session
	downstream.protocol = initialize.ProtocolVersion
	return downstream, nil
}

func (d *sdkDownstream) ProtocolVersion() string {
	if d == nil {
		return ""
	}
	return d.protocol
}

func (d *sdkDownstream) ListTools(ctx context.Context, cursor string) (ToolPage, error) {
	if d == nil || d.session == nil || d.observer == nil {
		return ToolPage{}, fmt.Errorf("downstream MCP session is unavailable")
	}
	params := &mcp.ListToolsParams{Cursor: cursor}
	call, err := d.observer.begin("tools/list", params, nil)
	if err != nil {
		return ToolPage{}, err
	}
	result, err := d.session.ListTools(ctx, params)
	if err != nil {
		d.observer.cancel(call)
		return ToolPage{}, wrapBoundaryError("list downstream tools", err)
	}
	raw, observeErr := d.observer.wait(ctx, call)
	if observeErr != nil {
		return ToolPage{}, observeErr
	}
	page, err := decodeToolPage(raw)
	if err != nil {
		return ToolPage{}, err
	}
	if result == nil || result.NextCursor != page.NextCursor || len(result.Tools) != len(page.Tools) {
		return ToolPage{}, fmt.Errorf("SDK tool page differs from the strict wire observation")
	}
	return page, nil
}

func (d *sdkDownstream) CallTool(
	ctx context.Context,
	name string,
	arguments json.RawMessage,
	progress ProgressSink,
) (CallResult, error) {
	if d == nil || d.session == nil || d.observer == nil {
		return CallResult{}, fmt.Errorf("downstream MCP session is unavailable")
	}
	params := &mcp.CallToolParams{Name: name, Arguments: arguments}
	progressToken, err := d.registerProgress(progress)
	if err != nil {
		return CallResult{}, err
	}
	if progressToken != "" {
		params.SetProgressToken(progressToken)
		defer d.unregisterProgress(progressToken)
	}
	call, err := d.observer.begin("tools/call", params, func() {
		d.unregisterProgress(progressToken)
	})
	if err != nil {
		return CallResult{}, err
	}
	result, err := d.session.CallTool(ctx, params)
	if err != nil {
		d.observer.cancel(call)
		return CallResult{}, wrapBoundaryError("call downstream tool", err)
	}
	raw, observeErr := d.observer.wait(ctx, call)
	if observeErr != nil {
		return CallResult{}, observeErr
	}
	if result == nil || result.NeedsInput() {
		return CallResult{}, fmt.Errorf("downstream tool returned unsupported input-required state")
	}
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return CallResult{}, fmt.Errorf("decode strict downstream tool result: %s", action.JSONErrorKindOf(err))
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return CallResult{}, fmt.Errorf("canonicalize downstream tool result: %w", err)
	}
	return CallResult{Canonical: canonical, Protocol: d.protocol}, nil
}

func (d *sdkDownstream) registerProgress(sink ProgressSink) (string, error) {
	if sink == nil {
		return "", nil
	}
	sequence := d.progressNext.Add(1)
	if sequence == 0 {
		return "", fmt.Errorf("downstream progress identity space is exhausted")
	}
	token := "reconc-progress-" + strconv.FormatUint(sequence, 10)
	d.progressMu.Lock()
	defer d.progressMu.Unlock()
	if _, duplicate := d.progress[token]; duplicate {
		return "", fmt.Errorf("downstream progress identity is duplicated")
	}
	d.progress[token] = sink
	return token, nil
}

func (d *sdkDownstream) unregisterProgress(token string) {
	if token == "" {
		return
	}
	d.progressMu.Lock()
	delete(d.progress, token)
	d.progressMu.Unlock()
}

func (d *sdkDownstream) observeInbound(frame validatedFrame) error {
	if err := d.observer.inboundFrame(frame); err != nil {
		return err
	}
	d.routeProgressFrame(frame)
	return nil
}

func (d *sdkDownstream) routeProgress(frame []byte) {
	parsed, err := parseFrameJSON(frame)
	if err != nil {
		return
	}
	d.routeProgressFrame(parsed)
}

func (d *sdkDownstream) routeProgressFrame(frame validatedFrame) {
	if frame.method != "notifications/progress" || len(frame.id) != 0 || len(frame.params) == 0 {
		return
	}
	var tokenEnvelope struct {
		ProgressToken json.RawMessage `json:"progressToken"`
	}
	if err := json.Unmarshal(frame.params, &tokenEnvelope); err != nil {
		return
	}
	var token string
	if err := json.Unmarshal(tokenEnvelope.ProgressToken, &token); err != nil || token == "" {
		return
	}
	d.progressMu.Lock()
	sink := d.progress[token]
	d.progressMu.Unlock()
	if sink == nil {
		return
	}
	if err := sink(context.Background(), ProgressEvent{
		Params: frame.params, FrameBytes: uint64(len(frame.raw)),
	}); err != nil {
		d.unregisterProgress(token)
	}
}

func (d *sdkDownstream) Close() error {
	if d == nil || d.session == nil {
		return nil
	}
	return wrapBoundaryError("close downstream MCP session", d.session.Close())
}

func (d *sdkDownstream) Wait() error {
	if d == nil || d.session == nil {
		return nil
	}
	return wrapBoundaryError("wait for downstream MCP session", d.session.Wait())
}

func decodeToolPage(raw []byte) (ToolPage, error) {
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return ToolPage{}, fmt.Errorf("decode strict downstream tool page: %s", action.JSONErrorKindOf(err))
	}
	toolsValue, ok := value.Lookup("tools")
	if !ok {
		return ToolPage{}, fmt.Errorf("downstream tool page omitted tools")
	}
	items, ok := toolsValue.Items()
	if !ok || len(items) > MaxToolsPerPage {
		return ToolPage{}, fmt.Errorf("downstream tool page contains an invalid tool array")
	}
	page := ToolPage{Tools: make([]json.RawMessage, len(items))}
	for index, item := range items {
		if item.Kind() != action.ValueObject {
			return ToolPage{}, fmt.Errorf("downstream tool definition %d is not an object", index+1)
		}
		body, encodeErr := item.MarshalJSON()
		if encodeErr != nil {
			return ToolPage{}, encodeErr
		}
		page.Tools[index] = body
	}
	if cursor, exists := value.Lookup("nextCursor"); exists {
		page.NextCursor, ok = cursor.Text()
		if !ok || page.NextCursor == "" || len(page.NextCursor) > 4096 {
			return ToolPage{}, fmt.Errorf("downstream nextCursor is invalid")
		}
	}
	return page, nil
}

var defaultDownstreamFactory downstreamFactory = newSDKDownstream
