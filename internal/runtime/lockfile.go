package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	stderrors "errors"
	"fmt"
	"io"
	"os"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
)

const maxLockfileBytes = compiler.MaxLockfileBytes

const maxLockfileJSONDepth = action.MaxJSONDepth + 2*action.MaxConditionDepth + 16

// --- Lockfile loading + freshness ---

type decodedLockfile struct {
	payload     map[string]interface{}
	migrated    bool
	byteHash    [sha256.Size]byte
	rulesJSON   []byte
	actionsJSON []byte
	actions     *action.CompiledPlan
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
	payload, err := decodeStrictLockfileJSON(data)
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "compiled lockfile is not strict JSON", Cause: err}
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
	rulesJSON, err := json.Marshal(payload["rules"])
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "encode compiled lockfile rules", Cause: err}
	}
	actionsJSON, err := json.Marshal(payload["actions"])
	if err != nil {
		return nil, &rerrors.LockfileError{Message: "encode compiled lockfile action plan", Cause: err}
	}
	actions, err := decodeActionPlanJSON(actionsJSON)
	if err != nil {
		return nil, err
	}

	return &decodedLockfile{
		payload: payload, migrated: len(applied) > 0,
		rulesJSON: rulesJSON, actionsJSON: actionsJSON, actions: actions,
	}, nil
}

func decodeStrictLockfileJSON(data []byte) (map[string]interface{}, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	value, err := decodeStrictJSONValue(decoder, 1)
	if err != nil {
		return nil, classifyStrictLockfileJSONError(data, err)
	}
	if trailing, err := decoder.ReadToken(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, classifyStrictLockfileJSONError(data, err)
	} else if trailing.Kind() != jsontext.KindInvalid {
		return nil, fmt.Errorf("multiple JSON values are not allowed")
	}
	payload, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("root value must be an object")
	}
	return payload, nil
}

func classifyStrictLockfileJSONError(data []byte, err error) error {
	if stderrors.Is(err, jsontext.ErrDuplicateName) {
		return fmt.Errorf("duplicate object key: %w", err)
	}
	if unicodeErr := action.ValidateJSONUnicode(data); unicodeErr != nil {
		return unicodeErr
	}
	return err
}

func decodeStrictJSONValue(decoder *jsontext.Decoder, depth int) (interface{}, error) {
	if depth > maxLockfileJSONDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", maxLockfileJSONDepth)
	}
	token, err := decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	switch token.Kind() {
	case jsontext.KindNull:
		return nil, nil
	case jsontext.KindFalse:
		return false, nil
	case jsontext.KindTrue:
		return true, nil
	case jsontext.KindString:
		return token.String(), nil
	case jsontext.KindNumber:
		return json.Number(token.String()), nil
	case jsontext.KindBeginObject:
		return decodeStrictJSONObject(decoder, depth)
	case jsontext.KindBeginArray:
		return decodeStrictJSONArray(decoder, depth)
	default:
		return nil, fmt.Errorf("unexpected JSON token %q", token.Kind())
	}
}

func decodeStrictJSONObject(decoder *jsontext.Decoder, depth int) (map[string]interface{}, error) {
	object := map[string]interface{}{}
	for decoder.PeekKind() != jsontext.KindEndObject {
		keyToken, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		if keyToken.Kind() != jsontext.KindString {
			return nil, fmt.Errorf("object key is not a string")
		}
		key := keyToken.String()
		child, err := decodeStrictJSONValue(decoder, depth+1)
		if err != nil {
			return nil, err
		}
		object[key] = child
	}
	closing, err := decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	if closing.Kind() != jsontext.KindEndObject {
		return nil, fmt.Errorf("JSON object closed by %q", closing.Kind())
	}
	return object, nil
}

func decodeStrictJSONArray(decoder *jsontext.Decoder, depth int) ([]interface{}, error) {
	array := []interface{}{}
	for decoder.PeekKind() != jsontext.KindEndArray {
		child, err := decodeStrictJSONValue(decoder, depth+1)
		if err != nil {
			return nil, err
		}
		array = append(array, child)
	}
	closing, err := decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	if closing.Kind() != jsontext.KindEndArray {
		return nil, fmt.Errorf("JSON array closed by %q", closing.Kind())
	}
	return array, nil
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

// LoadActionPlan returns the fresh canonical action contract as a defensive
// copy. Runtime matcher programs remain owned by the evaluator.
func LoadActionPlan(startPath string) (action.Plan, error) {
	return NewEvaluator().LoadActionPlan(startPath)
}

// LoadActionPlan returns the typed canonical action contract from this
// evaluator's immutable runtime plan.
func (e *Evaluator) LoadActionPlan(startPath string) (action.Plan, error) {
	discovery, err := ingest.DiscoverPolicyRepo(startPath)
	if err != nil {
		return action.Plan{}, err
	}
	if !discovery.Discovered {
		return action.Plan{}, fmt.Errorf("no policy markers discovered")
	}
	plan, err := e.loadFreshRuntimePlan(discovery.RepoRoot)
	if err != nil {
		return action.Plan{}, err
	}
	return plan.actions.Plan(), nil
}

// LoadMCPPolicy returns the fresh compatibility view derived from canonical
// host_mcp action declarations. A nil contract means the action plan retains
// the legacy host baseline and declares no host tools.
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
	return deriveMCPPolicy(plan.actions.Plan())
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

func deriveMCPPolicy(plan action.Plan) (*policy.MCPPolicy, error) {
	mode := policy.MCPUnclassifiedHost
	if plan.Defaults.HostUnmatched == action.DecisionBlock {
		mode = policy.MCPUnclassifiedDeny
	}
	contract := &policy.MCPPolicy{Unclassified: mode, Tools: []policy.MCPToolPolicy{}}
	for _, tool := range plan.Tools {
		if tool.Transport != action.TransportHostMCP {
			continue
		}
		contract.Tools = append(contract.Tools, policy.MCPToolPolicy{
			Platform:          policy.MCPPlatform(tool.Platform),
			ServerFingerprint: tool.ServerFingerprint,
			Tool:              tool.Tool,
			Effect:            policy.MCPEffect(tool.Effect.Kind),
			PathFields:        append([]string(nil), tool.Effect.PathFields...),
			CommandField:      tool.Effect.CommandField,
			SourcePath:        tool.SourceIdentity,
		})
	}
	if len(contract.Tools) == 0 && mode == policy.MCPUnclassifiedHost {
		return nil, nil
	}
	if err := contract.Validate(); err != nil {
		return nil, &rerrors.LockfileError{Message: "canonical action plan cannot produce the MCP compatibility view: " + err.Error()}
	}
	contract.Tools = policy.SortedMCPTools(contract.Tools)
	return contract, nil
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
