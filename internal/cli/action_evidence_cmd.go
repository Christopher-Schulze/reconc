package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/actionevidence"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime"
)

type actionEvidenceOptions struct {
	repository          string
	asOf                time.Time
	since               time.Time
	until               time.Time
	corpusPaths         []string
	approvalAuthorities string
	packPaths           []string
	packDigests         []string
	packSignatures      []string
	packAuthorities     string
	format              string
	outputPath          string
	jsonOutput          bool
	asOfSeen            bool
	sinceSeen           bool
	untilSeen           bool
}

const maximumActionEvidenceFutureSkew = 5 * time.Minute

func runActionEvidence(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return actionLogCLIError("action evidence", "missing subcommand (export | verify)")
	}
	switch args[0] {
	case "export":
		return runActionEvidenceExport(args[1:], stdout)
	case "verify":
		return runActionEvidenceVerify(args[1:], stdout)
	default:
		return actionLogCLIError("action evidence", fmt.Sprintf("unknown subcommand %q (expected export or verify)", args[0]))
	}
}

func runActionEvidenceExport(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseActionEvidenceOptions("action evidence export", args, true)
	if err != nil {
		return err
	}
	report, reader, err := buildActionEvidence(options)
	if err != nil {
		return actionLogCLIError("action evidence export", err.Error())
	}
	defer joinActionLedgerCloseError(&resultErr, reader)
	body, err := renderActionEvidence(report, options.format)
	if err != nil {
		return actionLogCLIError("action evidence export", err.Error())
	}
	return writeActionLogOutput("action evidence export", stdout, options.outputPath, body)
}

func runActionEvidenceVerify(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseActionEvidenceOptions("action evidence verify", args, false)
	if err != nil {
		return err
	}
	report, reader, err := buildActionEvidence(options)
	if err != nil {
		return actionLogCLIError("action evidence verify", err.Error())
	}
	defer joinActionLedgerCloseError(&resultErr, reader)
	body := actionevidence.RenderVerificationText(report)
	if options.jsonOutput {
		body, err = actionevidence.MarshalJSON(report)
		if err != nil {
			return actionLogCLIError("action evidence verify", err.Error())
		}
	}
	if err := writeActionLogOutput("action evidence verify", stdout, "", body); err != nil {
		return err
	}
	if report.OverallStatus != actionevidence.StatusCovered {
		return actionLogCLIError("action evidence verify", "technical evidence status is "+string(report.OverallStatus))
	}
	return nil
}

func parseActionEvidenceOptions(command string, args []string, export bool) (actionEvidenceOptions, error) {
	options := actionEvidenceOptions{
		repository: ".", since: time.Unix(0, 0).UTC(), format: "json",
		corpusPaths: []string{}, packPaths: []string{}, packDigests: []string{}, packSignatures: []string{},
	}
	seen := make(map[string]bool)
	repositorySeen := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--json" && !export {
			if options.jsonOutput {
				return options, actionLogCLIError(command, "--json may be specified only once")
			}
			options.jsonOutput = true
			continue
		}
		if actionEvidenceRepeatFlag(argument) {
			value, ok := nextActionLogValue(args, &index, argument)
			if !ok || value == "" {
				return options, actionLogCLIError(command, argument+" requires one value")
			}
			appendActionEvidenceValue(&options, argument, value)
			continue
		}
		if actionEvidenceValueFlag(argument, export) {
			value, ok := nextActionLogValue(args, &index, argument)
			if !ok || value == "" || seen[argument] {
				return options, actionLogCLIError(command, argument+" requires one value")
			}
			seen[argument] = true
			if err := bindActionEvidenceValue(&options, argument, value); err != nil {
				return options, actionLogCLIError(command, err.Error())
			}
			continue
		}
		if strings.HasPrefix(argument, "-") {
			return options, actionLogCLIError(command, fmt.Sprintf("unknown flag %q", argument))
		}
		if repositorySeen {
			return options, actionLogCLIError(command, "expected at most one repository path")
		}
		options.repository, repositorySeen = argument, true
	}
	if err := validateActionEvidenceOptions(options, export); err != nil {
		return options, actionLogCLIError(command, err.Error())
	}
	return options, nil
}

func actionEvidenceRepeatFlag(value string) bool {
	return value == "--corpus" || value == "--map-pack" ||
		value == "--map-pack-digest" || value == "--map-pack-signature"
}

func appendActionEvidenceValue(options *actionEvidenceOptions, flag, value string) {
	switch flag {
	case "--corpus":
		options.corpusPaths = append(options.corpusPaths, value)
	case "--map-pack":
		options.packPaths = append(options.packPaths, value)
	case "--map-pack-digest":
		options.packDigests = append(options.packDigests, value)
	case "--map-pack-signature":
		options.packSignatures = append(options.packSignatures, value)
	}
}

func actionEvidenceValueFlag(value string, export bool) bool {
	switch value {
	case "--as-of", "--since", "--until", "--approval-authorities", "--map-pack-authorities":
		return true
	case "--format", "--output":
		return export
	default:
		return false
	}
}

func bindActionEvidenceValue(options *actionEvidenceOptions, flag, value string) error {
	switch flag {
	case "--as-of":
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || value != parsed.UTC().Format(time.RFC3339Nano) {
			return fmt.Errorf("--as-of must be a canonical UTC RFC3339 timestamp")
		}
		options.asOf, options.asOfSeen = parsed, true
	case "--since", "--until":
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || value != parsed.UTC().Format(time.RFC3339Nano) {
			return fmt.Errorf("%s must be a canonical UTC RFC3339 timestamp", flag)
		}
		if flag == "--since" {
			options.since, options.sinceSeen = parsed, true
		} else {
			options.until, options.untilSeen = parsed, true
		}
	case "--approval-authorities":
		options.approvalAuthorities = value
	case "--map-pack-authorities":
		options.packAuthorities = value
	case "--format":
		options.format = value
	case "--output":
		options.outputPath = value
	}
	return nil
}

func validateActionEvidenceOptions(options actionEvidenceOptions, export bool) error {
	if !options.asOfSeen {
		return fmt.Errorf("--as-of is required for deterministic evidence")
	}
	if options.asOf.After(time.Now().UTC().Add(maximumActionEvidenceFutureSkew)) {
		return fmt.Errorf("--as-of cannot be in the future")
	}
	if !options.untilSeen {
		options.until = options.asOf
	}
	if !options.since.Before(options.until) || options.until.After(options.asOf) {
		return fmt.Errorf("evidence window must satisfy since < until <= as-of")
	}
	if len(options.corpusPaths) > actionevidence.MaxPacks || len(options.packPaths) > actionevidence.MaxPacks {
		return fmt.Errorf("evidence input count exceeds %d", actionevidence.MaxPacks)
	}
	if export && options.format != "json" && options.format != "markdown" {
		return fmt.Errorf("--format must be json or markdown")
	}
	return validatePackAuthenticationOptions(options)
}

func validatePackAuthenticationOptions(options actionEvidenceOptions) error {
	if len(options.packPaths) == 0 {
		if len(options.packDigests) != 0 || len(options.packSignatures) != 0 || options.packAuthorities != "" {
			return fmt.Errorf("mapping-pack authentication requires --map-pack")
		}
		return nil
	}
	digestMode := len(options.packDigests) > 0
	signatureMode := len(options.packSignatures) > 0
	if digestMode == signatureMode {
		return fmt.Errorf("custom mapping packs require exactly one digest or signature mode")
	}
	if digestMode && (len(options.packDigests) != len(options.packPaths) || options.packAuthorities != "") {
		return fmt.Errorf("each custom mapping pack requires one digest and no authority registry")
	}
	if signatureMode && (len(options.packSignatures) != len(options.packPaths) || options.packAuthorities == "") {
		return fmt.Errorf("each signed mapping pack requires one signature and a shared authority registry")
	}
	return nil
}

func buildActionEvidence(options actionEvidenceOptions) (actionevidence.Report, existingActionLedger, error) {
	ctx, cancel := context.WithTimeout(context.Background(), actionLogCommandTimeout)
	defer cancel()
	reader, err := openExistingActionLedger(ctx, options.repository)
	if err != nil {
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	compiled, runtimeSnapshot, err := currentActionEvidenceRuntime(reader.repository)
	if err != nil {
		_ = reader.close()
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	registry, err := loadEvidenceApprovalRegistry(options, reader.repository)
	if err != nil {
		_ = reader.close()
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	records, ledgerReport := snapshotEvidenceLedger(ctx, reader.store)
	stateMaterialPresent := reader.stateStorage != nil && existingActionStateMaterial(reader.repository)
	state, receipts, stateIntegrity, statePresent := snapshotEvidenceState(
		ctx,
		reader,
		registry,
		stateMaterialPresent,
	)
	scenarios, err := loadEvidenceScenarios(options.corpusPaths, reader.repository, compiled, runtimeSnapshot)
	if err != nil {
		_ = reader.close()
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	packs, err := loadEvidencePacks(options)
	if err != nil {
		_ = reader.close()
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	report, err := actionevidence.Build(actionevidence.BuildInput{
		AsOf: options.asOf, Since: options.since, Until: evidenceUntil(options),
		RepositoryIdentity: evidenceRepositoryIdentity(reader),
		Policy: actionevidence.PolicyEvidence{
			SourceDigest: runtimeSnapshot.SourceDigest, LockDigest: runtimeSnapshot.LockDigest,
			PlanIdentity: runtimeSnapshot.Evaluator.PlanIdentity(), ToolCount: runtimeSnapshot.ToolCount,
			RuleCount: runtimeSnapshot.ActionRuleCount, BudgetCount: len(runtimeSnapshot.Plan.Plan().Budgets),
			ApprovalCount: len(runtimeSnapshot.Plan.Plan().Approvals),
		},
		Plan: runtimeSnapshot.Plan.Plan(), Records: records, LedgerVerification: ledgerReport,
		StateIntegrity: stateIntegrity, StatePresent: statePresent, State: state,
		Receipts: receipts, Scenarios: scenarios, Packs: packs,
	})
	if err != nil {
		_ = reader.close()
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	if err := verifyEvidenceSourcesStable(
		ctx,
		reader,
		registry,
		runtimeSnapshot,
		records,
		ledgerReport,
		stateMaterialPresent,
		state,
	); err != nil {
		_ = reader.close()
		return actionevidence.Report{}, existingActionLedger{}, err
	}
	return report, reader, nil
}

func currentActionEvidenceRuntime(repository string) (*runtime.CompiledPolicyEvaluator, runtime.CompiledActionRuntime, error) {
	compiled, _, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repository)
	if err != nil {
		return nil, runtime.CompiledActionRuntime{}, fmt.Errorf("prepare current policy: %w", err)
	}
	actionRuntime, err := compiled.ActionRuntime()
	if err != nil {
		return nil, runtime.CompiledActionRuntime{}, fmt.Errorf("prepare current actions: %w", err)
	}
	return compiled, actionRuntime, nil
}

func loadEvidenceApprovalRegistry(options actionEvidenceOptions, repository string) (actionstate.LoadedApprovalRegistry, error) {
	if options.approvalAuthorities == "" {
		return actionstate.LoadedApprovalRegistry{}, nil
	}
	registry, err := actionstate.LoadApprovalAuthorityRegistry(options.approvalAuthorities, repository)
	if err != nil {
		return actionstate.LoadedApprovalRegistry{}, fmt.Errorf("load approval authority registry: %w", err)
	}
	return registry, nil
}

func snapshotEvidenceLedger(ctx context.Context, store *actionledger.Store) ([]actionledger.Record, actionledger.VerificationReport) {
	if store == nil {
		return []actionledger.Record{}, actionledger.EmptyVerificationReport()
	}
	records, report, err := store.Snapshot(ctx)
	if err != nil {
		return []actionledger.Record{}, report
	}
	return records, report
}

func snapshotEvidenceState(
	ctx context.Context,
	reader existingActionLedger,
	registry actionstate.LoadedApprovalRegistry,
	materialPresent bool,
) (actionstate.StateStatus, actionstate.ApprovalReceiptVerificationReport, actionevidence.IntegrityStatus, bool) {
	if reader.stateStorage == nil || !materialPresent {
		return actionstate.StateStatus{}, actionstate.ApprovalReceiptVerificationReport{
			Evaluated: true, Complete: true, Records: []actionstate.ApprovalReceiptVerification{},
		}, actionevidence.IntegrityUnavailable, false
	}
	status, receipts, present, err := actionstate.ReadExistingEvidence(ctx, *reader.stateStorage, registry)
	if err != nil {
		return actionstate.StateStatus{}, actionstate.ApprovalReceiptVerificationReport{
			Evaluated: true, Complete: false, Records: []actionstate.ApprovalReceiptVerification{},
		}, actionevidence.IntegrityInvalid, false
	}
	return status, receipts, actionevidence.IntegrityVerified, present
}

func existingActionStateMaterial(repository string) bool {
	home, err := actionstate.ResolveHome("")
	if err != nil {
		return false
	}
	directory := filepath.Join(retention.ProjectDir(home, repository), "action")
	for _, name := range []string{"state.json", "state-transaction.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			return true
		}
	}
	return false
}

func loadEvidenceScenarios(
	paths []string,
	repository string,
	compiled *runtime.CompiledPolicyEvaluator,
	actionRuntime runtime.CompiledActionRuntime,
) (actionevidence.ScenarioEvidence, error) {
	corpora := make([]impactlab.Corpus, len(paths))
	for index, path := range paths {
		corpus, err := impactlab.DecodeCorpusFile(path)
		if err != nil {
			return actionevidence.ScenarioEvidence{}, fmt.Errorf("load scenario corpus %d: %w", index+1, err)
		}
		corpora[index] = corpus
	}
	return actionevidence.EvaluateScenarios(repository, corpora, compiled, actionRuntime)
}

func loadEvidencePacks(options actionEvidenceOptions) ([]actionevidence.LoadedPack, error) {
	packs, err := actionevidence.BuiltinPacks()
	if err != nil {
		return nil, fmt.Errorf("prepare built-in mapping packs: %w", err)
	}
	for index, path := range options.packPaths {
		authentication := actionevidence.PackAuthentication{RegistryPath: options.packAuthorities}
		if len(options.packDigests) > 0 {
			authentication.ExpectedDigest = options.packDigests[index]
		} else {
			authentication.SignaturePath = options.packSignatures[index]
		}
		loaded, err := actionevidence.LoadPack(path, authentication)
		if err != nil {
			return nil, fmt.Errorf("load custom mapping pack %d: %w", index+1, err)
		}
		packs = append(packs, loaded)
	}
	return packs, nil
}

func verifyEvidenceSourcesStable(
	ctx context.Context,
	reader existingActionLedger,
	registry actionstate.LoadedApprovalRegistry,
	initial runtime.CompiledActionRuntime,
	records []actionledger.Record,
	ledgerReport actionledger.VerificationReport,
	stateMaterialPresent bool,
	state actionstate.StateStatus,
) error {
	_, current, err := currentActionEvidenceRuntime(reader.repository)
	if err != nil || current.SourceDigest != initial.SourceDigest || current.LockDigest != initial.LockDigest ||
		current.Evaluator.PlanIdentity() != initial.Evaluator.PlanIdentity() {
		return fmt.Errorf("policy changed while evidence was being built")
	}
	secondRecords, report := snapshotEvidenceLedger(ctx, reader.store)
	if !sameEvidenceLedgerSnapshot(records, ledgerReport, secondRecords, report) {
		return fmt.Errorf("action ledger changed while evidence was being built")
	}
	currentStateMaterial := reader.stateStorage != nil && existingActionStateMaterial(reader.repository)
	if currentStateMaterial != stateMaterialPresent {
		return fmt.Errorf("action state changed while evidence was being built")
	}
	if stateMaterialPresent {
		second, _, present, snapshotErr := actionstate.ReadExistingEvidence(ctx, *reader.stateStorage, registry)
		if state.StateVersion == "" && (snapshotErr != nil || !present) {
			return nil
		}
		if snapshotErr != nil || !present || second.StateVersion != state.StateVersion {
			return fmt.Errorf("action state changed while evidence was being built")
		}
	}
	return nil
}

func sameEvidenceLedgerSnapshot(
	left []actionledger.Record,
	leftReport actionledger.VerificationReport,
	right []actionledger.Record,
	rightReport actionledger.VerificationReport,
) bool {
	return leftReport == rightReport && sameLedgerSnapshot(left, right)
}

func sameLedgerSnapshot(left, right []actionledger.Record) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	return left[0].Sequence == right[0].Sequence && left[0].Digest == right[0].Digest &&
		left[len(left)-1].Sequence == right[len(right)-1].Sequence &&
		left[len(left)-1].Digest == right[len(right)-1].Digest
}

func evidenceRepositoryIdentity(reader existingActionLedger) string {
	if reader.repositoryIdentity == "" {
		return "unavailable"
	}
	return reader.repositoryIdentity
}

func evidenceUntil(options actionEvidenceOptions) time.Time {
	if options.untilSeen {
		return options.until
	}
	return options.asOf
}

func renderActionEvidence(report actionevidence.Report, format string) ([]byte, error) {
	if format == "markdown" {
		return actionevidence.MarshalMarkdown(report)
	}
	return actionevidence.MarshalJSON(report)
}

func (reader existingActionLedger) close() error {
	if reader.lease == nil {
		return nil
	}
	return reader.lease.Close()
}
