package actionstate

import (
	"math"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

func cloneState(input State) State {
	out := input
	out.Budgets = append([]BudgetRecord(nil), input.Budgets...)
	if out.Budgets == nil {
		out.Budgets = []BudgetRecord{}
	}
	for index := range out.Budgets {
		out.Budgets[index].Scope = cloneBudgetScope(input.Budgets[index].Scope)
		out.Budgets[index].GenerationHistory = append(
			[]action.BudgetGeneration(nil), input.Budgets[index].GenerationHistory...,
		)
	}
	out.Reservations = append([]Reservation(nil), input.Reservations...)
	if out.Reservations == nil {
		out.Reservations = []Reservation{}
	}
	for index := range out.Reservations {
		out.Reservations[index] = cloneReservation(input.Reservations[index])
	}
	out.TerminalCalls = append([]TerminalCall(nil), input.TerminalCalls...)
	if out.TerminalCalls == nil {
		out.TerminalCalls = []TerminalCall{}
	}
	out.Approvals = append([]ApprovalRecord(nil), input.Approvals...)
	if out.Approvals == nil {
		out.Approvals = []ApprovalRecord{}
	}
	for index := range out.Approvals {
		out.Approvals[index].Request = cloneApprovalRequest(input.Approvals[index].Request)
	}
	return out
}

func cloneApprovalRequest(input actionapproval.Request) actionapproval.Request {
	out := input
	out.CredentialLabels = append([]string(nil), input.CredentialLabels...)
	out.SelectedArguments = append([]actionapproval.SelectedArgument(nil), input.SelectedArguments...)
	out.RuleIDs = append([]string(nil), input.RuleIDs...)
	return out
}

func cloneReservation(input Reservation) Reservation {
	out := input
	out.Charges = append([]ReservationCharge(nil), input.Charges...)
	return out
}

func cloneBudgetScope(input action.BudgetScope) action.BudgetScope {
	out := input
	out.CredentialLabels = append([]string(nil), input.CredentialLabels...)
	if out.CredentialLabels == nil {
		out.CredentialLabels = []string{}
	}
	return out
}

func saturatingUsageAdd(left, right action.BudgetUsage) action.BudgetUsage {
	return action.BudgetUsage{
		CallCount:     saturatingAdd(left.CallCount, right.CallCount),
		DeniedCount:   saturatingAdd(left.DeniedCount, right.DeniedCount),
		ApprovalCount: saturatingAdd(left.ApprovalCount, right.ApprovalCount),
		ArgumentBytes: saturatingAdd(left.ArgumentBytes, right.ArgumentBytes),
		ResultBytes:   saturatingAdd(left.ResultBytes, right.ResultBytes),
		CostUnits:     saturatingAdd(left.CostUnits, right.CostUnits),
		Concurrent:    saturatingAdd(left.Concurrent, right.Concurrent),
		RateWindow:    saturatingAdd(left.RateWindow, right.RateWindow),
	}
}

func checkedUsageAdd(left, right action.BudgetUsage) (action.BudgetUsage, bool) {
	out := saturatingUsageAdd(left, right)
	overflow := addOverflow(left.CallCount, right.CallCount) ||
		addOverflow(left.DeniedCount, right.DeniedCount) ||
		addOverflow(left.ApprovalCount, right.ApprovalCount) ||
		addOverflow(left.ArgumentBytes, right.ArgumentBytes) ||
		addOverflow(left.ResultBytes, right.ResultBytes) ||
		addOverflow(left.CostUnits, right.CostUnits) ||
		addOverflow(left.Concurrent, right.Concurrent) ||
		addOverflow(left.RateWindow, right.RateWindow)
	return out, overflow
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

func addOverflow(left, right uint64) bool {
	return math.MaxUint64-left < right
}

func dispatchUsage(input action.BudgetUsage) action.BudgetUsage {
	return action.BudgetUsage{
		CallCount: input.CallCount, ArgumentBytes: input.ArgumentBytes,
		CostUnits: input.CostUnits, RateWindow: input.RateWindow,
	}
}

func postDispatchReservation(input action.BudgetUsage) action.BudgetUsage {
	return action.BudgetUsage{ResultBytes: input.ResultBytes, Concurrent: input.Concurrent}
}
