package action

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func CompilePointer(pointer string) ([]string, error) {
	if !utf8.ValidString(pointer) || len(pointer) > MaxPointerBytes {
		return nil, fmt.Errorf("JSON Pointer must be valid UTF-8 and at most %d bytes", MaxPointerBytes)
	}
	if pointer == "" {
		return []string{}, nil
	}
	if pointer[0] != '/' {
		return nil, fmt.Errorf("JSON Pointer must be empty or begin with /")
	}
	encoded := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(encoded))
	for index, token := range encoded {
		var decoded strings.Builder
		for offset := 0; offset < len(token); offset++ {
			if token[offset] != '~' {
				decoded.WriteByte(token[offset])
				continue
			}
			if offset+1 >= len(token) || token[offset+1] != '0' && token[offset+1] != '1' {
				return nil, fmt.Errorf("JSON Pointer contains invalid tilde escape")
			}
			if token[offset+1] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
			offset++
		}
		tokens[index] = decoded.String()
	}
	return tokens, nil
}

func compileCondition(condition *Condition, decision Decision, phases []Phase, depth int) (*CompiledCondition, int, error) {
	if condition == nil {
		return nil, 0, fmt.Errorf("condition is nil")
	}
	if depth > MaxConditionDepth {
		return nil, 0, fmt.Errorf("condition exceeds depth %d", MaxConditionDepth)
	}
	present := 0
	if condition.All != nil {
		present++
	}
	if condition.Any != nil {
		present++
	}
	if condition.Not != nil {
		present++
	}
	if condition.Predicate != nil {
		present++
	}
	if present != 1 {
		return nil, 0, fmt.Errorf("condition must contain exactly one of all, any, not, or predicate")
	}
	compiled := &CompiledCondition{}
	nodes := 1
	switch {
	case condition.All != nil:
		if len(condition.All) == 0 {
			return nil, 0, fmt.Errorf("all must contain at least one child")
		}
		compiled.Kind = ConditionAll
		children, count, err := compileChildren(condition.All, decision, phases, depth)
		if err != nil {
			return nil, 0, err
		}
		condition.All = conditionsFromCompiled(children)
		compiled.Children = children
		nodes += count
	case condition.Any != nil:
		if len(condition.Any) == 0 {
			return nil, 0, fmt.Errorf("any must contain at least one child")
		}
		compiled.Kind = ConditionAny
		children, count, err := compileChildren(condition.Any, decision, phases, depth)
		if err != nil {
			return nil, 0, err
		}
		condition.Any = conditionsFromCompiled(children)
		compiled.Children = children
		nodes += count
	case condition.Not != nil:
		compiled.Kind = ConditionNot
		child, count, err := compileCondition(condition.Not, decision, phases, depth+1)
		if err != nil {
			return nil, 0, err
		}
		compiled.Children = []*CompiledCondition{child}
		nodes += count
	case condition.Predicate != nil:
		compiled.Kind = ConditionPredicate
		predicate, err := compilePredicate(condition.Predicate, decision, phases)
		if err != nil {
			return nil, 0, err
		}
		compiled.Predicate = predicate
	}
	if nodes > MaxConditionNodes {
		return nil, 0, fmt.Errorf("condition contains %d nodes; maximum is %d", nodes, MaxConditionNodes)
	}
	return compiled, nodes, nil
}

func compileChildren(input []Condition, decision Decision, phases []Phase, depth int) ([]*CompiledCondition, int, error) {
	type pair struct {
		condition Condition
		compiled  *CompiledCondition
		key       string
		nodes     int
	}
	pairs := make([]pair, len(input))
	total := 0
	for index := range input {
		compiled, nodes, err := compileCondition(&input[index], decision, phases, depth+1)
		if err != nil {
			return nil, 0, fmt.Errorf("condition child %d: %w", index, err)
		}
		body, err := json.Marshal(input[index])
		if err != nil {
			return nil, 0, err
		}
		pairs[index] = pair{condition: input[index], compiled: compiled, key: string(body), nodes: nodes}
		total += nodes
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
	out := make([]*CompiledCondition, len(pairs))
	for index := range pairs {
		input[index] = pairs[index].condition
		out[index] = pairs[index].compiled
	}
	return out, total, nil
}

func conditionsFromCompiled(values []*CompiledCondition) []Condition {
	out := make([]Condition, len(values))
	for index := range values {
		out[index] = values[index].sourceCondition()
	}
	return out
}

func (c *CompiledCondition) sourceCondition() Condition {
	if c == nil {
		return Condition{}
	}
	switch c.Kind {
	case ConditionAll:
		return Condition{All: conditionsFromCompiled(c.Children)}
	case ConditionAny:
		return Condition{Any: conditionsFromCompiled(c.Children)}
	case ConditionNot:
		child := c.Children[0].sourceCondition()
		return Condition{Not: &child}
	case ConditionPredicate:
		predicate := c.Predicate.Predicate
		return Condition{Predicate: &predicate}
	default:
		return Condition{}
	}
}

func compilePredicate(predicate *Predicate, decision Decision, phases []Phase) (*CompiledPredicate, error) {
	if !predicate.Source.Valid() || !predicate.Op.Valid() {
		return nil, fmt.Errorf("predicate source or operator is invalid")
	}
	tokens, err := CompilePointer(predicate.Pointer)
	if err != nil {
		return nil, fmt.Errorf("predicate.pointer: %w", err)
	}
	if predicate.Source != SourceContext && predicate.MinimumProvenance != "" {
		return nil, fmt.Errorf("minimum_provenance is valid only for context")
	}
	if predicate.Source == SourceContext {
		minimum := ProvenanceAgentSupplied
		if decision == DecisionAllow {
			minimum = ProvenanceHostObserved
		}
		if predicate.MinimumProvenance == "" {
			predicate.MinimumProvenance = minimum
		}
		if !predicate.MinimumProvenance.Valid() || predicate.MinimumProvenance.Rank() < minimum.Rank() {
			return nil, fmt.Errorf("minimum_provenance is below the safe default %s", minimum)
		}
	} else if decision == DecisionAllow {
		return nil, fmt.Errorf("allow rules cannot depend on arguments, result, or progress")
	}
	for _, phase := range phases {
		if !sourceAllowedInPhase(predicate.Source, phase) {
			return nil, fmt.Errorf("predicate source %s is unsupported in phase %s", predicate.Source, phase)
		}
	}
	compiled := &CompiledPredicate{Predicate: *predicate, Tokens: tokens}
	if err := compileOperand(compiled); err != nil {
		return nil, err
	}
	predicate.Value = compiled.Predicate.Value
	return compiled, nil
}

func sourceAllowedInPhase(source ValueSource, phase Phase) bool {
	if source == SourceContext {
		return true
	}
	switch phase {
	case PhasePreCall:
		return source == SourceArguments
	case PhasePostResult:
		return source == SourceResult
	case PhaseProgress:
		return source == SourceProgress
	case PhaseObservation:
		return false
	default:
		return false
	}
}

func compileOperand(compiled *CompiledPredicate) error {
	predicate := &compiled.Predicate
	if predicate.Op == OperatorExists {
		if predicate.Value != nil {
			return fmt.Errorf("exists forbids value")
		}
		return nil
	}
	if predicate.Value == nil {
		return fmt.Errorf("operator %s requires value", predicate.Op)
	}
	switch predicate.Op {
	case OperatorEqual, OperatorNotEqual:
		return nil
	case OperatorIn, OperatorNotIn:
		return normalizeScalarOperandList(predicate)
	case OperatorPrefix, OperatorSuffix, OperatorContains:
		_, ok := predicate.Value.Text()
		if !ok {
			return fmt.Errorf("operator %s requires a string value", predicate.Op)
		}
	case OperatorGlob:
		pattern, ok := predicate.Value.Text()
		if !ok {
			return fmt.Errorf("glob requires a string pattern")
		}
		matcher, err := compileGlob(pattern)
		if err != nil {
			return err
		}
		compiled.Glob = matcher
	case OperatorRegex:
		pattern, ok := predicate.Value.Text()
		if !ok || len(pattern) > MaxPatternBytes {
			return fmt.Errorf("regex requires a string pattern of at most %d bytes", MaxPatternBytes)
		}
		matcher, err := regexpCompile(`\A(?:` + pattern + `)\z`)
		if err != nil {
			return fmt.Errorf("regex pattern is invalid: %w", err)
		}
		compiled.Regex = matcher
	case OperatorGreater, OperatorGreaterEq, OperatorLess, OperatorLessEq:
		if _, ok := predicate.Value.Decimal(); !ok {
			return fmt.Errorf("operator %s requires a number value", predicate.Op)
		}
	case OperatorCIDR:
		cidrs, value, err := normalizeCIDRs(*predicate.Value)
		if err != nil {
			return err
		}
		predicate.Value = &value
		compiled.CIDRs = cidrs
	case OperatorURL:
		constraint, value, err := normalizeURLConstraint(*predicate.Value)
		if err != nil {
			return err
		}
		predicate.Value = &value
		compiled.URL = constraint
	case OperatorPathWithin:
		constraint, value, err := normalizePathConstraint(*predicate.Value)
		if err != nil {
			return err
		}
		predicate.Value = &value
		compiled.Path = constraint
	}
	return nil
}

func normalizeScalarOperandList(predicate *Predicate) error {
	values, ok := predicate.Value.Items()
	if !ok || len(values) == 0 || len(values) > MaxListValues {
		return fmt.Errorf("operator %s requires 1 to %d scalar values", predicate.Op, MaxListValues)
	}
	kind := values[0].Kind()
	if !values[0].Scalar() {
		return fmt.Errorf("operator %s requires non-null scalar values", predicate.Op)
	}
	for index := range values {
		if !values[index].Scalar() || values[index].Kind() != kind {
			return fmt.Errorf("operator %s requires same-type scalar values", predicate.Op)
		}
	}
	type keyedValue struct {
		value Value
		key   []byte
	}
	keyed := make([]keyedValue, len(values))
	for index := range values {
		key, err := values[index].MarshalJSON()
		if err != nil {
			return fmt.Errorf("operator %s value %d: %w", predicate.Op, index, err)
		}
		keyed[index] = keyedValue{value: values[index], key: key}
	}
	sort.Slice(keyed, func(i, j int) bool { return bytes.Compare(keyed[i].key, keyed[j].key) < 0 })
	for index := range keyed {
		values[index] = keyed[index].value
	}
	for index := 1; index < len(values); index++ {
		if values[index-1].Equal(values[index]) {
			return fmt.Errorf("operator %s contains a duplicate value", predicate.Op)
		}
	}
	value, _ := Array(values)
	predicate.Value = &value
	return nil
}

func normalizeCIDRs(value Value) ([]netip.Prefix, Value, error) {
	items, ok := value.Items()
	if !ok || len(items) == 0 || len(items) > MaxListValues {
		return nil, Value{}, fmt.Errorf("cidr requires 1 to %d prefix strings", MaxListValues)
	}
	prefixes := make([]netip.Prefix, len(items))
	for index := range items {
		text, ok := items[index].Text()
		if !ok || strings.Contains(text, "%") {
			return nil, Value{}, fmt.Errorf("cidr value %d is not a canonical prefix string", index)
		}
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return nil, Value{}, fmt.Errorf("cidr value %d is invalid: %w", index, err)
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return nil, Value{}, fmt.Errorf("cidr value %d crosses the IPv4-mapped boundary", index)
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		prefixes[index] = prefix.Masked()
		canonical := prefixes[index].String()
		items[index], _ = String(canonical)
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].String() < prefixes[j].String() })
	sort.Slice(items, func(i, j int) bool {
		left, _ := items[i].Text()
		right, _ := items[j].Text()
		return left < right
	})
	for index := 1; index < len(prefixes); index++ {
		if prefixes[index-1] == prefixes[index] {
			return nil, Value{}, fmt.Errorf("cidr contains duplicate canonical prefix %s", prefixes[index].String())
		}
	}
	normalized, _ := Array(items)
	return prefixes, normalized, nil
}

func normalizeURLConstraint(value Value) (*URLConstraint, Value, error) {
	members, ok := value.Members()
	if !ok {
		return nil, Value{}, fmt.Errorf("url requires a constraint object")
	}
	allowed := map[string]bool{"schemes": true, "hosts": true, "ports": true, "path_prefixes": true, "allow_query": true, "allow_ip_literals": true}
	for _, member := range members {
		if !allowed[member.Name] {
			return nil, Value{}, fmt.Errorf("url constraint contains unknown field %q", member.Name)
		}
	}
	schemes, err := requiredStringArray(value, "schemes")
	if err != nil {
		return nil, Value{}, err
	}
	hosts, err := requiredStringArray(value, "hosts")
	if err != nil {
		return nil, Value{}, err
	}
	for index := range schemes {
		if schemes[index] != strings.ToLower(schemes[index]) || !urlSchemePattern.MatchString(schemes[index]) {
			return nil, Value{}, fmt.Errorf("url scheme %q is not canonical lowercase ASCII", schemes[index])
		}
	}
	for index := range hosts {
		rawHost := hosts[index]
		hosts[index], err = normalizeConstraintHost(rawHost)
		if err != nil {
			return nil, Value{}, fmt.Errorf("url host %q is invalid: %w", rawHost, err)
		}
	}
	allowQuery, err := requiredBool(value, "allow_query")
	if err != nil {
		return nil, Value{}, err
	}
	allowIP, err := requiredBool(value, "allow_ip_literals")
	if err != nil {
		return nil, Value{}, err
	}
	ports, err := optionalPorts(value)
	if err != nil {
		return nil, Value{}, err
	}
	paths, err := optionalStringArray(value, "path_prefixes")
	if err != nil {
		return nil, Value{}, err
	}
	for index := range paths {
		paths[index], err = normalizeURLPathPrefix(paths[index])
		if err != nil {
			return nil, Value{}, err
		}
	}
	sort.Strings(schemes)
	sort.Strings(hosts)
	sort.Strings(paths)
	if duplicateString(schemes) || duplicateString(hosts) || duplicateString(paths) {
		return nil, Value{}, fmt.Errorf("url constraint lists must not contain duplicates")
	}
	constraint := &URLConstraint{Schemes: schemes, Hosts: hosts, Ports: ports, PathPrefixes: paths, AllowQuery: allowQuery, AllowIPLiteral: allowIP}
	normalized, err := urlConstraintValue(*constraint)
	return constraint, normalized, err
}

func normalizePathConstraint(value Value) (*PathConstraint, Value, error) {
	members, ok := value.Members()
	if !ok {
		return nil, Value{}, fmt.Errorf("path_within requires a constraint object")
	}
	for _, member := range members {
		if member.Name != "style" && member.Name != "base" && member.Name != "case_sensitive" {
			return nil, Value{}, fmt.Errorf("path constraint contains unknown field %q", member.Name)
		}
	}
	styleText, err := requiredString(value, "style")
	if err != nil {
		return nil, Value{}, err
	}
	base, err := requiredString(value, "base")
	if err != nil {
		return nil, Value{}, err
	}
	caseSensitive, err := requiredBool(value, "case_sensitive")
	if err != nil {
		return nil, Value{}, err
	}
	style := PathStyle(styleText)
	if containsControl(base) {
		return nil, Value{}, fmt.Errorf("path base contains a control character")
	}
	switch style {
	case PathRepository:
		if strings.HasPrefix(base, "/") || strings.Contains(base, `\`) || strings.Contains(base, ":") {
			return nil, Value{}, fmt.Errorf("repository path base must be slash-separated and relative")
		}
		if err := validateCanonicalPathSegments(base, false); err != nil {
			return nil, Value{}, fmt.Errorf("repository path base is ambiguous: %w", err)
		}
		base = path.Clean(base)
	case PathPOSIX:
		if !strings.HasPrefix(base, "/") || strings.HasPrefix(base, "//") || strings.Contains(base, `\`) {
			return nil, Value{}, fmt.Errorf("posix path base must be absolute")
		}
		if err := validateCanonicalPathSegments(strings.TrimPrefix(base, "/"), true); err != nil {
			return nil, Value{}, fmt.Errorf("posix path base is ambiguous: %w", err)
		}
		base = path.Clean(base)
	case PathWindows:
		base, err = normalizeWindowsPathBase(base)
		if err != nil {
			return nil, Value{}, err
		}
	default:
		return nil, Value{}, fmt.Errorf("path style must be repository, posix, or windows")
	}
	constraint, err := preparePathConstraint(PathConstraint{Style: style, Base: base, CaseSensitive: caseSensitive})
	if err != nil {
		return nil, Value{}, err
	}
	normalized, err := pathConstraintValue(*constraint)
	return constraint, normalized, err
}

func preparePathConstraint(constraint PathConstraint) (*PathConstraint, error) {
	base, volume, ok := normalizeRuntimePath(constraint.Base, constraint.Style)
	if !ok {
		return nil, fmt.Errorf("path constraint base is invalid")
	}
	if !constraint.CaseSensitive {
		base = strings.ToLower(base)
		volume = strings.ToLower(volume)
	}
	constraint.matchBase = base
	constraint.matchVolume = volume
	constraint.matchPrefix = strings.TrimSuffix(base, "/") + "/"
	constraint.prepared = true
	return &constraint, nil
}

func requiredStringArray(value Value, field string) ([]string, error) {
	items, ok := value.Lookup(field)
	if !ok {
		return nil, fmt.Errorf("url constraint requires %s", field)
	}
	return stringArray(items, field)
}

func optionalStringArray(value Value, field string) ([]string, error) {
	items, ok := value.Lookup(field)
	if !ok {
		return nil, nil
	}
	return stringArray(items, field)
}

func stringArray(value Value, field string) ([]string, error) {
	items, ok := value.Items()
	if !ok || len(items) == 0 || len(items) > MaxListValues {
		return nil, fmt.Errorf("%s must contain 1 to %d strings", field, MaxListValues)
	}
	out := make([]string, len(items))
	for index := range items {
		text, ok := items[index].Text()
		if !ok || text == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", field, index)
		}
		out[index] = text
	}
	return out, nil
}

func requiredString(value Value, field string) (string, error) {
	selected, ok := value.Lookup(field)
	if !ok {
		return "", fmt.Errorf("constraint requires %s", field)
	}
	text, ok := selected.Text()
	if !ok || text == "" {
		return "", fmt.Errorf("constraint field %s must be a non-empty string", field)
	}
	return text, nil
}

func requiredBool(value Value, field string) (bool, error) {
	selected, ok := value.Lookup(field)
	if !ok {
		return false, fmt.Errorf("constraint requires %s", field)
	}
	boolean, ok := selected.Bool()
	if !ok {
		return false, fmt.Errorf("constraint field %s must be a boolean", field)
	}
	return boolean, nil
}

func optionalPorts(value Value) ([]uint16, error) {
	selected, ok := value.Lookup("ports")
	if !ok {
		return nil, nil
	}
	items, ok := selected.Items()
	if !ok || len(items) == 0 || len(items) > MaxListValues {
		return nil, fmt.Errorf("ports must contain 1 to %d integers", MaxListValues)
	}
	ports := make([]uint16, len(items))
	for index := range items {
		decimal, ok := items[index].Decimal()
		if !ok || decimal.negative || decimal.exponent != 0 {
			return nil, fmt.Errorf("ports[%d] must be an integer", index)
		}
		port, err := strconv.ParseUint(decimal.coefficient, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("ports[%d] must be between 1 and 65535", index)
		}
		ports[index] = uint16(port)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	for index := 1; index < len(ports); index++ {
		if ports[index-1] == ports[index] {
			return nil, fmt.Errorf("ports contains duplicate value %d", ports[index])
		}
	}
	return ports, nil
}

func urlConstraintValue(constraint URLConstraint) (Value, error) {
	members := []Member{}
	for _, pair := range []struct {
		name   string
		values []string
	}{{"schemes", constraint.Schemes}, {"hosts", constraint.Hosts}, {"path_prefixes", constraint.PathPrefixes}} {
		if len(pair.values) == 0 && pair.name == "path_prefixes" {
			continue
		}
		items := make([]Value, len(pair.values))
		for index, item := range pair.values {
			items[index], _ = String(item)
		}
		array, _ := Array(items)
		members = append(members, Member{Name: pair.name, Value: array})
	}
	if len(constraint.Ports) > 0 {
		items := make([]Value, len(constraint.Ports))
		for index, port := range constraint.Ports {
			decimal, _ := ParseDecimal(strconv.Itoa(int(port)))
			items[index] = Number(decimal)
		}
		array, _ := Array(items)
		members = append(members, Member{Name: "ports", Value: array})
	}
	members = append(members,
		Member{Name: "allow_query", Value: Boolean(constraint.AllowQuery)},
		Member{Name: "allow_ip_literals", Value: Boolean(constraint.AllowIPLiteral)},
	)
	return Object(members)
}

func pathConstraintValue(constraint PathConstraint) (Value, error) {
	style, _ := String(string(constraint.Style))
	base, _ := String(constraint.Base)
	return Object([]Member{
		{Name: "style", Value: style},
		{Name: "base", Value: base},
		{Name: "case_sensitive", Value: Boolean(constraint.CaseSensitive)},
	})
}

func duplicateString(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

func normalizeConstraintHost(host string) (string, error) {
	host = strings.ToLower(host)
	if strings.HasSuffix(host, ".") {
		trimmed := strings.TrimSuffix(host, ".")
		if _, err := netip.ParseAddr(trimmed); err == nil {
			return "", fmt.Errorf("IP literals cannot contain a trailing DNS dot")
		}
		host = trimmed
	}
	if host == "" || len(host) > 253 || !asciiHostPattern.MatchString(host) {
		return "", fmt.Errorf("must be non-empty canonical ASCII")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" {
			return "", fmt.Errorf("zones are forbidden")
		}
		return address.Unmap().String(), nil
	}
	if strings.Contains(host, ":") || onlyDigitsAndDots(host) {
		return "", fmt.Errorf("malformed IP literal")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("malformed DNS label")
		}
	}
	return host, nil
}

func normalizeURLPathPrefix(raw string) (string, error) {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || containsControl(raw) || strings.Contains(raw, `\`) {
		return "", fmt.Errorf("url path prefix %q must be absolute and unambiguous", raw)
	}
	if raw == "/" {
		return raw, nil
	}
	segments := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	for index := range segments {
		if segments[index] == "" {
			return "", fmt.Errorf("url path prefix %q contains an empty segment", raw)
		}
		if containsAmbiguousPercentEscape(segments[index]) {
			return "", fmt.Errorf("url path prefix %q contains an ambiguous escape", raw)
		}
		decoded, err := url.PathUnescape(segments[index])
		if err != nil || !utf8.ValidString(decoded) || decoded == "." || decoded == ".." ||
			containsControl(decoded) || strings.ContainsAny(decoded, `/\`) {
			return "", fmt.Errorf("url path prefix %q contains an invalid segment", raw)
		}
		segments[index] = decoded
	}
	return "/" + strings.Join(segments, "/"), nil
}

func containsAmbiguousPercentEscape(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) {
			return true
		}
		decoded, err := strconv.ParseUint(value[index+1:index+3], 16, 8)
		if err != nil {
			return true
		}
		if decoded == '/' || decoded == '\\' || decoded == 0 || decoded == '%' || decoded < 0x20 || decoded == 0x7f {
			return true
		}
		index += 2
	}
	return false
}

func normalizeWindowsPathBase(raw string) (string, error) {
	normalized := strings.ReplaceAll(raw, `\`, "/")
	if containsControl(normalized) {
		return "", fmt.Errorf("windows path base contains a control character")
	}
	if strings.HasPrefix(normalized, "//") {
		parts := strings.Split(strings.TrimPrefix(normalized, "//"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", fmt.Errorf("windows path base must contain an absolute UNC server and share")
		}
		if err := validateCanonicalPathSegments(strings.Join(parts, "/"), false); err != nil || strings.Contains(normalized, ":") {
			return "", fmt.Errorf("windows UNC path base is ambiguous or contains an alternate stream")
		}
		return "//" + strings.Join(parts, "/"), nil
	}
	if !windowsDrivePattern.MatchString(normalized) || strings.Contains(normalized[2:], ":") {
		return "", fmt.Errorf("windows path base must be an absolute drive or UNC path without alternate streams")
	}
	if err := validateCanonicalPathSegments(normalized[3:], true); err != nil {
		return "", fmt.Errorf("windows drive path base is ambiguous: %w", err)
	}
	return strings.ToUpper(normalized[:1]) + normalized[1:], nil
}

func validateCanonicalPathSegments(value string, allowEmptyRoot bool) error {
	if value == "" && allowEmptyRoot {
		return nil
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("empty or dot segment")
		}
	}
	return nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if character == 0 || character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func onlyDigitsAndDots(value string) bool {
	for _, character := range value {
		if character != '.' && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

var (
	urlSchemePattern    = regexpMustCompile(`^[a-z][a-z0-9+.-]*$`)
	asciiHostPattern    = regexpMustCompile(`^[a-z0-9.:-]+$`)
	windowsDrivePattern = regexpMustCompile(`^[A-Za-z]:/`)
)

func regexpCompile(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
