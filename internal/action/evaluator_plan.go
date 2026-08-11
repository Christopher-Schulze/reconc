package action

// Plan returns the evaluator's immutable canonical contract as a defensive
// copy. Runtime evidence adapters use it to derive deterministic fixtures
// without gaining access to matcher internals.
func (e *Evaluator) Plan() Plan {
	if e == nil {
		return Plan{}
	}
	return clonePlan(e.plan)
}
