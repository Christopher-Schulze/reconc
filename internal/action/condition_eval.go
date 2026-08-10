package action

type conditionEvaluation struct {
	state    ConditionState
	reason   ReasonCode
	actual   Provenance
	required Provenance
	complete bool
	summary  OperandSummary
	nodes    int
}

func evaluateConditionTree(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
) conditionEvaluation {
	if condition == nil {
		return conditionEvaluation{state: ConditionTrue, complete: true}
	}
	if depth > MaxConditionDepth {
		return invalidConditionEvaluation(1)
	}
	switch condition.Kind {
	case ConditionPredicate:
		return evaluatePredicateCondition(condition, request, decision)
	case ConditionNot:
		return evaluateNotCondition(condition, request, decision, depth)
	case ConditionAll, ConditionAny:
		return evaluateLogicalCondition(condition, request, decision, depth)
	default:
		return invalidConditionEvaluation(1)
	}
}

func evaluatePredicateCondition(
	condition *CompiledCondition,
	request Request,
	decision Decision,
) conditionEvaluation {
	if condition.Predicate == nil || len(condition.Children) != 0 {
		return invalidConditionEvaluation(1)
	}
	predicate := evaluatePredicate(condition.Predicate, request, decision)
	return conditionEvaluation{
		state: predicate.state, reason: predicate.reason,
		actual: predicate.actual, required: predicate.required,
		complete: predicate.complete, summary: predicate.summary, nodes: 1,
	}
}

func evaluateNotCondition(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
) conditionEvaluation {
	if condition.Predicate != nil || len(condition.Children) != 1 {
		return invalidConditionEvaluation(1)
	}
	child := evaluateConditionTree(condition.Children[0], request, decision, depth+1)
	child.nodes++
	if child.nodes > MaxConditionNodes {
		return invalidConditionEvaluation(child.nodes)
	}
	if child.state == ConditionTrue {
		child.state = ConditionFalse
	} else if child.state == ConditionFalse {
		child.state = ConditionTrue
	}
	return child
}

func evaluateLogicalCondition(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
) conditionEvaluation {
	if condition.Predicate != nil || len(condition.Children) == 0 {
		return invalidConditionEvaluation(1)
	}
	children := make([]conditionEvaluation, len(condition.Children))
	nodes := 1
	for index, child := range condition.Children {
		children[index] = evaluateConditionTree(child, request, decision, depth+1)
		nodes += children[index].nodes
		if nodes > MaxConditionNodes {
			return invalidConditionEvaluation(nodes)
		}
	}
	return combineConditionChildren(condition.Kind, children, nodes)
}

func combineConditionChildren(
	kind ConditionKind,
	children []conditionEvaluation,
	nodes int,
) conditionEvaluation {
	result := conditionEvaluation{complete: true, nodes: nodes}
	if kind == ConditionAll {
		result.state = ConditionTrue
	} else {
		result.state = ConditionFalse
	}
	for _, child := range children {
		result.complete = result.complete && child.complete
		mergeConditionMetadata(&result, child)
		if kind == ConditionAll {
			if child.state == ConditionFalse {
				result.state = ConditionFalse
			} else if child.state == ConditionIndeterminate && result.state == ConditionTrue {
				result.state = ConditionIndeterminate
			}
			continue
		}
		if child.state == ConditionTrue {
			result.state = ConditionTrue
		} else if child.state == ConditionIndeterminate && result.state == ConditionFalse {
			result.state = ConditionIndeterminate
		}
	}
	if result.state != ConditionIndeterminate {
		result.reason = ""
		result.complete = true
	}
	return result
}

func mergeConditionMetadata(target *conditionEvaluation, child conditionEvaluation) {
	if conditionReasonStrength(child.reason) > conditionReasonStrength(target.reason) {
		target.reason = child.reason
		target.summary = child.summary
	}
	if child.actual.Valid() && (!target.actual.Valid() || child.actual.Rank() < target.actual.Rank()) {
		target.actual = child.actual
	}
	if child.required.Valid() && (!target.required.Valid() || child.required.Rank() > target.required.Rank()) {
		target.required = child.required
	}
}

func conditionReasonStrength(reason ReasonCode) int {
	switch reason {
	case ReasonInternalInvariant:
		return 4
	case ReasonContextUntrusted:
		return 3
	case ReasonIdentityUnavailable:
		return 2
	case ReasonConditionIndeterminate:
		return 1
	default:
		return 0
	}
}

func invalidConditionEvaluation(nodes int) conditionEvaluation {
	return conditionEvaluation{
		state: ConditionIndeterminate, reason: ReasonInternalInvariant,
		complete: false, nodes: nodes,
	}
}

func validateCompiledCondition(condition *CompiledCondition, depth int) (int, bool) {
	if condition == nil {
		return 0, true
	}
	if depth > MaxConditionDepth {
		return 1, false
	}
	nodes := 1
	switch condition.Kind {
	case ConditionPredicate:
		if condition.Predicate == nil || len(condition.Children) != 0 ||
			!validateCompiledPredicate(condition.Predicate) {
			return nodes, false
		}
	case ConditionNot:
		if condition.Predicate != nil || len(condition.Children) != 1 {
			return nodes, false
		}
		childNodes, ok := validateCompiledCondition(condition.Children[0], depth+1)
		nodes += childNodes
		if !ok {
			return nodes, false
		}
	case ConditionAll, ConditionAny:
		if condition.Predicate != nil || len(condition.Children) == 0 {
			return nodes, false
		}
		for _, child := range condition.Children {
			childNodes, ok := validateCompiledCondition(child, depth+1)
			nodes += childNodes
			if !ok || nodes > MaxConditionNodes {
				return nodes, false
			}
		}
	default:
		return nodes, false
	}
	return nodes, nodes <= MaxConditionNodes
}

func validateCompiledPredicate(predicate *CompiledPredicate) bool {
	if predicate == nil || !predicate.Predicate.Source.Valid() || !predicate.Predicate.Op.Valid() ||
		validateCompiledPointer(predicate.Tokens) != nil || !compiledProvenanceValid(predicate.Predicate) {
		return false
	}
	op := predicate.Predicate.Op
	if op == OperatorExists {
		return predicate.Predicate.Value == nil && compiledMatcherCount(predicate) == 0
	}
	if predicate.Predicate.Value == nil {
		return false
	}
	switch op {
	case OperatorEqual, OperatorNotEqual:
		return compiledMatcherCount(predicate) == 0
	case OperatorIn, OperatorNotIn:
		return compiledMatcherCount(predicate) == 0 && validMembershipOperand(*predicate.Predicate.Value)
	case OperatorPrefix, OperatorSuffix, OperatorContains:
		_, ok := predicate.Predicate.Value.Text()
		return ok && compiledMatcherCount(predicate) == 0
	case OperatorGlob:
		return predicate.Glob != nil && compiledMatcherCount(predicate) == 1
	case OperatorRegex:
		return predicate.Regex != nil && compiledMatcherCount(predicate) == 1
	case OperatorGreater, OperatorGreaterEq, OperatorLess, OperatorLessEq:
		_, ok := predicate.Predicate.Value.Decimal()
		return ok && compiledMatcherCount(predicate) == 0
	case OperatorURL:
		return predicate.URL != nil && compiledMatcherCount(predicate) == 1
	case OperatorCIDR:
		return len(predicate.CIDRs) > 0 && len(predicate.CIDRs) <= MaxListValues &&
			compiledMatcherCount(predicate) == 1
	case OperatorPathWithin:
		return predicate.Path != nil && compiledMatcherCount(predicate) == 1
	default:
		return false
	}
}

func compiledProvenanceValid(predicate Predicate) bool {
	if predicate.Source == SourceContext {
		return predicate.MinimumProvenance.Valid()
	}
	return predicate.MinimumProvenance == ""
}

func compiledMatcherCount(predicate *CompiledPredicate) int {
	count := 0
	for _, present := range []bool{
		predicate.Regex != nil, predicate.Glob != nil, predicate.URL != nil,
		len(predicate.CIDRs) > 0, predicate.Path != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func validMembershipOperand(value Value) bool {
	items, ok := value.Items()
	if !ok || len(items) == 0 || len(items) > MaxListValues || !items[0].Scalar() {
		return false
	}
	for index, item := range items {
		if !item.Scalar() || item.Kind() != items[0].Kind() ||
			index > 0 && items[index-1].Equal(item) {
			return false
		}
	}
	return true
}
