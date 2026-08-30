package bootstrap

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/hooks"
	reconruntime "reconc.dev/reconc/internal/runtime"
)

type recordedInitState string

const (
	recordedInitAbsent  recordedInitState = "absent"
	recordedInitValid   recordedInitState = "valid"
	recordedInitInvalid recordedInitState = "invalid"
)

type recordedInitInspection struct {
	State recordedInitState
	Plan  *Plan
	Path  string
}

type recordedInitPlanLoader func(string) (*Plan, error)

var beforeInitVerification = func(*Plan) error { return nil }

// Initialize executes the canonical inspect, select, plan, apply, and verify
// transaction. It never prompts and never guesses a profile for a repository
// that already has unreceipted control artifacts.
func Initialize(request InitRequest, productVersion string) (*InitReport, error) {
	report := &InitReport{
		FormatVersion: InitFormatVersion, Operation: "init", Status: InitRefused,
		CurrentVersion: productVersion, PolicyPacks: []string{},
		HarnessPacks: []HarnessPackSelection{}, Hooks: []string{},
		Checks: []Check{}, Actions: []Action{}, Candidates: []string{},
		Warnings: append([]string{}, request.CompatibilityWarning...),
	}
	root, err := canonicalRepoRoot(request.RepoRoot)
	if err != nil {
		report.NextAction = err.Error()
		return report, err
	}
	report.RepoRoot = root
	err = withRepositoryTransactionLock(root, func() error {
		return initializeLocked(request, report, productVersion)
	})
	if err != nil && report.NextAction == "" {
		report.NextAction = err.Error()
	}
	return report, err
}

func initializeLocked(request InitRequest, result *InitReport, productVersion string) error {
	if err := ensureNoPendingRepositorySync(result.RepoRoot); err != nil {
		result.Status = InitRefused
		result.NextAction = err.Error()
		return err
	}
	inspection, err := Inspect(result.RepoRoot)
	if err != nil {
		return fmt.Errorf("inspect repository: %w", err)
	}
	recorded, recordedErr := inspectRecordedInitPlan(result.RepoRoot)
	if recordedErr != nil {
		result.Status = InitRefused
		result.Profile = suggestedExplicitProfile(inspection)
		result.NextAction = recordedInitRemediation(result.RepoRoot, recorded.Path)
		return recordedErr
	}
	recordedPlan := recorded.Plan
	profile := request.Profile
	packs := append([]string{}, request.Packs...)
	hookKinds := append([]string{}, request.Hooks...)
	if profile == "" && recordedPlan != nil {
		profile = recordedPlan.Selection.Profile
		if len(packs) == 0 {
			packs = append([]string{}, recordedPlan.Selection.Packs...)
		}
		if !request.HooksExplicit && !request.NoHooks {
			hookKinds = append([]string{}, recordedPlan.Selection.Hooks...)
		}
	}
	if profile == "" && hasUnreceiptedControlState(inspection) {
		result.Status = InitRefused
		result.Profile = suggestedExplicitProfile(inspection)
		result.NextAction = renderInitCommand(result.RepoRoot, result.Profile, packs, nil, request.NoHooks)
		return fmt.Errorf("repository has existing control artifacts without one valid Reconc transaction receipt; explicit --profile is required")
	}
	if profile == "" {
		profile = ProfileMinimal
	}
	if request.NoHooks && request.HooksExplicit {
		result.Status = InitRefused
		result.Profile = profile
		result.NextAction = renderInitCommand(result.RepoRoot, profile, packs, nil, true)
		return fmt.Errorf("--hook and --no-hooks are mutually exclusive")
	}
	if request.NoHooks {
		hookKinds = []string{}
	} else if !request.HooksExplicit && recordedPlan == nil {
		hookKinds, err = detectedInitHooks(inspection, request.SkipGitHook, request.SkipAgentHooks)
		if err != nil {
			result.Status = InitRefused
			result.Profile = profile
			return err
		}
	}
	plan, err := BuildPlan(Request{
		RepoRoot: result.RepoRoot, Profile: profile, Packs: packs, Hooks: hookKinds,
		TrustExistingWrapper: profile == ProfileMinimal,
	}, productVersion)
	if err != nil {
		result.Status = InitRefused
		result.Profile = profile
		result.NextAction = renderInitCommand(result.RepoRoot, profile, packs, hookKinds, request.NoHooks)
		return fmt.Errorf("build init plan: %w", err)
	}
	result.Profile = plan.Selection.Profile
	result.PolicyPacks = append([]string{}, plan.Selection.Packs...)
	result.HarnessPacks = append([]HarnessPackSelection{}, plan.Selection.HarnessPacks...)
	result.Hooks = append([]string{}, plan.Selection.Hooks...)
	result.Actions = append([]Action{}, plan.Actions...)
	result.PlanDigest = stringPointer(plan.PlanDigest)
	if len(plan.BlockingIssues) > 0 {
		result.Status = InitRefused
		result.NextAction = renderInitCommand(result.RepoRoot, profile, packs, hookKinds, request.NoHooks)
		return fmt.Errorf("init plan has blocking issue: %s", plan.BlockingIssues[0])
	}

	applyReport, err := apply(plan, productVersion, applyOptions{})
	if err != nil {
		result.Status, result.Changed = classifyInitApplyFailure(applyReport, err)
		result.Candidates = append([]string{}, applyReport.Candidates...)
		result.NextAction = err.Error()
		return fmt.Errorf("apply init plan: %w", err)
	}
	result.Warnings = append(result.Warnings, applyReport.Summary.InspectionErrors...)
	result.Changed = len(applyReport.Created) > 0
	result.Candidates = append([]string{}, applyReport.Candidates...)
	if applyReport.ReceiptPath != "" {
		result.ReceiptPath = stringPointer(applyReport.ReceiptPath)
		result.PlanPath = stringPointer(recordedPlanPath(plan))
	}
	if request.AcceptManagedBlocks && applyReport.Status == ApplyDrift {
		accepted, acceptErr := acceptManagedCandidatesLocked(plan, applyReport)
		if acceptErr != nil {
			result.Status = InitDrift
			result.NextAction = initDriftNext(plan, applyReport)
			return fmt.Errorf("accept managed init candidates: %w", acceptErr)
		}
		result.Changed = result.Changed || len(accepted.Updated) > 0
		acceptedPlan := plan
		plan, err = BuildPlan(Request{
			RepoRoot: result.RepoRoot, Profile: profile, Packs: packs, Hooks: hookKinds,
			TrustExistingWrapper: profile == ProfileMinimal,
		}, productVersion)
		if err != nil {
			result.Status = InitDrift
			result.NextAction = initVerificationDriftNext(acceptedPlan, err.Error())
			return fmt.Errorf("rebuild accepted init plan: %w", err)
		}
		applyReport, err = apply(plan, productVersion, applyOptions{})
		if err != nil {
			result.Status = InitDrift
			result.NextAction = initVerificationDriftNext(plan, err.Error())
			return fmt.Errorf("apply accepted init plan: %w", err)
		}
		result.Warnings = append(result.Warnings, applyReport.Summary.InspectionErrors...)
		result.Actions = append([]Action{}, plan.Actions...)
		result.PlanDigest = stringPointer(plan.PlanDigest)
		result.Candidates = append([]string{}, applyReport.Candidates...)
		if applyReport.ReceiptPath != "" {
			result.ReceiptPath = stringPointer(applyReport.ReceiptPath)
			result.PlanPath = stringPointer(recordedPlanPath(plan))
		}
	}
	if applyReport.Status != ApplyComplete {
		result.Status = InitDrift
		result.NextAction = initDriftNext(plan, applyReport)
		return nil
	}
	if err := beforeInitVerification(plan); err != nil {
		result.Status = InitDrift
		result.NextAction = initVerificationDriftNext(plan, err.Error())
		return fmt.Errorf("prepare init verification: %w", err)
	}
	verification, err := Verify(plan)
	if err != nil {
		result.Status = InitDrift
		result.NextAction = initVerificationDriftNext(plan, err.Error())
		return fmt.Errorf("verify init transaction: %w", err)
	}
	result.Checks = append([]Check{}, verification.Checks...)
	if !verification.Valid {
		result.Status = InitDrift
		result.NextAction = initVerificationDriftNext(plan, verification.NextAction)
		return fmt.Errorf("init verification failed: %s", verification.NextAction)
	}
	result.Status = InitComplete
	result.NextAction = "reconc check " + quoteBootstrapArgument(result.RepoRoot)
	if result.ReceiptPath == nil && recordedPlan != nil {
		if _, receiptPath, receiptErr := loadInstallReceipt(recordedPlan); receiptErr == nil {
			result.ReceiptPath = stringPointer(receiptPath)
			result.PlanPath = stringPointer(recordedPlanPath(recordedPlan))
		}
	}
	return nil
}

func inspectRecordedInitPlan(root string) (recordedInitInspection, error) {
	return inspectRecordedInitPlanWith(root, LoadPlan)
}

func inspectRecordedInitPlanWith(root string, load recordedInitPlanLoader) (recordedInitInspection, error) {
	inspection := recordedInitInspection{State: recordedInitAbsent}
	canonicalRoot, err := canonicalRepoRoot(root)
	if err != nil {
		inspection.State = recordedInitInvalid
		return inspection, fmt.Errorf("resolve recorded init repository: %w", err)
	}
	root = canonicalRoot
	pattern := filepath.Join(root, ".reconc", "bootstrap-plan-*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		inspection.State = recordedInitInvalid
		return inspection, fmt.Errorf("inspect recorded init plans: %w", err)
	}
	if len(paths) == 0 {
		return inspection, nil
	}
	if len(paths) > 1 {
		inspection.State = recordedInitInvalid
		return inspection, fmt.Errorf("repository has %d recorded init plan candidates; refusing ambiguous state", len(paths))
	}
	path := paths[0]
	inspection.Path = filepath.ToSlash(path)
	plan, err := load(path)
	if err != nil {
		inspection.State = recordedInitInvalid
		return inspection, fmt.Errorf("recorded init plan %s is unreadable or invalid: %w", path, err)
	}
	if plan.RepoRoot != root {
		inspection.State = recordedInitInvalid
		return inspection, fmt.Errorf("recorded init plan %s belongs to %s, not %s", path, plan.RepoRoot, root)
	}
	if _, _, err := loadInstallReceipt(plan); err != nil {
		inspection.State = recordedInitInvalid
		return inspection, fmt.Errorf("recorded init plan %s has no valid matching receipt: %w", path, err)
	}
	inspection.State = recordedInitValid
	inspection.Plan = plan
	return inspection, nil
}

func recordedInitRemediation(root, path string) string {
	target := path
	if target == "" {
		target = filepath.Join(root, ".reconc")
	}
	return "inspect and repair or remove the invalid recorded init state at " + target + ", then rerun reconc init"
}

func initVerificationDriftNext(plan *Plan, detail string) string {
	prefix := strings.TrimSpace(detail)
	if prefix != "" {
		prefix += "; "
	}
	return prefix + "resolve the installed artifact drift, then rerun " +
		renderInitCommand(plan.RepoRoot, plan.Selection.Profile, plan.Selection.Packs, plan.Selection.Hooks, false)
}

func classifyInitApplyFailure(report *Report, err error) (InitStatus, bool) {
	var rollbackFailure *applyRollbackFailure
	if errors.As(err, &rollbackFailure) {
		return InitDrift, true
	}
	if report != nil && len(report.RolledBack) > 0 {
		return InitRolledBack, false
	}
	return InitRefused, false
}

func hasUnreceiptedControlState(inspection *Inspection) bool {
	return inspection != nil && len(inspection.ExistingPaths) > 0
}

func suggestedExplicitProfile(inspection *Inspection) ProfileName {
	if inspection == nil {
		return ProfileMinimal
	}
	has := map[string]bool{}
	for _, path := range inspection.ExistingPaths {
		has[path] = true
	}
	if has[".reconc.yml"] && has[policyLockfilePath] {
		if err := reconruntime.ValidatePolicyLockfile(inspection.RepoRoot); err == nil {
			return ProfileExisting
		}
	}
	if has[".reconc.yml"] && (has["docs/tasks.md"] || has["docs/documentation.md"] || has["start.md"]) {
		return ProfileGoverned
	}
	return ProfileMinimal
}

func detectedInitHooks(inspection *Inspection, skipGit, skipAgents bool) ([]string, error) {
	kinds := []string{}
	if !skipGit {
		present, err := inspectRepositoryGitMetadata(inspection.RepoRoot)
		if err != nil {
			return nil, err
		}
		if present {
			kinds = append(kinds, hooks.KindGitPreCommit)
		}
	}
	if !skipAgents {
		agents := map[string]bool{}
		for _, platform := range hooks.RepositoryAgentPlatforms() {
			agents[platform.Kind] = true
		}
		for _, kind := range inspection.DetectedPlatforms {
			if agents[kind] {
				kinds = append(kinds, kind)
			}
		}
	}
	kinds = dedupePreservingOrder(kinds)
	sort.Strings(kinds)
	return kinds, nil
}

func initDriftNext(plan *Plan, report *Report) string {
	if HasManagedCandidates(plan) {
		return renderInitCommand(plan.RepoRoot, plan.Selection.Profile, plan.Selection.Packs, plan.Selection.Hooks, false) + " --accept-managed-blocks"
	}
	if len(report.Candidates) > 0 {
		return "review " + report.Candidates[0] + ", integrate or reject it, then rerun " +
			renderInitCommand(plan.RepoRoot, plan.Selection.Profile, plan.Selection.Packs, plan.Selection.Hooks, false)
	}
	return report.NextAction
}

func renderInitCommand(root string, profile ProfileName, packs, hookKinds []string, noHooks bool) string {
	args := []string{"init", root, "--profile", string(profile)}
	for _, pack := range packs {
		if pack != "default" && pack != "agent" {
			args = append(args, "--pack", pack)
		}
	}
	if noHooks {
		args = append(args, "--no-hooks")
	} else {
		for _, kind := range hookKinds {
			args = append(args, "--hook", kind)
		}
	}
	return renderBootstrapCommand("reconc", args...)
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}
