package proofbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

var (
	ErrMalformedInput      = errors.New("malformed proof bundle")
	ErrUnsafeInput         = errors.New("unsafe proof bundle input")
	ErrUnsupportedContract = errors.New("unsupported proof bundle contract")
	ErrInvalidContract     = errors.New("invalid proof bundle contract")
)

// DecodeFile reads and strictly verifies one bounded regular proof bundle.
func DecodeFile(filePath string) (*Bundle, error) {
	body, err := readBundleFile(filePath)
	if err != nil {
		return nil, err
	}
	return Decode(body)
}

// Decode strictly decodes and verifies one proof-bundle JSON object.
func Decode(body []byte) (*Bundle, error) {
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrMalformedInput, MaxBytes)
	}
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("%w: input is not valid UTF-8", ErrMalformedInput)
	}
	if err := validateJSONKeys(body); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedInput, err)
	}
	if err := validateRequiredFields(body); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedInput, err)
	}
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedInput, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: multiple JSON values are not allowed", ErrMalformedInput)
	}
	if err := Verify(&bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func validateRequiredFields(body []byte) error {
	root, err := decodeRawObject(body)
	if err != nil {
		return err
	}
	if err := requireRawFields(root, "$schema", "format_version", "ok", "decision", "repo_root", "build", "task", "candidate", "checks", "evidence", "violations", "superseded_blocks", "completion_digest", "digest"); err != nil {
		return err
	}
	objects := []struct {
		name   string
		fields []string
	}{
		{"build", []string{"version", "provenance_format", "source_digest", "goos", "goarch"}},
		{"task", []string{"configured", "state"}},
		{"candidate", []string{"fingerprint", "policy_lock_hash", "git_available", "worktree_trusted", "dirty_paths"}},
		{"evidence", []string{"required_commands", "required_paths", "required_claims", "satisfied_checks", "command_proofs"}},
	}
	for _, required := range objects {
		name, fields := required.name, required.fields
		object, err := decodeRawObject(root[name])
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := requireRawFields(object, fields...); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := validateRequiredArrayObjects(root["checks"], "check", "id", "status", "detail"); err != nil {
		return err
	}
	if err := validateRequiredArrayObjects(root["violations"], "violation", "rule_id", "kind", "mode", "message", "matched_paths", "required_paths", "required_commands", "required_claims"); err != nil {
		return err
	}
	return validateNestedRequiredFields(root)
}

func validateNestedRequiredFields(root map[string]json.RawMessage) error {
	evidence, err := decodeRawObject(root["evidence"])
	if err != nil {
		return err
	}
	if err := validateRequiredArrayObjects(evidence["command_proofs"], "command proof", "command", "command_hash", "execution_mode", "outcome", "exit_code", "head", "index_tree", "receipt_digest", "candidate_bound", "fresh"); err != nil {
		return err
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(root["superseded_blocks"], &blocks); err != nil {
		return err
	}
	for _, raw := range blocks {
		block, err := decodeRawObject(raw)
		if err != nil {
			return err
		}
		if err := requireRawFields(block, "candidate_fingerprint", "policy_report_hash", "violations"); err != nil {
			return fmt.Errorf("superseded block: %w", err)
		}
		if err := validateRequiredArrayObjects(block["violations"], "superseded violation", "rule_id", "kind", "mode", "message", "matched_paths", "required_paths", "required_commands", "required_claims"); err != nil {
			return err
		}
	}
	return nil
}

func decodeRawObject(body []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("expected a non-null object")
	}
	return object, nil
}

func requireRawFields(object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("required field %q is missing", field)
		}
	}
	return nil
}

func validateRequiredArrayObjects(body []byte, name string, fields ...string) error {
	var values []json.RawMessage
	if err := json.Unmarshal(body, &values); err != nil {
		return err
	}
	for _, raw := range values {
		object, err := decodeRawObject(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := requireRawFields(object, fields...); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func readBundleFile(filePath string) ([]byte, error) {
	identity, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("inspect proof bundle: %w", err)
	}
	if identity.Mode()&os.ModeSymlink != 0 || !identity.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: input must be a real regular file", ErrUnsafeInput)
	}
	if identity.Size() > MaxBytes {
		return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrMalformedInput, MaxBytes)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open proof bundle: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened proof bundle: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(identity, opened) {
		return nil, fmt.Errorf("%w: input identity changed before reading", ErrUnsafeInput)
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read proof bundle: %w", err)
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrMalformedInput, MaxBytes)
	}
	return body, nil
}

func validateJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return errors.New("root value must be an object")
	}
	if err := validateJSONContainer(decoder, '{'); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func validateJSONContainer(decoder *json.Decoder, delimiter json.Delim) error {
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			if err := readUniqueJSONObjectKey(decoder, seen); err != nil {
				return err
			}
		}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if err := validateNestedJSONToken(decoder, value); err != nil {
			return err
		}
	}
	return validateJSONClosingDelimiter(decoder, delimiter)
}

func readUniqueJSONObjectKey(decoder *json.Decoder, seen map[string]bool) error {
	keyToken, err := decoder.Token()
	if err != nil {
		return err
	}
	key, ok := keyToken.(string)
	if !ok {
		return errors.New("object key is not a string")
	}
	if seen[key] {
		return fmt.Errorf("duplicate object key %q", key)
	}
	seen[key] = true
	return nil
}

func validateNestedJSONToken(decoder *json.Decoder, value json.Token) error {
	nested, ok := value.(json.Delim)
	if !ok {
		return nil
	}
	if nested != '{' && nested != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	return validateJSONContainer(decoder, nested)
}

func validateJSONClosingDelimiter(decoder *json.Decoder, delimiter json.Delim) error {
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return errors.New("mismatched JSON delimiter")
	}
	return nil
}
