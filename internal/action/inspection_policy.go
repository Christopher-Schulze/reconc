package action

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const BuiltinDetectorPackID = "reconc-core-v1"

type DetectorCategory string

const (
	DetectorCredential          DetectorCategory = "credential"
	DetectorSecret              DetectorCategory = "secret"
	DetectorPIIEmail            DetectorCategory = "pii_email"
	DetectorPIIPhone            DetectorCategory = "pii_phone"
	DetectorPIIPaymentCard      DetectorCategory = "pii_payment_card"
	DetectorForbiddenData       DetectorCategory = "forbidden_data"
	DetectorPromptInjection     DetectorCategory = "prompt_injection"
	DetectorRoleOverride        DetectorCategory = "role_override"
	DetectorPrivilegeClaim      DetectorCategory = "privilege_claim"
	DetectorIndirectInstruction DetectorCategory = "indirect_instruction"
	DetectorDelimiterAttack     DetectorCategory = "delimiter_attack"
	DetectorExfiltration        DetectorCategory = "exfiltration"
)

func (c DetectorCategory) Valid() bool {
	switch c {
	case DetectorCredential, DetectorSecret, DetectorPIIEmail, DetectorPIIPhone,
		DetectorPIIPaymentCard, DetectorForbiddenData, DetectorPromptInjection,
		DetectorRoleOverride, DetectorPrivilegeClaim, DetectorIndirectInstruction,
		DetectorDelimiterAttack, DetectorExfiltration:
		return true
	default:
		return false
	}
}

type ResultDisposition string

const (
	ResultDispositionWarn          ResultDisposition = "warn"
	ResultDispositionWithhold      ResultDisposition = "withhold"
	ResultDispositionRequireSchema ResultDisposition = "require_schema"
)

func (d ResultDisposition) Valid() bool {
	return d == ResultDispositionWarn || d == ResultDispositionWithhold ||
		d == ResultDispositionRequireSchema
}

type ProgressDisposition string

const (
	ProgressDispositionForward  ProgressDisposition = "forward"
	ProgressDispositionSuppress ProgressDisposition = "suppress"
)

func (d ProgressDisposition) Valid() bool {
	return d == ProgressDispositionForward || d == ProgressDispositionSuppress
}

type SchemaPolicy string

const (
	SchemaValidateIfDeclared SchemaPolicy = "validate_if_declared"
	SchemaRequire            SchemaPolicy = "require"
)

func (p SchemaPolicy) Valid() bool {
	return p == SchemaValidateIfDeclared || p == SchemaRequire
}

type ContentType string

const (
	ContentText         ContentType = "text"
	ContentImage        ContentType = "image"
	ContentAudio        ContentType = "audio"
	ContentResourceText ContentType = "resource_text"
	ContentResourceBlob ContentType = "resource_blob"
	ContentResourceLink ContentType = "resource_link"
	ContentAnnotation   ContentType = "annotation"
	ContentMetadata     ContentType = "metadata"
	ContentStructured   ContentType = "structured_content"
	ContentUnknown      ContentType = "unknown"
)

func (t ContentType) Valid() bool {
	switch t {
	case ContentText, ContentImage, ContentAudio, ContentResourceText,
		ContentResourceBlob, ContentResourceLink, ContentAnnotation, ContentMetadata,
		ContentStructured, ContentUnknown:
		return true
	default:
		return false
	}
}

type DetectorField struct {
	Source  ValueSource `json:"source"`
	Pointer string      `json:"pointer"`
}

type InspectionLimits struct {
	MaxBytes        uint64 `json:"max_bytes"`
	MaxItems        uint32 `json:"max_items"`
	MaxDepth        uint32 `json:"max_depth"`
	MaxMilliseconds uint32 `json:"max_milliseconds"`
}

type DetectorPolicy struct {
	ID                      string              `json:"id"`
	Selector                Selector            `json:"selector"`
	PackID                  string              `json:"pack_id"`
	PackDigest              string              `json:"pack_digest"`
	Fields                  []DetectorField     `json:"fields"`
	Categories              []DetectorCategory  `json:"categories"`
	ForbiddenTerms          []string            `json:"forbidden_terms"`
	PreCallDecision         Decision            `json:"pre_call_decision"`
	PostResultDisposition   ResultDisposition   `json:"post_result_disposition"`
	ProgressDisposition     ProgressDisposition `json:"progress_disposition"`
	SchemaPolicy            SchemaPolicy        `json:"schema_policy"`
	AllowedContentTypes     []ContentType       `json:"allowed_content_types"`
	TrustedAnnotationFields []string            `json:"trusted_annotation_fields"`
	Limits                  InspectionLimits    `json:"limits"`
	SourceIdentity          string              `json:"source_identity"`
}

type CompiledDetectorField struct {
	Field  DetectorField
	Tokens []string
}

type CompiledDetectorPolicy struct {
	Policy DetectorPolicy
	Fields []CompiledDetectorField
}

func normalizeDetectorPolicy(
	policy *DetectorPolicy,
	tools []Tool,
	toolByID map[string]int,
) (CompiledDetectorPolicy, error) {
	if !SafeLabel(policy.ID) || !SafeLabel(policy.PackID) {
		return CompiledDetectorPolicy{}, fmt.Errorf("id and pack_id must be safe lower-kebab labels")
	}
	if !ValidSHA256Identity(policy.PackDigest) {
		return CompiledDetectorPolicy{}, fmt.Errorf("pack_digest must be sha256:<64-lowercase-hex>")
	}
	if selectorEmpty(policy.Selector) {
		return CompiledDetectorPolicy{}, fmt.Errorf("selector must contain at least one exact constraint")
	}
	if err := normalizeSelector(&policy.Selector, toolByID); err != nil {
		return CompiledDetectorPolicy{}, err
	}
	if len(policy.Selector.Phases) == 0 {
		return CompiledDetectorPolicy{}, fmt.Errorf("selector.phases must select at least one inspection phase")
	}
	for _, phase := range policy.Selector.Phases {
		if phase == PhaseObservation {
			return CompiledDetectorPolicy{}, fmt.Errorf("selector.phases cannot select observation")
		}
	}
	if len(policy.Fields) == 0 || len(policy.Fields) > MaxDetectorFields {
		return CompiledDetectorPolicy{}, fmt.Errorf("fields must contain 1 to %d selectors", MaxDetectorFields)
	}
	compiledFields, err := normalizeDetectorFields(policy.Fields, policy.Selector.Phases)
	if err != nil {
		return CompiledDetectorPolicy{}, err
	}
	policy.Fields = make([]DetectorField, len(compiledFields))
	for index := range compiledFields {
		policy.Fields[index] = compiledFields[index].Field
	}
	if err := normalizeDetectorCategories(&policy.Categories); err != nil {
		return CompiledDetectorPolicy{}, err
	}
	if err := normalizeForbiddenTerms(&policy.ForbiddenTerms, policy.Categories); err != nil {
		return CompiledDetectorPolicy{}, err
	}
	if policy.PreCallDecision == "" {
		policy.PreCallDecision = DecisionBlock
	}
	if policy.PreCallDecision != DecisionWarn && policy.PreCallDecision != DecisionBlock &&
		policy.PreCallDecision != DecisionRequireApproval {
		return CompiledDetectorPolicy{}, fmt.Errorf("pre_call_decision must be warn, block, or require_approval")
	}
	if policy.PostResultDisposition == "" {
		policy.PostResultDisposition = ResultDispositionWithhold
	}
	if !policy.PostResultDisposition.Valid() {
		return CompiledDetectorPolicy{}, fmt.Errorf("post_result_disposition is invalid")
	}
	if policy.ProgressDisposition == "" {
		policy.ProgressDisposition = ProgressDispositionSuppress
	}
	if !policy.ProgressDisposition.Valid() {
		return CompiledDetectorPolicy{}, fmt.Errorf("progress_disposition is invalid")
	}
	if policy.SchemaPolicy == "" {
		policy.SchemaPolicy = SchemaValidateIfDeclared
	}
	if !policy.SchemaPolicy.Valid() {
		return CompiledDetectorPolicy{}, fmt.Errorf("schema_policy is invalid")
	}
	if err := normalizeContentTypes(&policy.AllowedContentTypes); err != nil {
		return CompiledDetectorPolicy{}, err
	}
	if err := normalizeTrustedAnnotationFields(policy); err != nil {
		return CompiledDetectorPolicy{}, err
	}
	if err := normalizeInspectionLimits(&policy.Limits, policy.Selector.Phases); err != nil {
		return CompiledDetectorPolicy{}, err
	}
	if strings.TrimSpace(policy.SourceIdentity) == "" || !utf8.ValidString(policy.SourceIdentity) ||
		len(policy.SourceIdentity) > MaxPointerBytes {
		return CompiledDetectorPolicy{}, fmt.Errorf("source_identity must be a non-empty UTF-8 identity of at most %d bytes", MaxPointerBytes)
	}
	matched := 0
	for _, tool := range tools {
		if !selectorCanMatchTool(policy.Selector, tool) {
			continue
		}
		matched++
		if tool.Transport != TransportMCPStdio {
			return CompiledDetectorPolicy{}, fmt.Errorf("detector selects host_mcp tool %q without an inspection gateway", tool.ID)
		}
	}
	if matched == 0 {
		return CompiledDetectorPolicy{}, fmt.Errorf("selector cannot match any declared tool")
	}
	return CompiledDetectorPolicy{Policy: *policy, Fields: compiledFields}, nil
}

func normalizeDetectorFields(fields []DetectorField, phases []Phase) ([]CompiledDetectorField, error) {
	compiled := make([]CompiledDetectorField, len(fields))
	phaseSources := make(map[ValueSource]bool, len(phases))
	for _, phase := range phases {
		source := phaseSource(phase)
		phaseSources[source] = true
	}
	for index, field := range fields {
		if !field.Source.Valid() || field.Source == SourceContext || !phaseSources[field.Source] {
			return nil, fmt.Errorf("fields[%d].source is incompatible with selector.phases", index)
		}
		tokens, err := CompilePointer(field.Pointer)
		if err != nil {
			return nil, fmt.Errorf("fields[%d].pointer: %w", index, err)
		}
		compiled[index] = CompiledDetectorField{Field: field, Tokens: tokens}
	}
	sort.Slice(compiled, func(i, j int) bool {
		if compiled[i].Field.Source != compiled[j].Field.Source {
			return compiled[i].Field.Source < compiled[j].Field.Source
		}
		return compiled[i].Field.Pointer < compiled[j].Field.Pointer
	})
	for index := 1; index < len(compiled); index++ {
		if compiled[index-1].Field == compiled[index].Field {
			return nil, fmt.Errorf("fields contains duplicate source and pointer")
		}
	}
	for source := range phaseSources {
		found := false
		for _, field := range compiled {
			found = found || field.Field.Source == source
		}
		if !found {
			return nil, fmt.Errorf("fields must select at least one value for %s", source)
		}
	}
	return compiled, nil
}

func phaseSource(phase Phase) ValueSource {
	switch phase {
	case PhasePreCall:
		return SourceArguments
	case PhasePostResult:
		return SourceResult
	case PhaseProgress:
		return SourceProgress
	default:
		return ""
	}
}

func normalizeDetectorCategories(categories *[]DetectorCategory) error {
	if len(*categories) == 0 || len(*categories) > MaxDetectorCategories {
		return fmt.Errorf("categories must contain 1 to %d values", MaxDetectorCategories)
	}
	sort.Slice(*categories, func(i, j int) bool { return (*categories)[i] < (*categories)[j] })
	for index, category := range *categories {
		if !category.Valid() {
			return fmt.Errorf("categories contains invalid value %q", category)
		}
		if index > 0 && (*categories)[index-1] == category {
			return fmt.Errorf("categories contains duplicate value %q", category)
		}
	}
	return nil
}

func normalizeForbiddenTerms(terms *[]string, categories []DetectorCategory) error {
	hasCategory := false
	for _, category := range categories {
		hasCategory = hasCategory || category == DetectorForbiddenData
	}
	if hasCategory != (len(*terms) > 0) {
		return fmt.Errorf("forbidden_data requires non-empty forbidden_terms and forbids them otherwise")
	}
	if len(*terms) > MaxForbiddenTerms {
		return fmt.Errorf("forbidden_terms exceeds %d values", MaxForbiddenTerms)
	}
	for index := range *terms {
		raw := strings.TrimSpace((*terms)[index])
		if raw == "" || !utf8.ValidString(raw) || len(raw) > MaxRuleMessageBytes {
			return fmt.Errorf("forbidden_terms[%d] must contain 1 to %d UTF-8 bytes", index, MaxRuleMessageBytes)
		}
		value := strings.ToLower(norm.NFKC.String(raw))
		if len(value) > MaxRuleMessageBytes {
			return fmt.Errorf("forbidden_terms[%d] normalization exceeds %d UTF-8 bytes", index, MaxRuleMessageBytes)
		}
		(*terms)[index] = value
	}
	sort.Strings(*terms)
	for index := 1; index < len(*terms); index++ {
		if (*terms)[index-1] == (*terms)[index] {
			return fmt.Errorf("forbidden_terms contains duplicate value")
		}
	}
	if *terms == nil {
		*terms = []string{}
	}
	return nil
}

func normalizeContentTypes(types *[]ContentType) error {
	if len(*types) > MaxListValues {
		return fmt.Errorf("allowed_content_types exceeds %d values", MaxListValues)
	}
	sort.Slice(*types, func(i, j int) bool { return (*types)[i] < (*types)[j] })
	for index, contentType := range *types {
		if !contentType.Valid() || contentType == ContentAnnotation || contentType == ContentMetadata ||
			contentType == ContentStructured || contentType == ContentUnknown {
			return fmt.Errorf("allowed_content_types contains invalid value %q", contentType)
		}
		if index > 0 && (*types)[index-1] == contentType {
			return fmt.Errorf("allowed_content_types contains duplicate value %q", contentType)
		}
	}
	if *types == nil {
		*types = []ContentType{}
	}
	return nil
}

func normalizeTrustedAnnotationFields(policy *DetectorPolicy) error {
	if len(policy.TrustedAnnotationFields) > 3 {
		return fmt.Errorf("trusted_annotation_fields exceeds 3 values")
	}
	sort.Strings(policy.TrustedAnnotationFields)
	for index, value := range policy.TrustedAnnotationFields {
		if value != "audience" && value != "priority" && value != "lastModified" {
			return fmt.Errorf("trusted_annotation_fields contains invalid value %q", value)
		}
		if index > 0 && policy.TrustedAnnotationFields[index-1] == value {
			return fmt.Errorf("trusted_annotation_fields contains duplicate value %q", value)
		}
	}
	if len(policy.TrustedAnnotationFields) > 0 && len(policy.Selector.ServerFingerprints) == 0 {
		return fmt.Errorf("trusted_annotation_fields requires exact selector.server_fingerprints")
	}
	if policy.TrustedAnnotationFields == nil {
		policy.TrustedAnnotationFields = []string{}
	}
	return nil
}

func normalizeInspectionLimits(limits *InspectionLimits, phases []Phase) error {
	if limits.MaxBytes == 0 {
		limits.MaxBytes = MaxArgumentBytes
	}
	if limits.MaxItems == 0 {
		limits.MaxItems = MaxJSONItems
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = MaxJSONDepth
	}
	maximumMilliseconds := uint32(1000)
	for _, phase := range phases {
		switch phase {
		case PhasePreCall:
			maximumMilliseconds = min(maximumMilliseconds, 500)
		case PhaseProgress:
			maximumMilliseconds = min(maximumMilliseconds, 250)
		}
	}
	if limits.MaxMilliseconds == 0 {
		limits.MaxMilliseconds = maximumMilliseconds
	}
	if limits.MaxBytes > MaxArgumentBytes || limits.MaxItems > MaxJSONItems ||
		limits.MaxDepth > MaxJSONDepth || limits.MaxMilliseconds > maximumMilliseconds {
		return fmt.Errorf("limits exceed the hard inspection boundary")
	}
	return nil
}

func cloneDetectorPolicy(source DetectorPolicy) DetectorPolicy {
	out := source
	cloneSelector(&out.Selector, source.Selector)
	out.Fields = cloneSlice(source.Fields)
	out.Categories = cloneSlice(source.Categories)
	out.ForbiddenTerms = cloneSlice(source.ForbiddenTerms)
	out.AllowedContentTypes = cloneSlice(source.AllowedContentTypes)
	out.TrustedAnnotationFields = cloneSlice(source.TrustedAnnotationFields)
	return out
}

func cloneCompiledDetectorPolicy(source CompiledDetectorPolicy) CompiledDetectorPolicy {
	out := CompiledDetectorPolicy{Policy: cloneDetectorPolicy(source.Policy)}
	out.Fields = make([]CompiledDetectorField, len(source.Fields))
	for index := range source.Fields {
		out.Fields[index] = source.Fields[index]
		out.Fields[index].Tokens = cloneSlice(source.Fields[index].Tokens)
	}
	return out
}

// DetectorPolicies returns immutable compiled policies matching one exact
// normalized request. The result is detached from the plan.
func (p *CompiledPlan) DetectorPolicies(request Request) []CompiledDetectorPolicy {
	if p == nil {
		return []CompiledDetectorPolicy{}
	}
	toolID := ""
	tool := Tool{
		Transport: request.Transport, Platform: request.Platform,
		ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
		Tool: request.Tool,
	}
	if index, ok := lookupToolIndex(p.toolByExact, tool); ok && index >= 0 && index < len(p.plan.Tools) {
		toolID = p.plan.Tools[index].ID
	}
	out := make([]CompiledDetectorPolicy, 0, len(p.detectors))
	for _, policy := range p.detectors {
		if selectorMatches(policy.Policy.Selector, request, toolID) {
			out = append(out, cloneCompiledDetectorPolicy(policy))
		}
	}
	return out
}
