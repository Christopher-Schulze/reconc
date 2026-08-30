package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionledgerexport"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

const actionLogCommandTimeout = 10 * time.Second

type actionLogOptions struct {
	repository string
	filter     actionledger.Filter
	limit      int
	jsonOutput bool
	outputPath string
}

type actionLogParseMode struct {
	filters bool
	limit   bool
	output  bool
	json    bool
}

type existingActionLedger struct {
	store              *actionledger.Store
	stateStorage       *actionstate.PrivateProjectStorage
	lease              *actionstate.IdentityKeyLease
	repository         string
	repositoryIdentity string
}

func runAction(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return actionLogCLIError("action", "missing subcommand (evidence | key | log)")
	}
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Fprintln(stdout, "Usage: reconc action <evidence|key|log>")
		fmt.Fprintln(stdout, "Initialize action state, inspect its ledger, or produce local technical control evidence.")
		return nil
	}
	switch args[0] {
	case "evidence":
		return runActionEvidence(args[1:], stdout)
	case "key":
		return runActionKey(args[1:], stdout)
	case "log":
		return runActionLog(args[1:], stdout)
	default:
		return actionLogCLIError("action", fmt.Sprintf("unknown subcommand %q (expected evidence, key, or log)", args[0]))
	}
}

func runActionLog(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return actionLogCLIError("action log", "missing subcommand (tail | stats | verify | export)")
	}
	switch args[0] {
	case "tail":
		return runActionLogTail(args[1:], stdout)
	case "stats":
		return runActionLogStats(args[1:], stdout)
	case "verify":
		return runActionLogVerify(args[1:], stdout)
	case "export":
		return runActionLogExport(args[1:], stdout)
	default:
		return actionLogCLIError("action log", fmt.Sprintf(
			"unknown subcommand %q (expected tail, stats, verify, or export)", args[0],
		))
	}
}

func runActionLogTail(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseActionLogOptions("action log tail", args, actionLogParseMode{filters: true, limit: true, json: true})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionLogCommandTimeout)
	defer cancel()
	reader, err := openExistingActionLedger(ctx, options.repository)
	if err != nil {
		return actionLogCLIError("action log tail", err.Error())
	}
	defer joinActionLedgerCloseError(&resultErr, reader)
	report := actionledger.EmptyTailReport()
	if reader.store != nil {
		report, err = reader.store.Tail(ctx, options.filter, options.limit)
		if err != nil {
			return actionLogCLIError("action log tail", err.Error())
		}
	}
	if options.jsonOutput {
		body, encodeErr := actionledger.MarshalTail(report)
		if encodeErr != nil {
			return actionLogCLIError("action log tail", encodeErr.Error())
		}
		return writeActionLogOutput("action log tail", stdout, "", body)
	}
	return writeActionLogOutput("action log tail", stdout, "", actionledger.RenderTailText(report))
}

func runActionLogStats(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseActionLogOptions("action log stats", args, actionLogParseMode{filters: true, json: true})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionLogCommandTimeout)
	defer cancel()
	reader, err := openExistingActionLedger(ctx, options.repository)
	if err != nil {
		return actionLogCLIError("action log stats", err.Error())
	}
	defer joinActionLedgerCloseError(&resultErr, reader)
	report := actionledger.EmptyStatsReport()
	if reader.store != nil {
		report, err = reader.store.Stats(ctx, options.filter)
		if err != nil {
			return actionLogCLIError("action log stats", err.Error())
		}
	}
	if options.jsonOutput {
		body, encodeErr := actionledger.MarshalStats(report)
		if encodeErr != nil {
			return actionLogCLIError("action log stats", encodeErr.Error())
		}
		return writeActionLogOutput("action log stats", stdout, "", body)
	}
	return writeActionLogOutput("action log stats", stdout, "", actionledger.RenderStatsText(report))
}

func runActionLogVerify(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseActionLogOptions("action log verify", args, actionLogParseMode{json: true})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionLogCommandTimeout)
	defer cancel()
	reader, err := openExistingActionLedger(ctx, options.repository)
	if err != nil {
		return actionLogCLIError("action log verify", err.Error())
	}
	defer joinActionLedgerCloseError(&resultErr, reader)
	report := actionledger.EmptyVerificationReport()
	var verificationErr error
	if reader.store != nil {
		report, verificationErr = reader.store.Verify(ctx)
	}
	var body []byte
	if options.jsonOutput {
		body, err = actionledger.MarshalVerification(report)
	} else {
		body = actionledger.RenderVerificationText(report)
	}
	if err != nil {
		return actionLogCLIError("action log verify", err.Error())
	}
	if err := writeActionLogOutput("action log verify", stdout, "", body); err != nil {
		return err
	}
	if verificationErr != nil {
		return actionLogCLIError("action log verify", verificationErr.Error())
	}
	return nil
}

func runActionLogExport(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseActionLogOptions("action log export", args, actionLogParseMode{filters: true, output: true})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionLogCommandTimeout)
	defer cancel()
	reader, err := openExistingActionLedger(ctx, options.repository)
	if err != nil {
		return actionLogCLIError("action log export", err.Error())
	}
	defer joinActionLedgerCloseError(&resultErr, reader)
	report := actionledgerexport.EmptyReport()
	if reader.store != nil {
		compiledPolicy, _, compileErr := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(reader.repository)
		if compileErr != nil {
			return actionLogCLIError("action log export", "prepare current policy: "+compileErr.Error())
		}
		compiledAction, compileErr := compiledPolicy.ActionRuntime()
		if compileErr != nil {
			return actionLogCLIError("action log export", "prepare current actions: "+compileErr.Error())
		}
		report, err = actionledgerexport.Build(ctx, reader.store, reader.repository, options.filter, compiledAction)
		if err != nil {
			return actionLogCLIError("action log export", err.Error())
		}
	}
	body, err := actionledgerexport.Marshal(report)
	if err != nil {
		return actionLogCLIError("action log export", err.Error())
	}
	return writeActionLogOutput("action log export", stdout, options.outputPath, body)
}

func parseActionLogOptions(
	command string,
	args []string,
	mode actionLogParseMode,
) (actionLogOptions, error) {
	options := actionLogOptions{repository: ".", limit: actionledger.DefaultTailRecords}
	seen := make(map[string]bool)
	repositorySeen := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--json" && mode.json {
			if seen[argument] {
				return options, actionLogCLIError(command, "--json may be specified only once")
			}
			seen[argument], options.jsonOutput = true, true
			continue
		}
		if argument == "-n" && mode.limit {
			value, ok := nextActionLogValue(args, &index, argument)
			if !ok || seen[argument] {
				return options, actionLogCLIError(command, "-n requires one integer")
			}
			seen[argument] = true
			limit, parseErr := atoi(value)
			if parseErr != nil || limit < 1 || limit > actionledger.MaxTailRecords {
				return options, actionLogCLIError(command, fmt.Sprintf("-n must be between 1 and %d", actionledger.MaxTailRecords))
			}
			options.limit = limit
			continue
		}
		if argument == "--output" && mode.output {
			value, ok := nextActionLogValue(args, &index, argument)
			if !ok || value == "" || seen[argument] {
				return options, actionLogCLIError(command, "--output requires one path")
			}
			seen[argument], options.outputPath = true, value
			continue
		}
		if mode.filters && actionLogFilterFlag(argument) {
			value, ok := nextActionLogValue(args, &index, argument)
			if !ok || value == "" || seen[argument] {
				return options, actionLogCLIError(command, argument+" requires one value")
			}
			seen[argument] = true
			if err := bindActionLogFilter(&options.filter, argument, value); err != nil {
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
	if err := options.filter.Validate(); err != nil {
		return options, actionLogCLIError(command, err.Error())
	}
	return options, nil
}

func nextActionLogValue(args []string, index *int, flag string) (string, bool) {
	value, ok := nextArgValue(args, index, flag, argValueNoLeadingDash)
	return value, ok && !strings.HasPrefix(value, "-")
}

func actionLogFilterFlag(value string) bool {
	switch value {
	case "--call", "--run", "--session", "--principal", "--tool", "--event", "--decision", "--since":
		return true
	default:
		return false
	}
}

func bindActionLogFilter(filter *actionledger.Filter, flag, value string) error {
	switch flag {
	case "--call":
		filter.CallID = value
	case "--run":
		filter.RunIdentity = value
	case "--session":
		filter.SessionIdentity = value
	case "--principal":
		filter.Principal = value
	case "--tool":
		filter.ToolIdentity = value
	case "--event":
		filter.Event = actionledger.EventType(value)
	case "--decision":
		filter.Decision = action.Decision(value)
	case "--since":
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return fmt.Errorf("--since must be an RFC3339 timestamp")
		}
		filter.Since = parsed
	}
	return nil
}

func openExistingActionLedger(ctx context.Context, repository string) (existingActionLedger, error) {
	resolvedRepository, err := agentsession.ResolveRepoRoot(repository)
	if err != nil {
		return existingActionLedger{}, err
	}
	resolvedHome, err := actionstate.ResolveHome("")
	if err != nil {
		return existingActionLedger{}, err
	}
	directory := filepath.Join(retention.ProjectDir(resolvedHome, resolvedRepository), "action")
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return existingActionLedger{repository: resolvedRepository}, nil
	} else if err != nil {
		return existingActionLedger{}, fmt.Errorf("inspect action ledger directory: %w", err)
	}
	lease, err := actionstate.AcquireExistingIdentityKey(ctx, resolvedHome)
	if err != nil {
		return existingActionLedger{}, err
	}
	storage, err := actionstate.OpenExistingPrivateProjectStorage(resolvedHome, resolvedRepository, lease)
	if err != nil {
		return existingActionLedger{}, errors.Join(err, lease.Close())
	}
	store, err := actionledger.OpenStore(storage)
	if err != nil {
		return existingActionLedger{}, errors.Join(err, lease.Close())
	}
	exists, err := store.ExistingState(ctx)
	if err != nil {
		return existingActionLedger{}, errors.Join(err, lease.Close())
	}
	if !exists {
		return existingActionLedger{
			stateStorage: &storage, lease: lease, repository: resolvedRepository,
			repositoryIdentity: storage.RepositoryIdentity(),
		}, nil
	}
	return existingActionLedger{
		store: store, stateStorage: &storage, lease: lease, repository: resolvedRepository,
		repositoryIdentity: storage.RepositoryIdentity(),
	}, nil
}

func joinActionLedgerCloseError(resultErr *error, reader existingActionLedger) {
	if reader.lease == nil {
		return
	}
	if err := reader.lease.Close(); err != nil {
		*resultErr = errors.Join(*resultErr, actionLogCLIError("action log", "close identity lease: "+err.Error()))
	}
}

func writeActionLogOutput(command string, stdout io.Writer, outputPath string, body []byte) error {
	if outputPath != "" {
		if _, err := atomicfile.WritePrivateNew(outputPath, body, 0o600); err != nil {
			return actionLogCLIError(command, "write output: "+err.Error())
		}
	}
	if _, err := stdout.Write(body); err != nil {
		return actionLogCLIError(command, "write stdout: "+err.Error())
	}
	return nil
}

func actionLogCLIError(command, message string) error {
	return &CLIError{ExitCode: 1, Message: "reconc " + command + ": " + message}
}
