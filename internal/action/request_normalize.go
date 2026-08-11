package action

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode"
	"unicode/utf8"
)

func NormalizeRequest(raw RawRequest) (Request, error) {
	if err := validateRawValueBudget(raw); err != nil {
		return Request{}, err
	}
	request := requestMetadata(raw)
	if err := parseRawPhase(&request, raw); err != nil {
		return Request{}, err
	}
	context, err := normalizeRawContext(raw.Context)
	if err != nil {
		return Request{}, err
	}
	request.Context = context
	request.Completeness, err = normalizeCompleteness(raw.Completeness)
	if err != nil {
		return Request{}, err
	}
	return validateAndCloneRequest(request)
}

func validateRawValueBudget(raw RawRequest) error {
	rawValueBytes := 0
	for _, value := range []json.RawMessage{raw.Arguments, raw.Result, raw.Progress} {
		if len(value) > MaxArgumentBytes-rawValueBytes {
			return &RequestError{Code: ReasonLimitExceeded, Message: "request values exceed the aggregate byte limit"}
		}
		rawValueBytes += len(value)
	}
	for _, entry := range raw.Context {
		if len(entry.Value) > MaxArgumentBytes-rawValueBytes {
			return &RequestError{Code: ReasonLimitExceeded, Message: "request values exceed the aggregate byte limit"}
		}
		rawValueBytes += len(entry.Value)
	}
	return nil
}

func requestMetadata(raw RawRequest) Request {
	return Request{
		FormatVersion: raw.FormatVersion, CallID: raw.CallID,
		Transport: raw.Transport, Platform: raw.Platform,
		ServerLabel: raw.ServerLabel, ServerFingerprint: raw.ServerFingerprint,
		Tool: raw.Tool, ToolContractDigest: raw.ToolContractDigest,
		Phase: raw.Phase, RepositoryIdentity: raw.RepositoryIdentity,
		PolicyDigest: raw.PolicyDigest, LockDigest: raw.LockDigest,
		AuthorityMode: raw.AuthorityMode, Completeness: raw.Completeness,
		Deadline: raw.Deadline, StateVersion: raw.StateVersion,
	}
}

func parseRawPhase(request *Request, raw RawRequest) error {
	var err error
	switch raw.Phase {
	case PhasePreCall:
		request.Arguments, err = parseRawRequestValue(raw.Arguments, true)
	case PhasePostResult:
		request.Result, err = parseRawRequestValue(raw.Result, false)
	case PhaseProgress:
		request.Progress, err = parseRawRequestValue(raw.Progress, false)
	case PhaseObservation:
		if len(raw.Arguments) != 0 || len(raw.Result) != 0 || len(raw.Progress) != 0 {
			err = &RequestError{Code: ReasonUnsupportedPhase, Message: "observation requests cannot carry phase payloads"}
		}
	default:
		err = &RequestError{Code: ReasonUnsupportedPhase, Message: "request phase is unsupported"}
	}
	if err != nil {
		return err
	}
	if raw.Phase != PhasePreCall && len(raw.Arguments) != 0 ||
		raw.Phase != PhasePostResult && len(raw.Result) != 0 ||
		raw.Phase != PhaseProgress && len(raw.Progress) != 0 {
		return &RequestError{Code: ReasonUnsupportedPhase, Message: "request payload does not belong to its phase"}
	}
	return nil
}

func parseRawRequestValue(raw json.RawMessage, object bool) (*Value, error) {
	if len(raw) == 0 {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: "required phase payload is absent"}
	}
	var (
		value Value
		err   error
	)
	if object {
		value, err = ParseObjectJSON(raw)
	} else {
		value, err = ParseJSON(raw)
	}
	if err != nil {
		return nil, classifyJSONRequestError(err)
	}
	return &value, nil
}

func normalizeRawContext(raw []RawContextValue) ([]ContextValue, error) {
	if len(raw) > MaxContextValues {
		return nil, &RequestError{Code: ReasonLimitExceeded, Message: "context value count exceeds the contract limit"}
	}
	values := make([]ContextValue, len(raw))
	for index := range raw {
		entry := raw[index]
		if !validContextName(entry.Name) || !entry.Provenance.Valid() {
			return nil, &RequestError{Code: ReasonInvalidRequest, Message: "context metadata is invalid"}
		}
		if !entry.Available {
			if len(entry.Value) != 0 {
				return nil, &RequestError{Code: ReasonInvalidRequest, Message: "unavailable context cannot carry a value"}
			}
			values[index] = ContextValue{Name: entry.Name, Provenance: entry.Provenance}
			continue
		}
		value, err := ParseJSON(entry.Value)
		if err != nil {
			return nil, classifyJSONRequestError(err)
		}
		values[index] = ContextValue{
			Name: entry.Name, Value: value,
			Provenance: entry.Provenance, Available: true,
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	for index := 1; index < len(values); index++ {
		if values[index-1].Name == values[index].Name {
			return nil, &RequestError{Code: ReasonDuplicateKey, Message: "context contains a duplicate key"}
		}
	}
	if values == nil {
		values = []ContextValue{}
	}
	return values, nil
}

func normalizeCompleteness(input Completeness) (Completeness, error) {
	out := input
	out.Missing = append([]MissingEvidence(nil), input.Missing...)
	sort.Slice(out.Missing, func(i, j int) bool {
		if out.Missing[i].Field != out.Missing[j].Field {
			return out.Missing[i].Field < out.Missing[j].Field
		}
		return out.Missing[i].Reason < out.Missing[j].Reason
	})
	seen := make(map[EvidenceField]bool, len(out.Missing))
	for index, missing := range out.Missing {
		if !missing.Field.Valid() || !failureReason(missing.Reason) {
			return Completeness{}, &RequestError{Code: ReasonInvalidRequest, Message: "completeness contains an invalid missing-evidence reason"}
		}
		if index > 0 && out.Missing[index-1] == missing {
			return Completeness{}, &RequestError{Code: ReasonDuplicateKey, Message: "completeness contains a duplicate missing-evidence entry"}
		}
		seen[missing.Field] = true
		if completenessField(out, missing.Field) {
			return Completeness{}, &RequestError{Code: ReasonInvalidRequest, Message: "complete evidence cannot also be listed as missing"}
		}
	}
	for _, field := range []EvidenceField{
		EvidenceRequest, EvidencePolicy, EvidenceIdentity,
		EvidenceContext, EvidenceState, EvidencePhase,
	} {
		if !completenessField(out, field) && !seen[field] {
			return Completeness{}, &RequestError{Code: ReasonInvalidRequest, Message: "incomplete evidence requires one typed missing reason"}
		}
	}
	if out.Missing == nil {
		out.Missing = []MissingEvidence{}
	}
	return out, nil
}

// NormalizeCompleteness validates and canonicalizes one evidence declaration
// without requiring a complete action request.
func NormalizeCompleteness(input Completeness) (Completeness, error) {
	return normalizeCompleteness(input)
}

func completenessField(value Completeness, field EvidenceField) bool {
	switch field {
	case EvidenceRequest:
		return value.RequestComplete
	case EvidencePolicy:
		return value.PolicyComplete
	case EvidenceIdentity:
		return value.IdentityComplete
	case EvidenceContext:
		return value.ContextComplete
	case EvidenceState:
		return value.StateComplete
	case EvidencePhase:
		return value.PhaseComplete
	default:
		return false
	}
}

func validateAndCloneRequest(input Request) (Request, error) {
	request := input
	if err := validateRequestMetadata(request); err != nil {
		return Request{}, err
	}
	if err := validatePhasePayload(&request); err != nil {
		return Request{}, err
	}
	context, err := cloneContextValues(request.Context, phaseValueBytes(request))
	if err != nil {
		return Request{}, err
	}
	request.Context = context
	request.Completeness, err = normalizeCompleteness(request.Completeness)
	if err != nil {
		return Request{}, err
	}
	return request, nil
}

func validateRequestMetadata(request Request) error {
	if request.FormatVersion != RequestFormatVersion || !validCallID(request.CallID) {
		return &RequestError{Code: ReasonInvalidRequest, Message: "request version or call identity is invalid"}
	}
	if !request.Transport.Valid() || !request.Phase.Valid() || !request.AuthorityMode.Valid() || !request.Deadline.Valid() {
		return &RequestError{Code: ReasonInvalidRequest, Message: "request enum value is invalid"}
	}
	if err := validateToolName(request.Tool, request.Transport == TransportMCPStdio); err != nil {
		return &RequestError{Code: ReasonInvalidRequest, Message: "request tool identity is invalid"}
	}
	switch request.Transport {
	case TransportHostMCP:
		if !ValidPlatform(request.Platform) || request.ServerLabel != "" ||
			!sha256IdentityPattern.MatchString(request.ServerFingerprint) {
			return &RequestError{Code: ReasonIdentityUnavailable, Message: "host transport identity is unavailable"}
		}
	case TransportMCPStdio:
		if request.Platform != "" || !SafeLabel(request.ServerLabel) ||
			!hmacIdentityPattern.MatchString(request.ServerFingerprint) {
			return &RequestError{Code: ReasonIdentityUnavailable, Message: "stdio transport identity is unavailable"}
		}
	}
	if !sha256IdentityPattern.MatchString(request.ToolContractDigest) ||
		!ValidIdentity(request.RepositoryIdentity) ||
		!lowerHex64(request.PolicyDigest) ||
		!lowerHex64(request.LockDigest) ||
		!validOpaqueIdentity(request.StateVersion) {
		return &RequestError{Code: ReasonIdentityUnavailable, Message: "required request identity is unavailable"}
	}
	return nil
}

func cloneContextValues(input []ContextValue, valueBytes int) ([]ContextValue, error) {
	if len(input) > MaxContextValues {
		return nil, &RequestError{Code: ReasonLimitExceeded, Message: "context value count exceeds the contract limit"}
	}
	context := make([]ContextValue, len(input))
	for index, entry := range input {
		if !validContextName(entry.Name) || !entry.Provenance.Valid() {
			return nil, &RequestError{Code: ReasonInvalidRequest, Message: "context metadata is invalid"}
		}
		if !entry.Available {
			if entry.Value.kind != "" {
				return nil, &RequestError{Code: ReasonInvalidRequest, Message: "unavailable context cannot carry a value"}
			}
			context[index] = ContextValue{Name: entry.Name, Provenance: entry.Provenance}
			continue
		}
		value, err := cloneRuntimeValue(entry.Value)
		if err != nil {
			return nil, err
		}
		context[index] = ContextValue{Name: entry.Name, Value: value, Provenance: entry.Provenance, Available: true}
		body, _ := value.MarshalJSON()
		if len(body) > MaxArgumentBytes-valueBytes {
			return nil, &RequestError{Code: ReasonLimitExceeded, Message: "request values exceed the aggregate byte limit"}
		}
		valueBytes += len(body)
	}
	sort.Slice(context, func(i, j int) bool { return context[i].Name < context[j].Name })
	for index := 1; index < len(context); index++ {
		if context[index-1].Name == context[index].Name {
			return nil, &RequestError{Code: ReasonDuplicateKey, Message: "context contains a duplicate key"}
		}
	}
	if context == nil {
		context = []ContextValue{}
	}
	return context, nil
}

func phaseValueBytes(request Request) int {
	for _, value := range []*Value{request.Arguments, request.Result, request.Progress} {
		if value != nil {
			body, _ := value.MarshalJSON()
			return len(body)
		}
	}
	return 0
}

func validatePhasePayload(request *Request) error {
	switch request.Phase {
	case PhasePreCall:
		if request.Arguments == nil || request.Result != nil || request.Progress != nil {
			return &RequestError{Code: ReasonUnsupportedPhase, Message: "pre-call payload shape is invalid"}
		}
		value, err := cloneRuntimeValue(*request.Arguments)
		if err != nil {
			return err
		}
		if value.Kind() != ValueObject {
			return &RequestError{Code: ReasonInvalidRequest, Message: "arguments must contain one object"}
		}
		request.Arguments = &value
	case PhasePostResult:
		if request.Arguments != nil || request.Result == nil || request.Progress != nil {
			return &RequestError{Code: ReasonUnsupportedPhase, Message: "post-result payload shape is invalid"}
		}
		value, err := cloneRuntimeValue(*request.Result)
		if err != nil {
			return err
		}
		request.Result = &value
	case PhaseProgress:
		if request.Arguments != nil || request.Result != nil || request.Progress == nil {
			return &RequestError{Code: ReasonUnsupportedPhase, Message: "progress payload shape is invalid"}
		}
		value, err := cloneRuntimeValue(*request.Progress)
		if err != nil {
			return err
		}
		request.Progress = &value
	case PhaseObservation:
		if request.Arguments != nil || request.Result != nil || request.Progress != nil {
			return &RequestError{Code: ReasonUnsupportedPhase, Message: "observation payload shape is invalid"}
		}
	default:
		return &RequestError{Code: ReasonUnsupportedPhase, Message: "request phase is unsupported"}
	}
	return nil
}

func cloneRuntimeValue(value Value) (Value, error) {
	items := 0
	if err := validateRuntimeValue(value, 0, &items); err != nil {
		return Value{}, err
	}
	body, err := value.MarshalJSON()
	if err != nil {
		return Value{}, &RequestError{Code: ReasonInvalidRequest, Message: "normalized value is invalid"}
	}
	if len(body) > MaxArgumentBytes {
		return Value{}, &RequestError{Code: ReasonLimitExceeded, Message: "normalized value exceeds the byte limit"}
	}
	// Value owns a closed representation. Its slices are private and every
	// accessor returns a copy, so a validated value copy is immutable to callers.
	return value, nil
}

func validateRuntimeValue(value Value, depth int, items *int) error {
	if depth > MaxJSONDepth {
		return &RequestError{Code: ReasonLimitExceeded, Message: "normalized value exceeds the depth limit"}
	}
	switch value.kind {
	case ValueNull, ValueBool:
		return nil
	case ValueNumber:
		if !validRuntimeDecimal(value.number) {
			return &RequestError{Code: ReasonInvalidRequest, Message: "normalized number is invalid"}
		}
		return nil
	case ValueString:
		if !utf8.ValidString(value.string) || len(value.string) > MaxJSONStringBytes {
			return &RequestError{Code: ReasonLimitExceeded, Message: "normalized string exceeds its limit"}
		}
		return nil
	case ValueArray:
		return validateRuntimeArray(value.array, depth, items)
	case ValueObject:
		return validateRuntimeObject(value.object, depth, items)
	default:
		return &RequestError{Code: ReasonInvalidRequest, Message: "normalized value kind is invalid"}
	}
}

func validateRuntimeArray(values []Value, depth int, items *int) error {
	for _, value := range values {
		(*items)++
		if *items > MaxJSONItems {
			return &RequestError{Code: ReasonLimitExceeded, Message: "normalized value exceeds the item limit"}
		}
		if err := validateRuntimeValue(value, depth+1, items); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeObject(members []Member, depth int, items *int) error {
	for index, member := range members {
		if !utf8.ValidString(member.Name) || len(member.Name) > MaxJSONStringBytes ||
			index > 0 && members[index-1].Name >= member.Name {
			return &RequestError{Code: ReasonInvalidRequest, Message: "normalized object key is invalid"}
		}
		(*items)++
		if *items > MaxJSONItems {
			return &RequestError{Code: ReasonLimitExceeded, Message: "normalized value exceeds the item limit"}
		}
		if err := validateRuntimeValue(member.Value, depth+1, items); err != nil {
			return err
		}
	}
	return nil
}

func validRuntimeDecimal(value Decimal) bool {
	if value.coefficient == "0" {
		return !value.negative && value.exponent == 0
	}
	if len(value.coefficient) == 0 || len(value.coefficient) > MaxNumberDigits ||
		value.exponent < -MaxNumberExponent || value.exponent > MaxNumberExponent ||
		value.coefficient[0] == '0' || value.coefficient[len(value.coefficient)-1] == '0' {
		return false
	}
	for _, character := range value.coefficient {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func CanonicalRequest(request Request) ([]byte, error) {
	normalized, err := validateAndCloneRequest(request)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, &RequestError{Code: ReasonInvalidRequest, Message: "request cannot be encoded canonically"}
	}
	if len(body) > MaxArgumentBytes+MaxJSONStringBytes {
		return nil, &RequestError{Code: ReasonLimitExceeded, Message: "canonical request exceeds the byte limit"}
	}
	return body, nil
}

// RequestDigest returns an in-memory correlation digest. It is not a
// persistence identity; persisted selected-value identities require the keyed,
// domain-separated contract owned by action state.
func RequestDigest(request Request) (string, error) {
	body, err := CanonicalRequest(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func requestDigestValidated(request Request) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return digestBytes(body), nil
}

func classifyJSONRequestError(err error) *RequestError {
	switch JSONErrorKindOf(err) {
	case JSONErrorInvalidUTF8:
		return &RequestError{Code: ReasonInvalidUTF8, Message: "JSON contains invalid Unicode"}
	case JSONErrorDuplicateKey:
		return &RequestError{Code: ReasonDuplicateKey, Message: "JSON contains a duplicate object key"}
	case JSONErrorLimit:
		return &RequestError{Code: ReasonLimitExceeded, Message: "JSON exceeds a contract resource limit"}
	default:
		return &RequestError{Code: ReasonInvalidRequest, Message: "JSON request value is invalid"}
	}
}

func failureReason(reason ReasonCode) bool {
	return reason.Valid() && reason != ReasonDeclaredTool && reason != ReasonHostUnmatched && reason != ReasonRuleMatched
}

func validContextName(value string) bool {
	if value == "" || len(value) > MaxPointerBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validOpaqueIdentity(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || stringsContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validCallID(value string) bool {
	if len(value) != 30 || value[:4] != "act_" {
		return false
	}
	for _, character := range value[4:] {
		if character < 'a' || character > 'z' {
			if character < '2' || character > '7' {
				return false
			}
		}
	}
	return true
}

func lowerHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func stringsContainsRune(value string, want rune) bool {
	for _, character := range value {
		if character == want {
			return true
		}
	}
	return false
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func safeStringList(values []string, maximum int) ([]string, error) {
	if len(values) > maximum {
		return nil, fmt.Errorf("list exceeds %d values", maximum)
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	for index, value := range out {
		if !SafeLabel(value) {
			return nil, fmt.Errorf("list contains an invalid safe label")
		}
		if index > 0 && out[index-1] == value {
			return nil, fmt.Errorf("list contains a duplicate safe label")
		}
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
