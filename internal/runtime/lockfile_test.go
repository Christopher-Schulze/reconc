package runtime

import (
	"encoding/json"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
)

// lockfileDefaultMode extracts the validated default_mode from a loaded
// lockfile payload as a checked operation, so evaluation call sites do
// not depend on validation ordering for panic safety.
func lockfileDefaultMode(payload map[string]interface{}) (policy.Mode, error) {
	raw, ok := payload["default_mode"].(string)
	if !ok {
		return "", &rerrors.LockfileError{Message: "compiled lockfile default_mode must be a string"}
	}
	return policy.Mode(raw), nil
}

func loadLockfile(root string) (map[string]interface{}, error) {
	lock, err := readLockfile(root)
	if err != nil {
		return nil, err
	}
	return lock.payload, nil
}

func validateLockfileFreshness(root string, payload map[string]interface{}, migrated bool) error {
	bundle, err := ingest.LoadPolicySources(root)
	if err != nil {
		return err
	}
	currentDigest, err := compiler.ComputeSourceDigest(bundle)
	if err != nil {
		return &rerrors.LockfileError{Message: "compute current source digest", Cause: err}
	}
	return validateLockfileFreshnessBundle(payload, migrated, bundle, currentDigest)
}

func decodeActionPlan(payload map[string]interface{}) (*action.CompiledPlan, error) {
	raw, present := payload["actions"]
	if !present {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain an actions object"}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "encode compiled lockfile action plan", Cause: err}
	}
	return decodeActionPlanJSON(data)
}
