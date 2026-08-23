package actioninspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

type Engine struct {
	plan *action.CompiledPlan
	key  IdentityKey
	pack compiledDetectorPack
}

// EngineFactory owns one immutable compiled detector pack. The standard
// library regular expressions in the pack are safe for concurrent matching,
// so independent engines can share the compiled programs without sharing
// request or evidence state.
type EngineFactory struct {
	pack compiledDetectorPack
}

func NewEngineFactory() (*EngineFactory, error) {
	pack, err := compileBuiltinPack()
	if err != nil {
		return nil, err
	}
	return &EngineFactory{pack: pack}, nil
}

func (f *EngineFactory) NewEngine(plan *action.CompiledPlan, key IdentityKey) (*Engine, error) {
	if f == nil || len(f.pack.rules) == 0 {
		return nil, fmt.Errorf("inspection engine factory is unavailable")
	}
	return newEngine(plan, key, f.pack)
}

type inspectionAccumulator struct {
	evidence       action.InspectionEvidence
	fields         map[string]struct{}
	binaries       map[string]struct{}
	decision       action.Decision
	reason         action.ReasonCode
	phase          action.Phase
	schemaRequired bool
}

func NewEngine(plan *action.CompiledPlan, key IdentityKey) (*Engine, error) {
	factory, err := NewEngineFactory()
	if err != nil {
		return nil, err
	}
	return factory.NewEngine(plan, key)
}

func newEngine(plan *action.CompiledPlan, key IdentityKey, pack compiledDetectorPack) (*Engine, error) {
	if err := ValidateCompiledPlan(plan); err != nil {
		return nil, err
	}
	if key == nil || key.ID() == "" {
		return nil, fmt.Errorf("inspection identity key is unavailable")
	}
	return &Engine{plan: plan, key: key, pack: pack}, nil
}

func (e *Engine) Inspect(
	ctx context.Context,
	request action.Request,
	result *MCPToolResult,
	outputSchema *OutputSchema,
) (*action.InspectionEvidence, error) {
	if e == nil || e.plan == nil || e.key == nil {
		return nil, fmt.Errorf("inspection engine is unavailable")
	}
	policies := e.plan.DetectorPolicyViews(request)
	if len(policies) == 0 {
		return nil, nil
	}
	accumulator := newInspectionAccumulator(request.Phase)
	for _, policy := range policies {
		accumulator.evidence.PackIdentities = appendUnique(accumulator.evidence.PackIdentities, policy.PackDigest())
	}
	root, source, err := inspectionRoot(request, result)
	if err != nil {
		return e.incompleteEvidence(accumulator, action.ReasonInspectionIncomplete), nil
	}
	deadline := shortestInspectionDuration(policies)
	scanContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	if err := scanContext.Err(); err != nil {
		return e.incompleteEvidence(accumulator, inspectionContextReason(ctx, scanContext, err)), nil
	}
	if err := e.inspectSchema(&accumulator, policies, request, result, outputSchema); err != nil {
		reason := inspectionContextReason(ctx, scanContext, err)
		if reason == action.ReasonInspectionIncomplete {
			reason = action.ReasonSchemaInvalid
		}
		return e.incompleteEvidence(accumulator, reason), nil
	}
	if err := scanContext.Err(); err != nil {
		return e.incompleteEvidence(accumulator, inspectionContextReason(ctx, scanContext, err)), nil
	}
	if err := e.inspectContent(&accumulator, policies, request, result); err != nil {
		reason := inspectionContextReason(ctx, scanContext, err)
		if reason == action.ReasonInspectionIncomplete {
			reason = action.ReasonUnsupportedContent
		}
		return e.incompleteEvidence(accumulator, reason), nil
	}
	for _, policy := range policies {
		if err := e.inspectPolicy(scanContext, &accumulator, policy, source, root, result); err != nil {
			reason := inspectionContextReason(ctx, scanContext, err)
			return e.incompleteEvidence(accumulator, reason), nil
		}
		if accumulator.schemaRequired {
			return e.incompleteEvidence(accumulator, action.ReasonSchemaInvalid), nil
		}
	}
	return e.completeEvidence(accumulator), nil
}

func inspectionRoot(
	request action.Request,
	result *MCPToolResult,
) (action.Value, action.ValueSource, error) {
	switch request.Phase {
	case action.PhasePreCall:
		if request.Arguments == nil {
			return action.Value{}, "", fmt.Errorf("pre-call arguments are unavailable")
		}
		return *request.Arguments, action.SourceArguments, nil
	case action.PhasePostResult:
		if request.Result == nil || result == nil || !request.Result.Equal(result.Root) {
			return action.Value{}, "", fmt.Errorf("post-result value does not match the decoded MCP result")
		}
		return *request.Result, action.SourceResult, nil
	case action.PhaseProgress:
		if request.Progress == nil {
			return action.Value{}, "", fmt.Errorf("progress value is unavailable")
		}
		return *request.Progress, action.SourceProgress, nil
	default:
		return action.Value{}, "", fmt.Errorf("phase %q cannot be inspected", request.Phase)
	}
}

func newInspectionAccumulator(phase action.Phase) inspectionAccumulator {
	status := action.InspectionSchemaNotApplicable
	if phase == action.PhasePostResult {
		status = action.InspectionSchemaNotDeclared
	}
	return inspectionAccumulator{
		evidence: action.InspectionEvidence{
			Status: action.InspectionClean, RuleIDs: []string{}, Categories: []action.DetectorCategory{},
			PackIdentities: []string{}, SchemaStatus: status, SchemaIdentity: "absent",
			Fields: []action.InspectionFieldEvidence{}, UnsupportedContent: []action.InspectionContentEvidence{},
		},
		phase: phase,
	}
}

func shortestInspectionDuration(policies []action.DetectorPolicyView) time.Duration {
	milliseconds := uint32(1000)
	for _, policy := range policies {
		milliseconds = min(milliseconds, policy.Limits().MaxMilliseconds)
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func (e *Engine) inspectSchema(
	accumulator *inspectionAccumulator,
	policies []action.DetectorPolicyView,
	request action.Request,
	result *MCPToolResult,
	schema *OutputSchema,
) error {
	if request.Phase != action.PhasePostResult {
		accumulator.evidence.SchemaIdentity = "absent"
		return nil
	}
	required := false
	for _, policy := range policies {
		required = required || policy.SchemaPolicy() == action.SchemaRequire
	}
	if schema == nil {
		accumulator.evidence.SchemaIdentity = "absent"
		if required {
			accumulator.evidence.SchemaStatus = action.InspectionSchemaRequired
			return fmt.Errorf("output schema is required")
		}
		accumulator.evidence.SchemaStatus = action.InspectionSchemaNotDeclared
		return nil
	}
	accumulator.evidence.SchemaIdentity = schema.Identity()
	if result == nil || !result.HasStructuredContent {
		accumulator.evidence.SchemaStatus = action.InspectionSchemaInvalid
		return fmt.Errorf("declared output schema has no structured content")
	}
	if err := schema.Validate(result.StructuredContent); err != nil {
		accumulator.evidence.SchemaStatus = action.InspectionSchemaInvalid
		return err
	}
	accumulator.evidence.SchemaStatus = action.InspectionSchemaValid
	return nil
}

func (e *Engine) inspectContent(
	accumulator *inspectionAccumulator,
	policies []action.DetectorPolicyView,
	request action.Request,
	result *MCPToolResult,
) error {
	if request.Phase != action.PhasePostResult {
		return nil
	}
	if result == nil {
		return fmt.Errorf("decoded result is unavailable")
	}
	untrustedAnnotation := false
	for _, field := range result.AnnotationFields {
		for _, policy := range policies {
			if policy.TrustsAnnotationField(field) {
				continue
			}
			e.addAnnotationEvidence(accumulator, field)
			untrustedAnnotation = true
			break
		}
	}
	if untrustedAnnotation {
		return fmt.Errorf("result contains an untrusted annotation field")
	}
	unsupported := false
	for _, pointer := range result.MetadataPointers {
		if policiesAddressPointer(policies, pointer) {
			continue
		}
		if err := e.addValueEvidence(accumulator, result.Root, pointer, action.ContentMetadata); err != nil {
			return err
		}
		unsupported = true
	}
	if result.HasStructuredContent && !policiesAddressPointer(policies, "/structuredContent") {
		if err := e.addValueEvidence(accumulator, result.Root, "/structuredContent", action.ContentStructured); err != nil {
			return err
		}
		unsupported = true
	}
	for _, block := range result.Content {
		if block.Type == action.ContentUnknown {
			e.addBinaryEvidence(accumulator, block)
			unsupported = true
			continue
		}
		if contentTypeRequiresExplicitAllow(block.Type) {
			e.addBinaryEvidence(accumulator, block)
			for _, policy := range policies {
				if !policy.AllowsContentType(block.Type) {
					unsupported = true
					break
				}
			}
			continue
		}
		accepted, inspectedByAll := contentAccepted(policies, block.CoveragePointer, block.Type)
		if inspectedByAll {
			continue
		}
		if err := e.addValueEvidence(accumulator, result.Root, block.CoveragePointer, block.Type); err != nil {
			return err
		}
		if !accepted {
			unsupported = true
		}
	}
	if unsupported {
		return fmt.Errorf("result contains unsupported or uninspected content")
	}
	return nil
}

func (e *Engine) addAnnotationEvidence(accumulator *inspectionAccumulator, field string) {
	identity := e.key.Identity(actionstate.DomainInspection, []byte("annotation"), []byte(field))
	key := string(action.ContentAnnotation) + "\x00" + identity
	if _, exists := accumulator.binaries[key]; exists {
		return
	}
	if accumulator.binaries == nil {
		accumulator.binaries = make(map[string]struct{})
	}
	accumulator.binaries[key] = struct{}{}
	accumulator.evidence.UnsupportedContent = append(accumulator.evidence.UnsupportedContent, action.InspectionContentEvidence{
		ContentType: action.ContentAnnotation, Identity: identity, ByteLength: uint64(len(field)),
	})
}

func (e *Engine) addValueEvidence(
	accumulator *inspectionAccumulator,
	root action.Value,
	pointer string,
	contentType action.ContentType,
) error {
	selected, err := action.ResolvePointer(root, pointer)
	if err != nil || selected.State != action.PointerPresent {
		return fmt.Errorf("unsupported result content cannot be identified")
	}
	body, err := selected.Value.MarshalJSON()
	if err != nil {
		return fmt.Errorf("unsupported result content cannot be canonicalized")
	}
	identity := e.key.Identity(
		actionstate.DomainInspection, []byte("unsupported"), []byte(contentType), []byte(pointer), body,
	)
	key := string(contentType) + "\x00" + identity
	if _, exists := accumulator.binaries[key]; exists {
		return nil
	}
	if accumulator.binaries == nil {
		accumulator.binaries = make(map[string]struct{})
	}
	accumulator.binaries[key] = struct{}{}
	accumulator.evidence.UnsupportedContent = append(accumulator.evidence.UnsupportedContent, action.InspectionContentEvidence{
		ContentType: contentType, Identity: identity, ByteLength: uint64(len(body)),
	})
	return nil
}

func (e *Engine) inspectPolicy(
	ctx context.Context,
	accumulator *inspectionAccumulator,
	policy action.DetectorPolicyView,
	source action.ValueSource,
	root action.Value,
	result *MCPToolResult,
) error {
	categories := categorySet(policy)
	binaryPointers := resultBinaryPointers(result)
	var policyBytes uint64
	var policyItems uint32
	limits := policy.Limits()
	for index := 0; index < policy.FieldCount(); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		field, selected, err := policy.ResolveField(root, index)
		if err != nil {
			return err
		}
		if field.Source != source {
			continue
		}
		bytes, items, err := e.inspectSelectedValue(
			ctx, accumulator, policy, selected, field, categories, binaryPointers,
		)
		if err != nil {
			return err
		}
		if bytes > limits.MaxBytes-policyBytes || items > limits.MaxItems-policyItems {
			return fmt.Errorf("%w: selected values exceed cumulative boundary", errInspectionLimit)
		}
		policyBytes += bytes
		policyItems += items
	}
	return nil
}

func (e *Engine) inspectSelectedValue(
	ctx context.Context,
	accumulator *inspectionAccumulator,
	policy action.DetectorPolicyView,
	selected action.PointerResult,
	field action.DetectorField,
	categories map[action.DetectorCategory]struct{},
	binaryPointers map[string]struct{},
) (uint64, uint32, error) {
	pointerIdentity := e.key.Identity(actionstate.DomainInspection, []byte("pointer"), []byte(field.Source), []byte(field.Pointer))
	evidence := action.InspectionFieldEvidence{Source: field.Source, PointerIdentity: pointerIdentity}
	if selected.State != action.PointerPresent && selected.State != action.PointerNull {
		evidence.ValueIdentity = e.key.Identity(
			actionstate.DomainInspection, []byte("value"), []byte(pointerIdentity), []byte(selected.State),
		)
		accumulator.addField(evidence)
		return 0, 0, nil
	}
	limits := policy.Limits()
	items, err := countInspectionItems(
		ctx, selected.Value, int(limits.MaxDepth), limits.MaxItems,
	)
	if err != nil {
		return 0, 0, err
	}
	body, err := selected.Value.MarshalJSON()
	if err != nil {
		return 0, 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if uint64(len(body)) > limits.MaxBytes {
		return 0, 0, fmt.Errorf("%w: selected value exceeds byte boundary", errInspectionLimit)
	}
	evidence.ByteLength = uint64(len(body))
	evidence.ItemCount = items
	evidence.ValueIdentity = e.key.Identity(actionstate.DomainInspection, []byte("value"), []byte(pointerIdentity), body)
	if evidence.ByteLength > action.MaxArgumentBytes-accumulator.evidence.ScannedBytes ||
		evidence.ItemCount > action.MaxJSONItems-accumulator.evidence.ScannedItems {
		return 0, 0, fmt.Errorf("%w: inspection evidence exceeds aggregate boundary", errInspectionLimit)
	}
	accumulator.addField(evidence)
	if err := e.scanValue(ctx, accumulator, policy, selected.Value, field.Pointer, categories, binaryPointers); err != nil {
		return 0, 0, err
	}
	return evidence.ByteLength, evidence.ItemCount, nil
}

func (e *Engine) scanValue(
	ctx context.Context,
	accumulator *inspectionAccumulator,
	policy action.DetectorPolicyView,
	value action.Value,
	pointer string,
	categories map[action.DetectorCategory]struct{},
	binaryPointers map[string]struct{},
) error {
	trackPointers := len(binaryPointers) != 0
	if trackPointers {
		if _, binary := binaryPointers[pointer]; binary {
			return nil
		}
	}
	switch value.Kind() {
	case action.ValueString:
		text, _ := value.Text()
		findings, err := e.pack.scanPolicy(
			ctx, text, categories, policy, policy.Limits().MaxBytes,
		)
		if err != nil {
			return err
		}
		accumulator.addFindings(findings, policy, accumulator.evidence.SchemaStatus)
	case action.ValueArray:
		length, _ := value.ArrayLen()
		for index := 0; index < length; index++ {
			item, _ := value.ArrayItem(index)
			childPointer := ""
			if trackPointers {
				childPointer = pointer + "/" + strconv.Itoa(index)
			}
			if err := e.scanValue(ctx, accumulator, policy, item, childPointer, categories, binaryPointers); err != nil {
				return err
			}
		}
	case action.ValueObject:
		length, _ := value.ObjectLen()
		for index := 0; index < length; index++ {
			member, _ := value.ObjectMember(index)
			child := ""
			if trackPointers {
				child = pointer + "/" + escapePointerToken(member.Name)
			}
			if err := e.scanValue(ctx, accumulator, policy, member.Value, child, categories, binaryPointers); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (a *inspectionAccumulator) addField(field action.InspectionFieldEvidence) {
	key := string(field.Source) + "\x00" + field.PointerIdentity
	if _, exists := a.fields[key]; exists {
		return
	}
	if a.fields == nil {
		a.fields = make(map[string]struct{})
	}
	a.fields[key] = struct{}{}
	a.evidence.Fields = append(a.evidence.Fields, field)
	a.evidence.ScannedBytes += field.ByteLength
	a.evidence.ScannedItems += field.ItemCount
}

func countInspectionItems(
	ctx context.Context,
	value action.Value,
	maxDepth int,
	maxItems uint32,
) (uint32, error) {
	var count uint32
	if err := walkInspectionItems(ctx, value, 0, maxDepth, maxItems, &count); err != nil {
		return 0, err
	}
	return count, nil
}

func walkInspectionItems(
	ctx context.Context,
	value action.Value,
	depth, maxDepth int,
	maxItems uint32,
	count *uint32,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxDepth {
		return fmt.Errorf("%w: selected value exceeds depth boundary", errInspectionLimit)
	}
	switch value.Kind() {
	case action.ValueArray:
		length, _ := value.ArrayLen()
		for index := 0; index < length; index++ {
			child, _ := value.ArrayItem(index)
			if err := walkInspectionChild(ctx, child, depth, maxDepth, maxItems, count); err != nil {
				return err
			}
		}
	case action.ValueObject:
		length, _ := value.ObjectLen()
		for index := 0; index < length; index++ {
			member, _ := value.ObjectMember(index)
			if err := walkInspectionChild(ctx, member.Value, depth, maxDepth, maxItems, count); err != nil {
				return err
			}
		}
	}
	return nil
}

func walkInspectionChild(
	ctx context.Context,
	child action.Value,
	depth, maxDepth int,
	maxItems uint32,
	count *uint32,
) error {
	if *count >= maxItems {
		return fmt.Errorf("%w: selected value exceeds item boundary", errInspectionLimit)
	}
	*count = *count + 1
	return walkInspectionItems(ctx, child, depth+1, maxDepth, maxItems, count)
}

func (e *Engine) addBinaryEvidence(accumulator *inspectionAccumulator, block ContentBlock) {
	identity := e.key.Identity(
		actionstate.DomainInspection, []byte("binary"), []byte(block.Type), []byte(block.MIMEType), block.Binary,
	)
	key := string(block.Type) + "\x00" + identity
	if _, exists := accumulator.binaries[key]; exists {
		return
	}
	if accumulator.binaries == nil {
		accumulator.binaries = make(map[string]struct{})
	}
	accumulator.binaries[key] = struct{}{}
	accumulator.evidence.UnsupportedContent = append(accumulator.evidence.UnsupportedContent, action.InspectionContentEvidence{
		ContentType: block.Type, Identity: identity, ByteLength: uint64(len(block.Binary)),
	})
}

func (a *inspectionAccumulator) addFindings(
	findings []Finding,
	policy action.DetectorPolicyView,
	schemaStatus action.InspectionSchemaStatus,
) {
	for _, finding := range findings {
		a.evidence.RuleIDs = appendUnique(a.evidence.RuleIDs, finding.RuleID)
		a.evidence.Categories = appendUniqueCategory(a.evidence.Categories, finding.Category)
	}
	if len(findings) == 0 {
		return
	}
	decision, reason := inspectionDisposition(policy, a.phase, schemaStatus)
	if reason == action.ReasonSchemaInvalid {
		a.schemaRequired = true
	}
	if decision.Strength() > a.decision.Strength() {
		a.decision, a.reason = decision, reason
	}
}

func inspectionDisposition(
	policy action.DetectorPolicyView,
	phase action.Phase,
	schemaStatus action.InspectionSchemaStatus,
) (action.Decision, action.ReasonCode) {
	switch phase {
	case action.PhasePreCall:
		return policy.PreCallDecision(), action.ReasonRuleMatched
	case action.PhasePostResult:
		switch policy.PostResultDisposition() {
		case action.ResultDispositionWarn:
			return action.DecisionWarn, action.ReasonRuleMatched
		case action.ResultDispositionRequireSchema:
			if schemaStatus == action.InspectionSchemaValid {
				return action.DecisionWarn, action.ReasonRuleMatched
			}
			return action.DecisionBlock, action.ReasonSchemaInvalid
		default:
			return action.DecisionBlock, action.ReasonResultWithheld
		}
	case action.PhaseProgress:
		if policy.ProgressDisposition() == action.ProgressDispositionForward {
			return action.DecisionWarn, action.ReasonRuleMatched
		}
		return action.DecisionBlock, action.ReasonResultWithheld
	}
	return action.DecisionBlock, action.ReasonInspectionIncomplete
}

func (e *Engine) incompleteEvidence(
	accumulator inspectionAccumulator,
	reason action.ReasonCode,
) *action.InspectionEvidence {
	accumulator.evidence.Status = action.InspectionIncomplete
	accumulator.evidence.Decision = action.DecisionBlock
	accumulator.evidence.Reason = reason
	accumulator.evidence.RuleIDs = []string{}
	accumulator.evidence.Categories = []action.DetectorCategory{}
	e.finalizeEvidence(&accumulator.evidence)
	return &accumulator.evidence
}

func (e *Engine) completeEvidence(accumulator inspectionAccumulator) *action.InspectionEvidence {
	if len(accumulator.evidence.RuleIDs) > 0 {
		accumulator.evidence.Status = action.InspectionMatched
		accumulator.evidence.Decision = accumulator.decision
		accumulator.evidence.Reason = accumulator.reason
	}
	e.finalizeEvidence(&accumulator.evidence)
	return &accumulator.evidence
}

func (e *Engine) finalizeEvidence(evidence *action.InspectionEvidence) {
	sort.Strings(evidence.RuleIDs)
	sort.Slice(evidence.Categories, func(i, j int) bool { return evidence.Categories[i] < evidence.Categories[j] })
	sort.Strings(evidence.PackIdentities)
	sort.Slice(evidence.Fields, func(i, j int) bool {
		if evidence.Fields[i].Source != evidence.Fields[j].Source {
			return evidence.Fields[i].Source < evidence.Fields[j].Source
		}
		return evidence.Fields[i].PointerIdentity < evidence.Fields[j].PointerIdentity
	})
	sort.Slice(evidence.UnsupportedContent, func(i, j int) bool {
		if evidence.UnsupportedContent[i].ContentType != evidence.UnsupportedContent[j].ContentType {
			return evidence.UnsupportedContent[i].ContentType < evidence.UnsupportedContent[j].ContentType
		}
		return evidence.UnsupportedContent[i].Identity < evidence.UnsupportedContent[j].Identity
	})
	evidence.Identity = ""
	body, _ := json.Marshal(evidence)
	evidence.Identity = e.key.Identity(actionstate.DomainInspection, []byte("evidence"), body)
}

func resultBinaryPointers(result *MCPToolResult) map[string]struct{} {
	var values map[string]struct{}
	if result == nil {
		return values
	}
	for _, block := range result.Content {
		if block.Type == action.ContentImage || block.Type == action.ContentAudio ||
			block.Type == action.ContentResourceBlob || block.Type == action.ContentUnknown {
			if values == nil {
				values = make(map[string]struct{})
			}
			values[block.Pointer] = struct{}{}
		}
	}
	return values
}

func categorySet(policy action.DetectorPolicyView) map[action.DetectorCategory]struct{} {
	result := make(map[action.DetectorCategory]struct{})
	policy.VisitCategories(func(value action.DetectorCategory) { result[value] = struct{}{} })
	return result
}

func contentAccepted(
	policies []action.DetectorPolicyView,
	pointer string,
	contentType action.ContentType,
) (bool, bool) {
	inspectedByAll := true
	for _, policy := range policies {
		if policyAddressesPointer(policy, pointer) {
			continue
		}
		inspectedByAll = false
		if !policy.AllowsContentType(contentType) {
			return false, false
		}
	}
	return true, inspectedByAll
}

func policiesAddressPointer(policies []action.DetectorPolicyView, pointer string) bool {
	for _, policy := range policies {
		if !policyAddressesPointer(policy, pointer) {
			return false
		}
	}
	return true
}

func policyAddressesPointer(policy action.DetectorPolicyView, pointer string) bool {
	return policy.AddressesResultPointer(pointer)
}

func contentTypeRequiresExplicitAllow(value action.ContentType) bool {
	return value == action.ContentImage || value == action.ContentAudio ||
		value == action.ContentResourceBlob
}

func escapePointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueCategory(values []action.DetectorCategory, value action.DetectorCategory) []action.DetectorCategory {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func inspectionContextReason(parent, scan context.Context, err error) action.ReasonCode {
	if parent.Err() == context.Canceled {
		return action.ReasonCancelled
	}
	if parent.Err() == context.DeadlineExceeded || scan.Err() == context.DeadlineExceeded {
		return action.ReasonDeadlineExceeded
	}
	if errors.Is(err, errInspectionLimit) {
		return action.ReasonLimitExceeded
	}
	return action.ReasonInspectionIncomplete
}
