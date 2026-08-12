// Command langchain-fixture is a deterministic Go MCP server used only by the
// disposable LangChain interoperability proof. It is not a Reconc adapter or
// release artifact.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fixtureVersion = "1"

var fixtureInputSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
var fixtureOutputSchema = json.RawMessage(`{"type":"object","properties":{"echo":{"type":"string"}},"required":["echo"],"additionalProperties":false}`)

type fixtureOptions struct {
	eventsPath       string
	cancellationPath string
}

type fixtureArguments struct {
	Value string `json:"value"`
}

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintln(os.Stderr, "langchain-fixture:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := mcp.NewServer(&mcp.Implementation{
		Name: "reconc-langchain-fixture", Version: fixtureVersion,
	}, nil)
	for _, name := range []string{
		"approval", "blocked", "budgeted", "echo", "slow", "tool-error", "warn", "withheld",
	} {
		tool := &mcp.Tool{
			Name: name, Description: "Deterministic Reconc interoperability fixture.",
			InputSchema: fixtureInputSchema,
		}
		if name == "echo" {
			destructive, openWorld := false, false
			tool.OutputSchema = fixtureOutputSchema
			tool.Annotations = &mcp.ToolAnnotations{
				ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &openWorld,
				DestructiveHint: &destructive,
			}
		}
		server.AddTool(tool, fixtureHandler(options, name))
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func parseOptions(args []string) (fixtureOptions, error) {
	var options fixtureOptions
	set := flag.NewFlagSet("langchain-fixture", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&options.eventsPath, "events", "", "absolute invocation event path")
	set.StringVar(&options.cancellationPath, "cancellation", "", "absolute cancellation marker path")
	if err := set.Parse(args); err != nil {
		return fixtureOptions{}, err
	}
	if set.NArg() != 0 {
		return fixtureOptions{}, fmt.Errorf("positional arguments are not accepted")
	}
	if options.eventsPath == "" || !filepath.IsAbs(options.eventsPath) {
		return fixtureOptions{}, fmt.Errorf("--events must be an absolute path")
	}
	if options.cancellationPath == "" || !filepath.IsAbs(options.cancellationPath) {
		return fixtureOptions{}, fmt.Errorf("--cancellation must be an absolute path")
	}
	return options, nil
}

func fixtureHandler(options fixtureOptions, name string) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := appendEvent(options.eventsPath, name); err != nil {
			return nil, err
		}
		var arguments fixtureArguments
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, fmt.Errorf("decode fixture arguments: %w", err)
		}
		if name == "slow" {
			<-ctx.Done()
			if err := appendEvent(options.cancellationPath, "cancelled"); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		}
		if name == "tool-error" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "downstream-tool-error"}},
				IsError: true,
			}, nil
		}
		if name == "withheld" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "sensitive-downstream-result"}},
			}, nil
		}
		if token := request.Params.GetProgressToken(); token != nil {
			for index := 1; index <= 2; index++ {
				if err := request.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
					ProgressToken: token, Message: fmt.Sprintf("step %d", index),
					Progress: float64(index), Total: 2,
				}); err != nil {
					return nil, fmt.Errorf("notify fixture progress: %w", err)
				}
			}
		}
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "fixture:" + name + ":" + arguments.Value}},
		}
		if name == "echo" {
			result.StructuredContent = map[string]string{"echo": arguments.Value}
		}
		return result, nil
	}
}

func appendEvent(path, event string) (resultErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open fixture event path: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	if _, err := fmt.Fprintln(file, event); err != nil {
		return fmt.Errorf("append fixture event: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync fixture event: %w", err)
	}
	return nil
}
