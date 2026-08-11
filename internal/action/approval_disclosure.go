package action

import (
	"fmt"
	"sort"
)

// ApprovalDisclosures returns the canonical disclosure declarations and the
// exact union of keyed-only argument pointers selected for one request.
func (p *CompiledPlan) ApprovalDisclosures(request Request) ([]ApprovalDisclosure, []string, error) {
	if p == nil {
		return nil, nil, fmt.Errorf("compiled action plan is unavailable")
	}
	if request.Phase != PhasePreCall && request.Phase != PhasePostResult {
		return []ApprovalDisclosure{}, []string{}, nil
	}
	key := ToolIdentityKey(Tool{
		Transport: request.Transport, Platform: request.Platform,
		ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
		Tool: request.Tool,
	})
	index, exists := p.toolByExact[key]
	if !exists || index < 0 || index >= len(p.plan.Tools) {
		return []ApprovalDisclosure{}, []string{}, nil
	}
	toolID := p.plan.Tools[index].ID
	disclosures := make([]ApprovalDisclosure, 0, len(p.approvals))
	pointers := make(map[string]struct{})
	for _, declaration := range p.approvals {
		if !selectorMatches(declaration.Selector, request, toolID) {
			continue
		}
		copy := declaration
		cloneSelector(&copy.Selector, declaration.Selector)
		copy.SelectedArguments = cloneSlice(declaration.SelectedArguments)
		disclosures = append(disclosures, copy)
		for _, pointer := range declaration.SelectedArguments {
			pointers[pointer] = struct{}{}
		}
	}
	selected := make([]string, 0, len(pointers))
	for pointer := range pointers {
		selected = append(selected, pointer)
	}
	sort.Strings(selected)
	if disclosures == nil {
		disclosures = []ApprovalDisclosure{}
	}
	return disclosures, selected, nil
}
