package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"reconc.dev/reconc/internal/hooks"
)

const hookVerificationFormatVersion = "reconc-hook-verification/v1"

type hookVerificationReport struct {
	FormatVersion string                   `json:"format_version"`
	Mode          string                   `json:"mode"`
	Complete      bool                     `json:"complete"`
	Results       []hookVerificationResult `json:"results"`
}

type hookVerificationResult struct {
	Kind               string   `json:"kind"`
	Surface            string   `json:"surface"`
	ArtifactGeneration string   `json:"artifact_generation"`
	Configuration      string   `json:"configuration"`
	Transport          string   `json:"transport"`
	PolicyDecision     string   `json:"policy_decision"`
	ResponseAdaptation string   `json:"response_adaptation"`
	DurationMillis     int64    `json:"duration_ms"`
	HostAvailable      *bool    `json:"host_available,omitempty"`
	Configured         bool     `json:"configured"`
	Discoverable       bool     `json:"discoverable"`
	Loaded             bool     `json:"loaded"`
	Observed           bool     `json:"observed"`
	Enforced           bool     `json:"enforced"`
	SyntheticEnforced  bool     `json:"synthetic_enforced"`
	Inferred           bool     `json:"inferred"`
	Degraded           bool     `json:"degraded"`
	Unsupported        []string `json:"unsupported"`
	ExpectedEvents     []string `json:"expected_events"`
	UnprovenEvents     []string `json:"unproven_events"`
	ObservedFields     []string `json:"observed_fields"`
	ResultClass        string   `json:"result_class"`
	Detail             string   `json:"detail"`
	ActionRequired     string   `json:"action_required"`
}

type hookVerifyOptions struct {
	host               string
	surface            string
	jsonOutput         bool
	live               bool
	allowAuthenticated bool
}

type offlineHookKindResult struct {
	artifactGeneration string
	configuration      string
	transport          string
	policyDecision     string
	responseAdaptation string
	durationMillis     int64
	configured         bool
	discoverable       bool
	syntheticEnforced  bool
	degraded           bool
	unsupported        []string
	detail             string
}

func runHookVerify(args []string, stdout, stderr io.Writer) error {
	options, help, err := parseHookVerifyOptions(args, stdout)
	if err != nil || help {
		return err
	}
	surfaces, err := selectHookVerificationSurfaces(options.host, options.surface)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	if options.live {
		return runLiveHookVerification(options, surfaces, os.Stdin, stdout, stderr)
	}
	report, err := runOfflineHookVerification(options, surfaces)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	return writeHookVerificationReport(report, options.jsonOutput, stdout)
}

func parseHookVerifyOptions(args []string, stdout io.Writer) (hookVerifyOptions, bool, error) {
	var options hookVerifyOptions
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--host":
			if index+1 >= len(args) {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc hook verify: --host requires a value"}
			}
			index++
			options.host = args[index]
		case "--surface":
			if index+1 >= len(args) {
				return options, false, &CLIError{ExitCode: 1, Message: "reconc hook verify: --surface requires a value"}
			}
			index++
			options.surface = args[index]
		case "--json":
			options.jsonOutput = true
		case "--live":
			options.live = true
		case "--allow-authenticated":
			options.allowAuthenticated = true
		case "-h", "--help":
			writeHookVerifyHelp(stdout)
			return options, true, nil
		default:
			return options, false, &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc hook verify: unknown flag %q", args[index])}
		}
	}
	return validateHookVerifyOptions(options)
}

func validateHookVerifyOptions(options hookVerifyOptions) (hookVerifyOptions, bool, error) {
	if options.surface != "" && options.host == "" {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc hook verify: --surface requires --host"}
	}
	if options.live && (options.host == "" || options.surface == "") {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc hook verify: --live requires --host and --surface"}
	}
	if options.live && !options.allowAuthenticated {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc hook verify: --live requires explicit --allow-authenticated approval"}
	}
	if !options.live && options.allowAuthenticated {
		return options, false, &CLIError{ExitCode: 1, Message: "reconc hook verify: --allow-authenticated is valid only with --live"}
	}
	return options, false, nil
}

func writeHookVerifyHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: reconc hook verify [--host KIND [--surface SURFACE]] [--json]")
	fmt.Fprintln(output, "       reconc hook verify --live --host KIND --surface SURFACE --allow-authenticated [--json]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "Offline mode uses a disposable repository and no model, account, cloud service, or caller repository.")
	fmt.Fprintln(output, "Live mode prepares an isolated repository, never launches a host, and waits for explicit operator confirmation.")
}

func selectHookVerificationSurfaces(kind, surface string) ([]hooks.VerificationSurface, error) {
	all := hooks.VerificationSurfaces()
	if kind == "" {
		return all, nil
	}
	selected := make([]hooks.VerificationSurface, 0, 8)
	for _, candidate := range all {
		if candidate.Kind == kind && (surface == "" || candidate.Surface == surface) {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		if _, ok := hooks.PlatformForKind(kind); !ok {
			return nil, fmt.Errorf("unknown host %q", kind)
		}
		return nil, fmt.Errorf("host %q has no surface %q", kind, surface)
	}
	return selected, nil
}

func writeHookVerificationReport(report hookVerificationReport, jsonOutput bool, output io.Writer) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc hook verify: encode JSON: " + err.Error()}
		}
		return nil
	}
	state := "complete"
	if !report.Complete {
		state = "incomplete"
	}
	fmt.Fprintf(output, "Hook verification: %s (%s)\n", state, report.Mode)
	for _, result := range report.Results {
		fmt.Fprintf(output, "- %s/%s: generation=%s configuration=%s transport=%s policy=%s response=%s duration=%dms\n",
			result.Kind, result.Surface, result.ArtifactGeneration, result.Configuration,
			result.Transport, result.PolicyDecision, result.ResponseAdaptation, result.DurationMillis)
		fmt.Fprintf(output, "  configured=%t discoverable=%t loaded=%t observed=%t enforced=%t synthetic-enforced=%t inferred=%t degraded=%t\n",
			result.Configured, result.Discoverable, result.Loaded, result.Observed,
			result.Enforced, result.SyntheticEnforced, result.Inferred, result.Degraded)
		if result.Detail != "" {
			fmt.Fprintf(output, "  detail: %s\n", result.Detail)
		}
		if len(result.UnprovenEvents) > 0 {
			fmt.Fprintf(output, "  live routes unproven: %s\n", strings.Join(result.UnprovenEvents, ", "))
		}
	}
	return nil
}
