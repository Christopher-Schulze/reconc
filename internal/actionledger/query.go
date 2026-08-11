package actionledger

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
)

const (
	DefaultTailRecords = 20
	MaxTailRecords     = 1000
	TailReportFormat   = "reconc.action-ledger-tail/v1"
	StatsReportFormat  = "reconc.action-ledger-stats/v1"
)

// Filter contains only exact, safe ledger identifiers. For lifecycle queries,
// event, decision, and time predicates select calls while aggregation still
// uses every retained event for each selected call.
type Filter struct {
	CallID          string
	RunIdentity     string
	SessionIdentity string
	Principal       string
	ToolIdentity    string
	Event           EventType
	Decision        action.Decision
	Since           time.Time
}

func (f Filter) Validate() error {
	if f.CallID != "" && !validCallFilter(f.CallID) {
		return fmt.Errorf("call filter is invalid")
	}
	if f.RunIdentity != "" && !action.ValidKeyedIdentity(f.RunIdentity) {
		return fmt.Errorf("run filter must be one keyed ledger identity")
	}
	if f.SessionIdentity != "" && !action.ValidKeyedIdentity(f.SessionIdentity) {
		return fmt.Errorf("session filter must be one keyed ledger identity")
	}
	if f.Principal != "" && !action.SafeLabel(f.Principal) {
		return fmt.Errorf("principal filter is invalid")
	}
	if f.ToolIdentity != "" {
		if _, ok := parseToolIdentityFilter(f.ToolIdentity); !ok {
			return fmt.Errorf("tool filter must be one exact mode:value identity")
		}
	}
	if f.Event != "" && !f.Event.Valid() {
		return fmt.Errorf("event filter is invalid")
	}
	if f.Decision != "" && !f.Decision.Valid() {
		return fmt.Errorf("decision filter is invalid")
	}
	if !f.Since.IsZero() && !f.Since.After(time.Unix(0, 0)) {
		return fmt.Errorf("since filter is invalid")
	}
	return nil
}

func validCallFilter(value string) bool {
	if len(value) != 30 || !strings.HasPrefix(value, "act_") {
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

func (f Filter) matches(record Record) bool {
	if f.CallID != "" && record.Call.CallID != f.CallID ||
		f.RunIdentity != "" && record.Call.RunIdentity != f.RunIdentity ||
		f.SessionIdentity != "" && record.Call.SessionIdentity != f.SessionIdentity ||
		f.Principal != "" && record.Call.Principal != f.Principal ||
		f.ToolIdentity != "" && canonicalToolIdentity(record.Call.Tool) != f.ToolIdentity ||
		f.Event != "" && record.Event != f.Event ||
		f.Decision != "" && record.Decision.Decision != f.Decision {
		return false
	}
	if f.Since.IsZero() {
		return true
	}
	timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	return err == nil && !timestamp.Before(f.Since)
}

func (f Filter) Matches(record Record) bool {
	return f.matches(record)
}

type TailReport struct {
	FormatVersion string             `json:"format_version"`
	Verification  VerificationReport `json:"verification"`
	Records       []Record           `json:"records"`
}

func EmptyTailReport() TailReport {
	return TailReport{
		FormatVersion: TailReportFormat,
		Verification:  EmptyVerificationReport(),
		Records:       []Record{},
	}
}

func (s *Store) Tail(ctx context.Context, filter Filter, limit int) (TailReport, error) {
	report := EmptyTailReport()
	if err := filter.Validate(); err != nil {
		return report, err
	}
	if limit <= 0 || limit > MaxTailRecords {
		return report, fmt.Errorf("tail limit must be between 1 and %d", MaxTailRecords)
	}
	records, verification, err := s.Snapshot(ctx)
	report.Verification = verification
	if err != nil {
		return report, err
	}
	matched := make([]Record, 0, min(limit, len(records)))
	for index := len(records) - 1; index >= 0 && len(matched) < limit; index-- {
		if filter.matches(records[index]) {
			matched = append(matched, records[index])
		}
	}
	for left, right := 0, len(matched)-1; left < right; left, right = left+1, right-1 {
		matched[left], matched[right] = matched[right], matched[left]
	}
	report.Records = matched
	return report, nil
}

type LifecycleCounts struct {
	Calls                 uint64 `json:"calls"`
	Evaluated             uint64 `json:"evaluated"`
	Allowed               uint64 `json:"allowed"`
	Warned                uint64 `json:"warned"`
	ApprovalRequired      uint64 `json:"approval_required"`
	Blocked               uint64 `json:"blocked"`
	Approved              uint64 `json:"approved"`
	NotDispatched         uint64 `json:"not_dispatched"`
	Dispatched            uint64 `json:"dispatched"`
	DownstreamSucceeded   uint64 `json:"downstream_succeeded"`
	DownstreamFailed      uint64 `json:"downstream_failed"`
	DownstreamUnknown     uint64 `json:"downstream_unknown"`
	Delivered             uint64 `json:"delivered"`
	Withheld              uint64 `json:"withheld"`
	Suppressed            uint64 `json:"suppressed"`
	TerminalComplete      uint64 `json:"terminal_complete"`
	IncompleteTerminal    uint64 `json:"incomplete_terminal"`
	EvidenceComplete      uint64 `json:"evidence_complete"`
	EvidenceIncomplete    uint64 `json:"evidence_incomplete"`
	RetainedHistoryWhole  uint64 `json:"retained_history_whole"`
	RetainedHistoryPruned uint64 `json:"retained_history_pruned"`
}

func (c *LifecycleCounts) add(status CallStatus) {
	c.Calls++
	if status.Evaluated {
		c.Evaluated++
	}
	switch status.Decision {
	case action.DecisionAllow:
		c.Allowed++
	case action.DecisionWarn:
		c.Warned++
	case action.DecisionRequireApproval:
		c.ApprovalRequired++
	case action.DecisionBlock:
		c.Blocked++
	}
	if status.Approval == actionapproval.StatusApproved {
		c.Approved++
	}
	switch status.Dispatch {
	case DispatchNotDispatched:
		c.NotDispatched++
	case DispatchDispatched:
		c.Dispatched++
	case DispatchSucceeded:
		c.DownstreamSucceeded++
	case DispatchFailed:
		c.DownstreamFailed++
	case DispatchUnknown:
		c.DownstreamUnknown++
	}
	switch status.Delivery {
	case DeliveryForwarded:
		c.Delivered++
	case DeliveryWithheld:
		c.Withheld++
	case DeliverySuppressed:
		c.Suppressed++
	}
	if status.TerminalComplete {
		c.TerminalComplete++
	} else {
		c.IncompleteTerminal++
	}
	if status.EvidenceComplete {
		c.EvidenceComplete++
	} else {
		c.EvidenceIncomplete++
	}
	if status.HistoryComplete {
		c.RetainedHistoryWhole++
	} else {
		c.RetainedHistoryPruned++
	}
}

type LifecycleGroup struct {
	Identity string          `json:"identity"`
	Counts   LifecycleCounts `json:"counts"`
}

type StatsReport struct {
	FormatVersion string             `json:"format_version"`
	Verification  VerificationReport `json:"verification"`
	Counts        LifecycleCounts    `json:"counts"`
	Calls         []CallStatus       `json:"calls"`
	ByRun         []LifecycleGroup   `json:"by_run"`
	BySession     []LifecycleGroup   `json:"by_session"`
	ByPrincipal   []LifecycleGroup   `json:"by_principal"`
	ByTool        []LifecycleGroup   `json:"by_tool"`
}

func EmptyStatsReport() StatsReport {
	return StatsReport{
		FormatVersion: StatsReportFormat,
		Verification:  EmptyVerificationReport(),
		Calls:         []CallStatus{},
		ByRun:         []LifecycleGroup{},
		BySession:     []LifecycleGroup{},
		ByPrincipal:   []LifecycleGroup{},
		ByTool:        []LifecycleGroup{},
	}
}

func (s *Store) Stats(ctx context.Context, filter Filter) (StatsReport, error) {
	report := EmptyStatsReport()
	if err := filter.Validate(); err != nil {
		return report, err
	}
	records, verification, err := s.Snapshot(ctx)
	report.Verification = verification
	if err != nil {
		return report, err
	}
	statuses, err := BuildCallStatuses(records)
	if err != nil {
		return report, err
	}
	selected := selectedCallIDs(records, filter)
	for _, status := range statuses {
		if _, include := selected[status.CallID]; !include {
			continue
		}
		report.Calls = append(report.Calls, status)
		report.Counts.add(status)
	}
	report.ByRun = lifecycleGroups(report.Calls, func(status CallStatus) string { return status.RunIdentity })
	report.BySession = lifecycleGroups(report.Calls, func(status CallStatus) string { return status.SessionIdentity })
	report.ByPrincipal = lifecycleGroups(report.Calls, func(status CallStatus) string { return status.Principal })
	report.ByTool = lifecycleGroups(report.Calls, func(status CallStatus) string {
		return canonicalToolIdentity(status.Tool)
	})
	return report, nil
}

func parseToolIdentityFilter(value string) (ToolIdentity, bool) {
	separator := strings.IndexByte(value, ':')
	if separator <= 0 || separator == len(value)-1 {
		return ToolIdentity{}, false
	}
	identity := ToolIdentity{Mode: action.LedgerToolIdentity(value[:separator]), Value: value[separator+1:]}
	return identity, identity.validate() == nil
}

func canonicalToolIdentity(identity ToolIdentity) string {
	return string(identity.Mode) + ":" + identity.Value
}

func selectedCallIDs(records []Record, filter Filter) map[string]struct{} {
	selected := make(map[string]struct{})
	for _, record := range records {
		if filter.matches(record) {
			selected[record.Call.CallID] = struct{}{}
		}
	}
	return selected
}

func lifecycleGroups(statuses []CallStatus, identity func(CallStatus) string) []LifecycleGroup {
	counts := make(map[string]LifecycleCounts)
	for _, status := range statuses {
		key := identity(status)
		if key == "" {
			key = "absent"
		}
		value := counts[key]
		value.add(status)
		counts[key] = value
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := make([]LifecycleGroup, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, LifecycleGroup{Identity: key, Counts: counts[key]})
	}
	return groups
}
