package tasklifecycle

import (
	"errors"
)

// onlyConcurrentReadMutation reports a read that failed solely because TASK
// files changed while it ran, which a fresh read resolves.
func onlyConcurrentReadMutation(err error) bool {
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Issues) == 0 {
		return false
	}
	for _, item := range validation.Issues {
		if item.ID != "task/read/concurrent-mutation" {
			return false
		}
	}
	return true
}
