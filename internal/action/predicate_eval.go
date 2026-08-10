package action

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type predicateEvaluation struct {
	state    ConditionState
	reason   ReasonCode
	actual   Provenance
	required Provenance
	complete bool
	summary  OperandSummary
}

type selectedValue struct {
	pointer    PointerResult
	provenance Provenance
	available  bool
}

func evaluatePredicate(
	predicate *CompiledPredicate,
	request Request,
	ruleDecision Decision,
) predicateEvaluation {
	if predicate == nil || !predicate.Predicate.Op.Valid() ||
		!predicate.Predicate.Source.Valid() || validateCompiledPointer(predicate.Tokens) != nil {
		return indeterminatePredicate(ReasonInternalInvariant, "", "", OperandSummary{})
	}
	selected := selectPredicateValue(predicate, request)
	summary := summarizePointer(selected.pointer)
	required := predicate.Predicate.MinimumProvenance
	if predicate.Predicate.Source == SourceContext {
		if !selected.available {
			return indeterminatePredicate(ReasonConditionIndeterminate, selected.provenance, required, summary)
		}
		if selected.provenance.Rank() < required.Rank() {
			return indeterminatePredicate(ReasonContextUntrusted, selected.provenance, required, summary)
		}
	}
	if predicate.Predicate.Op == OperatorPathWithin && ruleDecision == DecisionAllow &&
		selected.provenance.Rank() < ProvenanceHostObserved.Rank() {
		return indeterminatePredicate(ReasonContextUntrusted, selected.provenance, ProvenanceHostObserved, summary)
	}
	state, reason := evaluateOperator(predicate, selected.pointer)
	return predicateEvaluation{
		state: state, reason: reason, actual: selected.provenance,
		required: required, complete: state != ConditionIndeterminate,
		summary: summary,
	}
}

func selectPredicateValue(predicate *CompiledPredicate, request Request) selectedValue {
	switch predicate.Predicate.Source {
	case SourceArguments:
		return selectRoot(request.Arguments, predicate.Tokens, ProvenanceAgentSupplied)
	case SourceResult:
		return selectRoot(request.Result, predicate.Tokens, ProvenanceAgentSupplied)
	case SourceProgress:
		return selectRoot(request.Progress, predicate.Tokens, ProvenanceAgentSupplied)
	case SourceContext:
		return selectContext(request.Context, predicate.Tokens)
	default:
		return selectedValue{pointer: PointerResult{State: PointerWrongContainer}}
	}
}

func selectRoot(root *Value, tokens []string, provenance Provenance) selectedValue {
	if root == nil {
		return selectedValue{pointer: PointerResult{State: PointerMissing}, provenance: provenance}
	}
	return selectedValue{
		pointer: resolvePointerTokens(*root, tokens), provenance: provenance,
		available: true,
	}
}

func selectContext(context []ContextValue, tokens []string) selectedValue {
	if len(tokens) == 0 {
		return selectContextRoot(context)
	}
	return selectContextMember(context, tokens)
}

func selectContextRoot(context []ContextValue) selectedValue {
	members := make([]Member, 0, len(context))
	provenance := ProvenanceOperatorBound
	for _, entry := range context {
		if !entry.Available {
			return selectedValue{
				pointer: PointerResult{State: PointerMissing}, provenance: entry.Provenance,
			}
		}
		if entry.Provenance.Rank() < provenance.Rank() {
			provenance = entry.Provenance
		}
		members = append(members, Member{Name: entry.Name, Value: entry.Value})
	}
	root, err := Object(members)
	if err != nil {
		return selectedValue{pointer: PointerResult{State: PointerWrongContainer}}
	}
	if len(context) == 0 {
		provenance = ProvenanceAgentSupplied
	}
	return selectedValue{
		pointer:    PointerResult{State: PointerPresent, Value: root},
		provenance: provenance, available: true,
	}
}

func selectContextMember(context []ContextValue, tokens []string) selectedValue {
	index := sortContext(context, tokens[0])
	if index < 0 {
		return selectedValue{
			pointer:    PointerResult{State: PointerMissing},
			provenance: ProvenanceAgentSupplied, available: true,
		}
	}
	entry := context[index]
	if !entry.Available {
		return selectedValue{
			pointer:    PointerResult{State: PointerMissing},
			provenance: entry.Provenance,
		}
	}
	return selectedValue{
		pointer:    resolvePointerTokens(entry.Value, tokens[1:]),
		provenance: entry.Provenance, available: true,
	}
}

func sortContext(context []ContextValue, name string) int {
	low, high := 0, len(context)
	for low < high {
		middle := int(uint(low+high) >> 1)
		if context[middle].Name < name {
			low = middle + 1
		} else {
			high = middle
		}
	}
	if low == len(context) || context[low].Name != name {
		return -1
	}
	return low
}

func evaluateOperator(predicate *CompiledPredicate, selected PointerResult) (ConditionState, ReasonCode) {
	op := predicate.Predicate.Op
	if state, reason, ready := operatorTargetState(predicate, selected); !ready {
		return state, reason
	}
	want := *predicate.Predicate.Value
	switch op {
	case OperatorEqual, OperatorNotEqual:
		return evaluateEqualityOperator(op, selected.Value, want)
	case OperatorIn, OperatorNotIn:
		return evaluateMembership(op, selected.Value, want)
	case OperatorPrefix, OperatorSuffix, OperatorContains:
		return evaluateStringOperator(op, selected.Value, want)
	case OperatorGlob, OperatorRegex:
		return evaluatePatternOperator(predicate, selected.Value)
	case OperatorGreater, OperatorGreaterEq, OperatorLess, OperatorLessEq:
		return evaluateNumericOperator(op, selected.Value, want)
	case OperatorURL:
		return evaluateURLPredicate(predicate, selected.Value)
	case OperatorCIDR:
		return evaluateCIDRPredicate(predicate, selected.Value)
	case OperatorPathWithin:
		return evaluatePathPredicate(predicate, selected.Value)
	default:
		return ConditionIndeterminate, ReasonInternalInvariant
	}
}

func operatorTargetState(
	predicate *CompiledPredicate,
	selected PointerResult,
) (ConditionState, ReasonCode, bool) {
	op := predicate.Predicate.Op
	if selected.State == PointerMissing {
		if op == OperatorExists {
			return ConditionFalse, "", false
		}
		return ConditionIndeterminate, ReasonConditionIndeterminate, false
	}
	if selected.State == PointerWrongContainer || selected.State == PointerInvalidIndex {
		return ConditionIndeterminate, ReasonConditionIndeterminate, false
	}
	if op == OperatorExists {
		return ConditionTrue, "", false
	}
	if predicate.Predicate.Value == nil {
		return ConditionIndeterminate, ReasonInternalInvariant, false
	}
	if selected.State == PointerNull && op != OperatorEqual && op != OperatorNotEqual {
		return ConditionIndeterminate, ReasonConditionIndeterminate, false
	}
	return "", "", true
}

func evaluateEqualityOperator(op Operator, target, operand Value) (ConditionState, ReasonCode) {
	equal := target.Equal(operand)
	if op == OperatorNotEqual {
		equal = !equal
	}
	return conditionFromBool(equal), ""
}

func evaluatePatternOperator(predicate *CompiledPredicate, target Value) (ConditionState, ReasonCode) {
	text, ok := target.Text()
	if predicate.Predicate.Op == OperatorGlob {
		if !ok || predicate.Glob == nil {
			return ConditionIndeterminate, reasonForMatcher(ok, predicate.Glob != nil)
		}
		return conditionFromBool(predicate.Glob.Match(text)), ""
	}
	if !ok || predicate.Regex == nil {
		return ConditionIndeterminate, reasonForMatcher(ok, predicate.Regex != nil)
	}
	return conditionFromBool(predicate.Regex.MatchString(text)), ""
}

func evaluateURLPredicate(predicate *CompiledPredicate, target Value) (ConditionState, ReasonCode) {
	text, ok := target.Text()
	if !ok || predicate.URL == nil {
		return ConditionIndeterminate, reasonForMatcher(ok, predicate.URL != nil)
	}
	return matchURLConstraint(text, *predicate.URL), ""
}

func evaluateCIDRPredicate(predicate *CompiledPredicate, target Value) (ConditionState, ReasonCode) {
	text, ok := target.Text()
	if !ok || len(predicate.CIDRs) == 0 {
		return ConditionIndeterminate, reasonForMatcher(ok, len(predicate.CIDRs) > 0)
	}
	return matchCIDRs(text, predicate.CIDRs), ""
}

func evaluatePathPredicate(predicate *CompiledPredicate, target Value) (ConditionState, ReasonCode) {
	text, ok := target.Text()
	if !ok || predicate.Path == nil {
		return ConditionIndeterminate, reasonForMatcher(ok, predicate.Path != nil)
	}
	return matchPathConstraint(text, *predicate.Path), ""
}

func evaluateMembership(op Operator, target, operand Value) (ConditionState, ReasonCode) {
	if !target.Scalar() || target.Kind() == ValueNull {
		return ConditionIndeterminate, ReasonConditionIndeterminate
	}
	items, ok := operand.Items()
	if !ok || len(items) == 0 || len(items) > MaxListValues {
		return ConditionIndeterminate, ReasonInternalInvariant
	}
	matched := false
	for _, item := range items {
		if !item.Scalar() || item.Kind() != target.Kind() {
			return ConditionIndeterminate, ReasonInternalInvariant
		}
		if target.Equal(item) {
			matched = true
			break
		}
	}
	if op == OperatorNotIn {
		matched = !matched
	}
	return conditionFromBool(matched), ""
}

func evaluateStringOperator(op Operator, target, operand Value) (ConditionState, ReasonCode) {
	text, ok := target.Text()
	if !ok {
		return ConditionIndeterminate, ReasonConditionIndeterminate
	}
	want, ok := operand.Text()
	if !ok {
		return ConditionIndeterminate, ReasonInternalInvariant
	}
	switch op {
	case OperatorPrefix:
		return conditionFromBool(strings.HasPrefix(text, want)), ""
	case OperatorSuffix:
		return conditionFromBool(strings.HasSuffix(text, want)), ""
	default:
		return conditionFromBool(strings.Contains(text, want)), ""
	}
}

func evaluateNumericOperator(op Operator, target, operand Value) (ConditionState, ReasonCode) {
	left, ok := target.Decimal()
	if !ok {
		return ConditionIndeterminate, ReasonConditionIndeterminate
	}
	right, ok := operand.Decimal()
	if !ok {
		return ConditionIndeterminate, ReasonInternalInvariant
	}
	comparison := left.Compare(right)
	switch op {
	case OperatorGreater:
		return conditionFromBool(comparison > 0), ""
	case OperatorGreaterEq:
		return conditionFromBool(comparison >= 0), ""
	case OperatorLess:
		return conditionFromBool(comparison < 0), ""
	default:
		return conditionFromBool(comparison <= 0), ""
	}
}

type normalizedURLTarget struct {
	scheme    string
	host      string
	port      uint16
	path      string
	ipLiteral bool
	query     bool
}

func matchURLConstraint(raw string, constraint URLConstraint) ConditionState {
	target, ok := normalizeURLTarget(raw)
	if !ok {
		return ConditionIndeterminate
	}
	if !containsSorted(constraint.Schemes, target.scheme) ||
		!containsSorted(constraint.Hosts, target.host) {
		return ConditionFalse
	}
	if target.ipLiteral && !constraint.AllowIPLiteral {
		return ConditionFalse
	}
	if len(constraint.Ports) > 0 && !containsPort(constraint.Ports, target.port) {
		return ConditionFalse
	}
	if len(constraint.PathPrefixes) > 0 &&
		!matchesAnyPathPrefix(target.path, constraint.PathPrefixes) {
		return ConditionFalse
	}
	if target.query && !constraint.AllowQuery {
		return ConditionFalse
	}
	return ConditionTrue
}

func normalizeURLTarget(raw string) (normalizedURLTarget, bool) {
	if raw == "" || !utf8.ValidString(raw) || containsControl(raw) ||
		strings.Contains(raw, `\`) || strings.Contains(raw, "#") {
		return normalizedURLTarget{}, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Fragment != "" {
		return normalizedURLTarget{}, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	hostText := parsed.Hostname()
	if hostText == "" || strings.Contains(hostText, "%") {
		return normalizedURLTarget{}, false
	}
	host, err := normalizeConstraintHost(hostText)
	if err != nil {
		return normalizedURLTarget{}, false
	}
	port, ok := normalizedURLPort(parsed, scheme)
	if !ok {
		return normalizedURLTarget{}, false
	}
	pathValue, ok := normalizedURLPath(parsed)
	if !ok || !validPercentEncoding(parsed.RawQuery) {
		return normalizedURLTarget{}, false
	}
	_, addressErr := netip.ParseAddr(host)
	return normalizedURLTarget{
		scheme: scheme, host: host, port: port, path: pathValue,
		ipLiteral: addressErr == nil, query: parsed.ForceQuery || parsed.RawQuery != "",
	}, true
}

func normalizedURLPort(parsed *url.URL, scheme string) (uint16, bool) {
	portText := parsed.Port()
	if portText != "" {
		port, err := strconv.ParseUint(portText, 10, 16)
		return uint16(port), err == nil && port > 0
	}
	host := parsed.Host
	if strings.Contains(host, ":") && !strings.HasSuffix(host, "]") {
		return 0, false
	}
	switch scheme {
	case "http":
		return 80, true
	case "https":
		return 443, true
	default:
		return 0, false
	}
}

func normalizedURLPath(parsed *url.URL) (string, bool) {
	escaped := parsed.EscapedPath()
	if escaped == "" {
		return "/", true
	}
	if !strings.HasPrefix(escaped, "/") || strings.HasPrefix(escaped, "//") {
		return "", false
	}
	if escaped == "/" {
		return escaped, true
	}
	segments := strings.Split(strings.TrimPrefix(escaped, "/"), "/")
	for index, segment := range segments {
		if segment == "" || containsAmbiguousPercentEscape(segment) {
			return "", false
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil || !utf8.ValidString(decoded) || decoded == "." || decoded == ".." ||
			containsControl(decoded) || strings.ContainsAny(decoded, `/\`) {
			return "", false
		}
		segments[index] = decoded
	}
	return "/" + strings.Join(segments, "/"), true
}

func validPercentEncoding(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) {
			return false
		}
		if _, err := strconv.ParseUint(value[index+1:index+3], 16, 8); err != nil {
			return false
		}
		index += 2
	}
	return true
}

func matchCIDRs(raw string, prefixes []netip.Prefix) ConditionState {
	if strings.Contains(raw, "%") {
		return ConditionIndeterminate
	}
	address, err := netip.ParseAddr(raw)
	if err != nil {
		return ConditionIndeterminate
	}
	address = address.Unmap()
	for _, prefix := range prefixes {
		prefix = prefix.Masked()
		if prefix.Addr().Is4() != address.Is4() {
			continue
		}
		if prefix.Contains(address) {
			return ConditionTrue
		}
	}
	return ConditionFalse
}

func matchPathConstraint(raw string, constraint PathConstraint) ConditionState {
	target, targetVolume, ok := normalizeRuntimePath(raw, constraint.Style)
	if !ok {
		return ConditionIndeterminate
	}
	base, baseVolume, ok := normalizeRuntimePath(constraint.Base, constraint.Style)
	if !ok {
		return ConditionIndeterminate
	}
	if !constraint.CaseSensitive {
		target = strings.ToLower(target)
		base = strings.ToLower(base)
		targetVolume = strings.ToLower(targetVolume)
		baseVolume = strings.ToLower(baseVolume)
	}
	if targetVolume != baseVolume {
		return ConditionIndeterminate
	}
	if target == base {
		return ConditionTrue
	}
	prefix := strings.TrimSuffix(base, "/") + "/"
	return conditionFromBool(strings.HasPrefix(target, prefix))
}

func normalizeRuntimePath(raw string, style PathStyle) (string, string, bool) {
	if raw == "" || !utf8.ValidString(raw) || containsControl(raw) {
		return "", "", false
	}
	switch style {
	case PathRepository:
		if strings.HasPrefix(raw, "/") || strings.Contains(raw, `\`) || strings.Contains(raw, ":") ||
			validateCanonicalPathSegments(raw, false) != nil {
			return "", "", false
		}
		return raw, "", true
	case PathPOSIX:
		if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, `\`) ||
			validateCanonicalPathSegments(strings.TrimPrefix(raw, "/"), true) != nil {
			return "", "", false
		}
		return raw, "/", true
	case PathWindows:
		normalized, err := normalizeWindowsPathBase(raw)
		if err != nil {
			return "", "", false
		}
		if strings.HasPrefix(normalized, "//") {
			parts := strings.Split(strings.TrimPrefix(normalized, "//"), "/")
			return normalized, "//" + parts[0] + "/" + parts[1], true
		}
		return normalized, strings.ToUpper(normalized[:2]), true
	default:
		return "", "", false
	}
}

func containsSorted(values []string, want string) bool {
	low, high := 0, len(values)
	for low < high {
		middle := int(uint(low+high) >> 1)
		if values[middle] < want {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low < len(values) && values[low] == want
}

func containsPort(values []uint16, want uint16) bool {
	low, high := 0, len(values)
	for low < high {
		middle := int(uint(low+high) >> 1)
		if values[middle] < want {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low < len(values) && values[low] == want
}

func matchesAnyPathPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if value == prefix || prefix == "/" || strings.HasPrefix(value, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func conditionFromBool(value bool) ConditionState {
	if value {
		return ConditionTrue
	}
	return ConditionFalse
}

func reasonForMatcher(targetValid, matcherValid bool) ReasonCode {
	if !matcherValid {
		return ReasonInternalInvariant
	}
	if !targetValid {
		return ReasonConditionIndeterminate
	}
	return ""
}

func indeterminatePredicate(
	reason ReasonCode,
	actual Provenance,
	required Provenance,
	summary OperandSummary,
) predicateEvaluation {
	return predicateEvaluation{
		state: ConditionIndeterminate, reason: reason, actual: actual,
		required: required, complete: false, summary: summary,
	}
}

func summarizePointer(pointer PointerResult) OperandSummary {
	summary := OperandSummary{PointerState: pointer.State}
	if pointer.State != PointerPresent && pointer.State != PointerNull {
		return summary
	}
	summary.Kind = pointer.Value.Kind()
	body, err := pointer.Value.MarshalJSON()
	if err == nil {
		summary.ByteLength = len(body)
	}
	if items, ok := pointer.Value.Items(); ok {
		summary.ItemCount = len(items)
	}
	if members, ok := pointer.Value.Members(); ok {
		summary.ItemCount = len(members)
	}
	return summary
}
