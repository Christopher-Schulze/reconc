package cli

import (
	"fmt"

	"reconc.dev/reconc/internal/policyauthor"
	"reconc.dev/reconc/internal/runtime"
)

type policyAuthorOptions struct {
	repo          string
	repoSet       bool
	candidateFile string
	detected      bool
	target        string
	corpusPaths   []string
	caseID        string
	apply         bool
	jsonOutput    bool
	inputs        runtime.ExecutionInputs
	hasEvidence   bool
}

func parsePolicyAuthorOptions(args []string) (policyAuthorOptions, error) {
	options := policyAuthorOptions{
		repo: ".", target: policyauthor.DefaultTarget, caseID: "policy-author",
		inputs: runtime.Empty(),
	}
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--detected":
			options.detected = true
		case "--apply":
			options.apply = true
		case "--json":
			options.jsonOutput = true
		case "--candidate", "--target", "--corpus", "--fixture", "--case-id":
			value, ok := nextArgValue(args, &index, flag, argValueNoLeadingDash)
			if !ok || value == "" {
				return options, policyAuthorError(flag + " requires a value")
			}
			switch flag {
			case "--candidate":
				options.candidateFile = value
			case "--target":
				options.target = value
			case "--corpus", "--fixture":
				options.corpusPaths = append(options.corpusPaths, value)
			case "--case-id":
				options.caseID = value
			}
		case "--read", "--write", "--command", "--command-success", "--command-failure", "--claim":
			if err := consumeImpactEvidence(flag, args, &index, &options.inputs, &options.hasEvidence); err != nil {
				return options, policyAuthorError(err.Error())
			}
		default:
			if len(flag) > 0 && flag[0] == '-' {
				return options, policyAuthorError(fmt.Sprintf("unknown flag %q", flag))
			}
			if options.repoSet {
				return options, policyAuthorError("expected at most one repo path")
			}
			options.repo, options.repoSet = flag, true
		}
	}
	if (options.candidateFile == "") == !options.detected {
		return options, policyAuthorError("specify exactly one of --candidate FILE or --detected")
	}
	return options, nil
}

func policyAuthorError(message string) error {
	return &CLIError{ExitCode: 1, Message: "reconc policy author: " + message}
}
