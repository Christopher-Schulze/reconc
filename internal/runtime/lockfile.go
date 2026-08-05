package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
)

const maxLockfileBytes int64 = 16 << 20

// --- Lockfile loading + freshness ---

type decodedLockfile struct {
	payload  map[string]interface{}
	migrated bool
	byteHash [sha256.Size]byte
}

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

func lockfileRefreshRequired(err error) error {
	var lockErr *rerrors.LockfileError
	if !stderrors.As(err, &lockErr) {
		return err
	}
	return &rerrors.LockfileError{
		Message: lockErr.Message + "; explicit refresh required; run `reconc refresh .`",
		Cause:   lockErr.Cause,
	}
}

func loadLockfile(root string) (map[string]interface{}, error) {
	lock, err := readLockfile(root)
	if err != nil {
		return nil, err
	}
	return lock.payload, nil
}

func readLockfile(root string) (*decodedLockfile, error) {
	data, err := readLockfileBytes(root)
	if err != nil {
		return nil, err
	}
	lock, err := decodeLockfile(data)
	if err != nil {
		return nil, err
	}
	lock.byteHash = sha256.Sum256(data)
	return lock, nil
}

func readLockfileBytes(root string) ([]byte, error) {
	lf, err := resolvePolicyFile(root, ingest.LockfilePath)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile escapes the repository", Cause: err}
	}
	data, err := boundedio.ReadFile(lf, maxLockfileBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &rerrors.LockfileError{Message: "compiled lockfile not found at " + ingest.LockfilePath}
		}
		return nil, &rerrors.LockfileError{Message: "read lockfile", Cause: err}
	}
	return data, nil
}

func decodeLockfile(data []byte) (*decodedLockfile, error) {
	var payload map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile is not valid JSON", Cause: err}
	}
	if payload == nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain a JSON object at the top level"}
	}
	// Route every non-current payload through the migration table before
	// validating the rest of the contract.
	migrated, applied, err := compiler.MigrateLockfile(payload)
	if err != nil {
		return nil, err
	}
	payload = migrated

	if err := compiler.ValidateLockfileEnvelope(payload); err != nil {
		return nil, err
	}

	defaultMode, _ := payload["default_mode"].(string)
	if !policy.Mode(defaultMode).Valid() {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile has invalid default_mode: " + defaultMode}
	}

	rulesRaw, ok := payload["rules"].([]interface{})
	if !ok {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain a 'rules' list"}
	}
	ruleCountNum, _ := payload["rule_count"].(json.Number)
	expectedCount, err := ruleCountNum.Int64()
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain an integer 'rule_count'"}
	}
	if int(expectedCount) != len(rulesRaw) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile rule_count does not match the embedded rules"}
	}
	sources, ok := payload["sources"].([]interface{})
	if !ok {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain a 'sources' list"}
	}
	sourceCountNum, ok := payload["source_count"].(json.Number)
	if !ok {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain an integer 'source_count'"}
	}
	expectedSourceCount, err := sourceCountNum.Int64()
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile must contain an integer 'source_count'"}
	}
	if int(expectedSourceCount) != len(sources) {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile source_count does not match the embedded sources"}
	}
	if _, err := decodeMCPPolicy(payload); err != nil {
		return nil, err
	}

	return &decodedLockfile{payload: payload, migrated: len(applied) > 0}, nil
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

func validateLockfileFreshnessBundle(payload map[string]interface{}, migrated bool, bundle *ingest.SourceBundle, currentDigest string) error {
	if int(numAsInt(payload["source_count"])) != len(bundle.Sources) {
		return &rerrors.LockfileError{Message: "compiled lockfile source_count does not match the current policy sources"}
	}
	stored, _ := payload["source_digest"].(string)
	if len(stored) != 64 {
		return &rerrors.LockfileError{Message: "compiled lockfile source_digest is missing or invalid"}
	}
	if stored != currentDigest {
		return &rerrors.LockfileError{Message: "compiled lockfile source_digest does not match the current policy sources"}
	}
	if !migrated {
		return nil
	}

	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		return err
	}
	if err := compiler.ValidateEmbeddedRules(payload, parsed); err != nil {
		return err
	}
	lockedMode, _ := payload["default_mode"].(string)
	if string(parsed.DefaultMode) != lockedMode {
		return &rerrors.LockfileError{Message: "compiled lockfile default_mode does not match the current policy sources"}
	}
	if int(numAsInt(payload["rule_count"])) != len(parsed.Rules) {
		return &rerrors.LockfileError{Message: "compiled lockfile rule_count does not match the current policy sources"}
	}
	return nil
}

// ValidatePolicyLockfile verifies that the discovered lockfile is readable,
// structurally valid, and fresh without compiling or writing any repository
// state. Callers use it for read-only status and gate surfaces.
func ValidatePolicyLockfile(startPath string) error {
	return NewEvaluator().ValidatePolicyLockfile(startPath)
}

// ValidatePolicyLockfile validates through this evaluator's immutable plan.
func (e *Evaluator) ValidatePolicyLockfile(startPath string) error {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return err
	}
	if !discovery.Discovered {
		warning := "no policy markers discovered"
		if len(discovery.Warnings) > 0 {
			warning = discovery.Warnings[0]
		}
		return fmt.Errorf("%s", warning)
	}
	_, err = e.loadFreshRuntimePlan(discovery.RepoRoot)
	return err
}

// LoadMCPPolicy returns the fresh, validated MCP contract. A nil contract
// means the optional compiler-config section is absent.
func LoadMCPPolicy(startPath string) (*policy.MCPPolicy, error) {
	return NewEvaluator().LoadMCPPolicy(startPath)
}

// LoadMCPPolicy returns the typed MCP contract from this evaluator's plan.
func (e *Evaluator) LoadMCPPolicy(startPath string) (*policy.MCPPolicy, error) {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return nil, err
	}
	if !discovery.Discovered {
		return nil, fmt.Errorf("no policy markers discovered")
	}
	plan, err := e.loadFreshRuntimePlan(discovery.RepoRoot)
	if err != nil {
		return nil, err
	}
	if plan.mcp == nil {
		return nil, nil
	}
	contract := *plan.mcp
	contract.Tools = policy.SortedMCPTools(contract.Tools)
	return &contract, nil
}

func decodeMCPPolicy(payload map[string]interface{}) (*policy.MCPPolicy, error) {
	raw, present := payload["mcp"]
	if !present {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "encode compiled lockfile MCP contract", Cause: err}
	}
	var contract policy.MCPPolicy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile MCP contract is invalid", Cause: err}
	}
	if err := contract.Validate(); err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile MCP contract is invalid: " + err.Error()}
	}
	contract.Tools = policy.SortedMCPTools(contract.Tools)
	return &contract, nil
}

func numAsInt(v interface{}) int64 {
	if n, ok := v.(json.Number); ok {
		i, _ := n.Int64()
		return i
	}
	if i, ok := v.(int); ok {
		return int64(i)
	}
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}
