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
	return evaluateConditionTreeCore(condition, request, decision, depth, nil, true, true)
}

func evaluateConditionTreeWithRoots(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
	roots *predicateRoots,
) conditionEvaluation {
	return evaluateConditionTreeCore(condition, request, decision, depth, roots, false, true)
}

func evaluateConditionTreeCore(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
	roots *predicateRoots,
	validatePointer bool,
	summaryRequired bool,
) conditionEvaluation {
	if condition == nil {
		return conditionEvaluation{state: ConditionTrue, complete: true}
	}
	if depth > MaxConditionDepth {
		return invalidConditionEvaluation(1)
	}
	switch condition.Kind {
	case ConditionPredicate:
		return evaluatePredicateConditionCore(
			condition, request, decision, roots, validatePointer, summaryRequired,
		)
	case ConditionNot:
		return evaluateNotConditionCore(
			condition, request, decision, depth, roots, validatePointer, summaryRequired,
		)
	case ConditionAll, ConditionAny:
		return evaluateLogicalConditionCore(condition, request, decision, depth, roots, validatePointer)
	default:
		return invalidConditionEvaluation(1)
	}
}

func evaluatePredicateConditionCore(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	roots *predicateRoots,
	validatePointer bool,
	summaryRequired bool,
) conditionEvaluation {
	if condition.Predicate == nil || len(condition.Children) != 0 {
		return invalidConditionEvaluation(1)
	}
	predicate := evaluatePredicateCore(
		condition.Predicate, request, decision, roots, validatePointer, summaryRequired,
	)
	return conditionEvaluation{
		state: predicate.state, reason: predicate.reason,
		actual: predicate.actual, required: predicate.required,
		complete: predicate.complete, summary: predicate.summary, nodes: 1,
	}
}

func evaluateNotConditionCore(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
	roots *predicateRoots,
	validatePointer bool,
	summaryRequired bool,
) conditionEvaluation {
	if condition.Predicate != nil || len(condition.Children) != 1 {
		return invalidConditionEvaluation(1)
	}
	child := evaluateConditionTreeCore(
		condition.Children[0], request, decision, depth+1,
		roots, validatePointer, summaryRequired,
	)
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

func evaluateLogicalConditionCore(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
	roots *predicateRoots,
	validatePointer bool,
) conditionEvaluation {
	if condition.Predicate != nil || len(condition.Children) == 0 {
		return invalidConditionEvaluation(1)
	}
	result := conditionEvaluation{complete: true, nodes: 1}
	if condition.Kind == ConditionAll {
		result.state = ConditionTrue
	} else {
		result.state = ConditionFalse
	}
	for index := 0; index < len(condition.Children); index++ {
		child := condition.Children[index]
		childResult := evaluateConditionTreeCore(
			child, request, decision, depth+1, roots, validatePointer, false,
		)
		result.nodes += childResult.nodes
		if result.nodes > MaxConditionNodes {
			return invalidConditionEvaluation(result.nodes)
		}
		combineConditionChild(&result, condition.Kind, childResult)
		if !conditionStateDecisive(condition.Kind, result.state) {
			continue
		}
		for index+1 < len(condition.Children) {
			metadata, safe := skippedConditionMetadata(condition.Children[index+1], depth+1)
			if !safe || result.nodes > MaxConditionNodes-metadata.nodes {
				break
			}
			result.nodes += metadata.nodes
			mergeConditionMetadata(&result, metadata)
			index++
		}
	}
	if result.state != ConditionIndeterminate {
		result.reason = ""
		result.complete = true
	}
	return result
}

func conditionStateDecisive(kind ConditionKind, state ConditionState) bool {
	return kind == ConditionAll && state == ConditionFalse ||
		kind == ConditionAny && state == ConditionTrue
}

// skippedConditionMetadata recognizes subtrees whose trace metadata is known
// without resolving request values or running operators. Root existence checks
// on request-owned values are always determinate, agent-supplied, and summary-
// free, so a decisive parent can merge their exact metadata without evaluating
// their truth values.
func skippedConditionMetadata(condition *CompiledCondition, depth int) (conditionEvaluation, bool) {
	if condition == nil {
		return conditionEvaluation{complete: true}, true
	}
	if depth > MaxConditionDepth {
		return conditionEvaluation{}, false
	}
	switch condition.Kind {
	case ConditionPredicate:
		predicate := condition.Predicate
		if predicate == nil || len(condition.Children) != 0 ||
			predicate.Predicate.Op != OperatorExists || len(predicate.Tokens) != 0 ||
			!requestOwnedValueSource(predicate.Predicate.Source) {
			return conditionEvaluation{}, false
		}
		return conditionEvaluation{
			actual: ProvenanceAgentSupplied, required: predicate.Predicate.MinimumProvenance,
			complete: true, nodes: 1,
		}, true
	case ConditionNot:
		if condition.Predicate != nil || len(condition.Children) != 1 {
			return conditionEvaluation{}, false
		}
		child, safe := skippedConditionMetadata(condition.Children[0], depth+1)
		if !safe || child.nodes >= MaxConditionNodes {
			return conditionEvaluation{}, false
		}
		child.nodes++
		return child, true
	case ConditionAll, ConditionAny:
		if condition.Predicate != nil || len(condition.Children) == 0 {
			return conditionEvaluation{}, false
		}
		result := conditionEvaluation{complete: true, nodes: 1}
		for _, child := range condition.Children {
			metadata, safe := skippedConditionMetadata(child, depth+1)
			if !safe || result.nodes > MaxConditionNodes-metadata.nodes {
				return conditionEvaluation{}, false
			}
			result.nodes += metadata.nodes
			mergeConditionMetadata(&result, metadata)
		}
		return result, true
	default:
		return conditionEvaluation{}, false
	}
}

func requestOwnedValueSource(source ValueSource) bool {
	return source == SourceArguments || source == SourceResult || source == SourceProgress
}

func combineConditionChild(result *conditionEvaluation, kind ConditionKind, child conditionEvaluation) {
	result.complete = result.complete && child.complete
	mergeConditionMetadata(result, child)
	if kind == ConditionAll {
		if child.state == ConditionFalse {
			result.state = ConditionFalse
		} else if child.state == ConditionIndeterminate && result.state == ConditionTrue {
			result.state = ConditionIndeterminate
		}
		return
	}
	if child.state == ConditionTrue {
		result.state = ConditionTrue
	} else if child.state == ConditionIndeterminate && result.state == ConditionFalse {
		result.state = ConditionIndeterminate
	}
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
		return 5
	case ReasonLimitExceeded:
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
