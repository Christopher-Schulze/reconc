package actioninspect

import (
	"errors"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

const (
	ProtocolCurrent             = "2026-07-28"
	ProtocolLegacy              = "2025-11-25"
	MaxOutputSchemaBytes        = 1 << 20
	MaxOutputSchemaItems        = 8192
	MaxOutputSchemaPatternBytes = 4096
	MaxMCPMetadataBytes         = 1 << 20
	MaxMCPContentBlocks         = 4096
	// MaxMCPBinaryDecodedBytes is the largest payload whose canonical base64
	// encoding fits action.MaxJSONStringBytes exactly.
	MaxMCPBinaryDecodedBytes = 3 << 20
)

var (
	ErrMalformedResult       = errors.New("malformed MCP tool result")
	ErrUnsupportedResultType = errors.New("unsupported MCP result type")
	errInspectionLimit       = errors.New("inspection limit exceeded")
)

type ContentBlock struct {
	Type            action.ContentType
	Pointer         string
	CoveragePointer string
	Text            string
	Binary          []byte
	MIMEType        string
}

// MCPToolResult is a strict, transient representation of one official MCP
// tools/call result. Root retains the canonical JSON value only until the
// gateway forwards or withholds the result.
type MCPToolResult struct {
	Root                 action.Value
	ResultType           string
	Content              []ContentBlock
	AnnotationFields     []string
	MetadataPointers     []string
	StructuredContent    action.Value
	HasStructuredContent bool
	IsError              bool
}

func (r *MCPToolResult) Release() {
	if r == nil {
		return
	}
	for index := range r.Content {
		for offset := range r.Content[index].Binary {
			r.Content[index].Binary[offset] = 0
		}
		r.Content[index] = ContentBlock{}
	}
	r.Root = action.Value{}
	r.StructuredContent = action.Value{}
	r.Content = nil
	r.AnnotationFields = nil
	r.MetadataPointers = nil
	r.ResultType = ""
	r.HasStructuredContent = false
	r.IsError = false
}

type OutputSchema struct {
	identity string
	schema   *jsonschema.Schema
	items    int
}

func (s *OutputSchema) Identity() string {
	if s == nil {
		return "absent"
	}
	return s.identity
}

type Finding struct {
	RuleID   string
	Category action.DetectorCategory
}

type IdentityKey interface {
	ID() string
	Identity(actionstate.IdentityDomain, ...[]byte) string
}
