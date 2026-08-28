// Package mcpgateway owns the enforcing, tools-only MCP stdio process
// boundary. No MCP SDK type crosses this package boundary.
package mcpgateway

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/actionstate"
)

const (
	MaxProtocolFrameBytes   = 10 << 20
	MaxProtocolIDBytes      = 4096
	MaxToolPages            = 64
	MaxToolsPerPage         = 128
	MaxTools                = 512
	MaxToolTitleBytes       = 4096
	MaxToolDescriptionBytes = 64 << 10
	MaxToolMetadataBytes    = 256 << 10
	MaxCatalogBytes         = 8 << 20
	MaxIconURIBytes         = 64 << 10
	MaxIconPayloadBytes     = 48 << 10
	MaxConcurrentCalls      = 4
	// MaxReservationConflictRetries bounds optimistic action-state retries
	// independently of the number of calls admitted by the gateway.
	MaxReservationConflictRetries = 8
	MaxPendingApprovals           = actionstate.MaxPendingApprovals
	MaxUpstreamRequests           = 64
	MaxStderrBytes                = 256 << 10
	MaxProgressTokenBytes         = 4096
	MaxProgressEvents             = 128
	MaxProgressQueueEvents        = 16
	MaxProgressEventBytes         = 64 << 10
	MaxProgressBytes              = 1 << 20
	MaxDiagnosticBytes            = 4096

	StartupTimeout       = 15 * time.Second
	ToolPageTimeout      = 5 * time.Second
	ToolDiscoveryTimeout = 30 * time.Second
	ResampleTimeout      = 2 * time.Second
	EvaluationTimeout    = 500 * time.Millisecond
	ProgressEventTimeout = 250 * time.Millisecond
	ProgressTotalTimeout = time.Second
	DefaultCallTimeout   = 60 * time.Second
	MaximumCallTimeout   = 300 * time.Second
	CancellationGrace    = 2 * time.Second
	ShutdownTimeout      = 5 * time.Second
	ChildKillGrace       = 2 * time.Second
)

type Config struct {
	Repository          string
	ServerLabel         string
	PolicyAuthority     actionstate.PolicyAuthority
	Principal           string
	Role                string
	Environment         string
	CredentialLabels    []string
	RunID               string
	SessionID           string
	ApprovalAuthorities string
	ApprovalPolicyID    string
	ServerWorkingDir    string
	InheritedEnvNames   []string
	CallTimeout         time.Duration
	Command             string
	Arguments           []string
	ReconcHome          string
	Version             string
	Input               io.Reader
	Output              io.Writer
	Diagnostics         io.Writer
	PolicyLoader        PolicyLoader
	EvidenceProvider    EvidenceProvider
}

type PolicySnapshot struct {
	Repository   string
	Evaluator    *action.Evaluator
	Plan         *action.CompiledPlan
	SourceDigest string
	LockDigest   string
}

type PolicyLoader interface {
	Load(context.Context, string) (PolicySnapshot, error)
}

type EvidenceSnapshot struct {
	Taint            action.TaintSnapshot
	RepositoryEffect *action.RepositoryEffectCandidate
	RepositoryPaths  []RepositoryPathBinding
}

type RepositoryPathBinding struct {
	Lexical  string
	Identity string
}

type EvidenceProvider interface {
	Observe(context.Context, PolicySnapshot, action.Request, action.Tool) (EvidenceSnapshot, error)
}

type ToolContract struct {
	Name           string
	Canonical      json.RawMessage
	ContractDigest string
	InputSchema    *actioninspect.OutputSchema
	OutputSchema   *actioninspect.OutputSchema
}

type ToolPage struct {
	Tools      []json.RawMessage
	NextCursor string
}

type CallResult struct {
	Canonical json.RawMessage
	Protocol  string
}

type ProgressEvent struct {
	Params     json.RawMessage
	FrameBytes uint64
}

type ProgressSink func(context.Context, ProgressEvent) error

type Downstream interface {
	ProtocolVersion() string
	ListTools(context.Context, string) (ToolPage, error)
	CallTool(context.Context, string, json.RawMessage, ProgressSink) (CallResult, error)
	Close() error
	Wait() error
}

type downstreamFactory func(
	context.Context,
	*ownedProcess,
	string,
	func(context.Context),
) (Downstream, error)
