package action

func evaluateConditionTree(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
) conditionEvaluation {
	return evaluateConditionTreeCore(condition, request, decision, depth, nil, true, true, nil)
}

func evaluateConditionTreeWithRoots(
	condition *CompiledCondition,
	request Request,
	decision Decision,
	depth int,
	roots *predicateRoots,
) conditionEvaluation {
	return evaluateConditionTreeWithRootsControl(condition, request, decision, depth, roots, nil)
}
