package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/hooks"
	reconruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/tasklifecycle"
)

func Verify(plan *Plan) (*Verification, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	verification := &Verification{
		FormatVersion: VerifyFormatVersion, RepoRoot: plan.RepoRoot,
		PlanDigest: plan.PlanDigest, Valid: true, Checks: []Check{},
	}
	for _, issue := range plan.BlockingIssues {
		verification.add("plan", false, issue)
	}
	for _, action := range plan.Actions {
		path := action.Path
		expected := action.DesiredSHA256
		if action.State == ActionConflict {
			path = action.CandidatePath
			verification.add("target:"+action.Path, false, "existing target drift remains; review "+action.CandidatePath)
		}
		target := filepath.Join(plan.RepoRoot, filepath.FromSlash(path))
		info, err := os.Lstat(target)
		if err != nil {
			verification.add("artifact:"+path, false, "artifact missing or unreadable: "+err.Error())
			continue
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			verification.add("artifact:"+path, false, "artifact is not a real regular file")
			continue
		}
		if !modeSatisfies(info.Mode(), action.Mode) {
			verification.add("artifact:"+path, false, fmt.Sprintf("mode %04o does not satisfy %04o", info.Mode().Perm(), action.Mode))
			continue
		}
		digest, err := fileSHA256(target)
		if err != nil {
			verification.add("artifact:"+path, false, err.Error())
			continue
		}
		if digest != expected {
			verification.add("artifact:"+path, false, "checksum drift: expected "+expected+", got "+digest)
			continue
		}
		verification.add("artifact:"+path, true, "sha256 and mode verified")
	}
	if err := reconruntime.ValidatePolicyLockfile(plan.RepoRoot); err != nil {
		verification.add("policy-lock", false, err.Error())
	} else {
		verification.add("policy-lock", true, "compiled lockfile matches current policy sources")
	}
	profile, err := profileByName(plan.Selection.Profile)
	if err != nil {
		return nil, err
	}
	if profile.Tasks {
		board, err := tasklifecycle.Load(plan.RepoRoot)
		if err != nil {
			verification.add("task-lifecycle", false, err.Error())
		} else if board.Active == nil {
			verification.add("task-lifecycle", false, "TASK control plane has no active task")
		} else {
			verification.add("task-lifecycle", true, "active task "+board.Active.ID+" is structurally valid")
		}
	}
	if len(plan.Selection.Hooks) > 0 {
		statuses, err := hooks.InspectPlatforms(plan.RepoRoot)
		if err != nil {
			verification.add("hooks", false, err.Error())
		} else {
			byKind := map[string]hooks.PlatformStatus{}
			for _, status := range statuses {
				byKind[status.Kind] = status
			}
			for _, kind := range plan.Selection.Hooks {
				status, ok := byKind[kind]
				if !ok {
					verification.add("hook:"+kind, false, "selected hook has no registry status")
					continue
				}
				verification.add("hook:"+kind, status.State == hooks.StateConfigured, string(status.State)+": "+status.Detail)
			}
		}
	}
	if plan.Selection.Binary != nil {
		selection := plan.Selection.Binary
		resolution := ResolveRepoBinary(plan.RepoRoot, selection.OS, selection.Arch)
		if resolution.Path == "" {
			verification.add("binary-resolution", false, resolution.Diagnostic)
		} else {
			digest, err := fileSHA256(resolution.Path)
			if err != nil {
				verification.add("binary-resolution", false, err.Error())
			} else if digest != selection.SHA256 {
				verification.add("binary-resolution", false, "resolved binary checksum differs from the plan")
			} else {
				verification.add("binary-resolution", true, resolution.Source+": "+resolution.Path)
			}
		}
	}
	sort.SliceStable(verification.Checks, func(i, j int) bool { return verification.Checks[i].Name < verification.Checks[j].Name })
	if verification.Valid {
		verification.NextAction = "Bootstrap plan is fully installed and verified."
	} else {
		for _, check := range verification.Checks {
			if check.Status == "FAIL" {
				verification.NextAction = check.Detail
				break
			}
		}
	}
	return verification, nil
}

func (verification *Verification) add(name string, pass bool, detail string) {
	status := "PASS"
	if !pass {
		status = "FAIL"
		verification.Valid = false
	}
	verification.Checks = append(verification.Checks, Check{Name: name, Status: status, Detail: strings.TrimSpace(detail)})
}

func modeSatisfies(actual os.FileMode, desired uint32) bool {
	if runtime.GOOS == "windows" {
		actualWritable := actual.Perm()&0o200 != 0
		desiredWritable := os.FileMode(desired).Perm()&0o200 != 0
		return actualWritable == desiredWritable
	}
	return uint32(actual.Perm()) == desired
}
