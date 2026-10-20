package runtime

import (
	"reconc.dev/reconc/internal/policy"
)

func matchingCommands(commands, expected []string, repoRoot string, match policy.CommandMatch) []string {
	return matchingCommandsWithEvidence(nil, nil, commands, expected, repoRoot, match)
}

func matchingForbiddenCommands(commands, expected []string, repoRoot string, match policy.CommandMatch) []string {
	return matchingForbiddenCommandsWithCache(nil, commands, expected, repoRoot, match)
}

func matchingCommandResults(results []CommandResult, expected []string, outcome string, repoRoot string) []string {
	return matchingCommandResultsSince(results, expected, outcome, repoRoot, 0, policy.CommandMatchExact)
}

func matchingCommandResultsSince(results []CommandResult, expected []string, outcome string, repoRoot string, minimumEpoch uint64, match policy.CommandMatch) []string {
	return matchingCommandResultsSinceWithEvidence(nil, nil, results, expected, outcome, repoRoot, minimumEpoch, match)
}
