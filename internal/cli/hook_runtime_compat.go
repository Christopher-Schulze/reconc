package cli

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/ingest"
)

var hookRuntimeSupportProbe = func() bool {
	return true
}

type hookRuntimeCheckResult struct {
	Status string
	Detail string
}

func inspectHookRuntimeCompatibility(discovery ingest.DiscoveryResult) hookRuntimeCheckResult {
	result := hookRuntimeCheckResult{
		Status: doctorStatusOK,
		Detail: "no agent hook configs installed",
	}
	if !discovery.Discovered {
		result.Status = doctorStatusWarn
		result.Detail = "cannot inspect hook configs without a discovered reconc repo"
		return result
	}

	reports, err := hooks.InspectPlatforms(discovery.RepoRoot)
	if err != nil {
		result.Status = doctorStatusWarn
		result.Detail = "cannot inspect hook platforms: " + err.Error()
		return result
	}
	parts := make([]string, 0, len(reports))
	runtimeKinds := make([]string, 0, len(reports))
	hasWarn := false
	for _, report := range reports {
		if report.State == hooks.StateAbsent {
			continue
		}
		if report.Kind != hooks.KindGitPreCommit {
			runtimeKinds = append(runtimeKinds, report.Kind)
		}
		parts = append(parts, fmt.Sprintf("%s: %s (%s)", report.Kind, report.State, report.Detail))
		if report.State != hooks.StateActive {
			hasWarn = true
		}
	}
	if len(parts) == 0 {
		return result
	}
	if len(runtimeKinds) > 0 && !hookRuntimeSupportProbe() {
		result.Status = doctorStatusWarn
		result.Detail = fmt.Sprintf("%s hook config(s) reference `reconc hook runtime`, but this binary is older than %s; upgrade reconc or reinstall hooks after upgrading",
			strings.Join(runtimeKinds, ", "), hooks.MinRuntimeSupportedVersion)
		return result
	}

	if hasWarn {
		result.Status = doctorStatusWarn
	}
	result.Detail = strings.Join(parts, "; ")
	return result
}
