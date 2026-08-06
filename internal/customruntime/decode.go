package customruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"
)

// DecodeManifest strictly decodes, canonicalizes, and validates one manifest.
func DecodeManifest(body []byte) (Manifest, error) {
	if len(body) == 0 || len(body) > MaxManifestBytes || !utf8.Valid(body) {
		return Manifest{}, fmt.Errorf("custom runtime manifest must be non-empty UTF-8 within %d bytes", MaxManifestBytes)
	}
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestFieldPresence(body); err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode custom runtime manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("custom runtime manifest must contain exactly one JSON object")
	}
	if manifest.Routes == nil {
		return Manifest{}, fmt.Errorf("custom runtime routes must not be null")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	sort.Slice(manifest.Routes, func(left, right int) bool {
		return manifest.Routes[left].HostEvent < manifest.Routes[right].HostEvent
	})
	return manifest, nil
}

func validateManifestFieldPresence(body []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("decode custom runtime manifest fields: %w", err)
	}
	if err := requireJSONFields(document, "$schema", "format_version", "name", "display_name", "routes"); err != nil {
		return fmt.Errorf("custom runtime manifest: %w", err)
	}
	var routes []map[string]json.RawMessage
	if err := json.Unmarshal(document["routes"], &routes); err != nil || routes == nil {
		return fmt.Errorf("custom runtime routes must contain a list of objects")
	}
	for index, route := range routes {
		if err := requireJSONFields(route, "host_event", "event", "support", "response", "error_policy", "timeout_policy", "timeout_seconds", "max_output_bytes", "guarantees", "fields"); err != nil {
			return fmt.Errorf("custom runtime route[%d]: %w", index, err)
		}
		var guarantees map[string]json.RawMessage
		if err := json.Unmarshal(route["guarantees"], &guarantees); err != nil || guarantees == nil {
			return fmt.Errorf("custom runtime route[%d].guarantees must contain an object", index)
		}
		if err := requireJSONFields(guarantees, "pre_execution", "synchronous_response", "authoritative_outcome", "continuation", "continuation_ack", "mcp_identity"); err != nil {
			return fmt.Errorf("custom runtime route[%d].guarantees: %w", index, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(route["fields"], &fields); err != nil || fields == nil {
			return fmt.Errorf("custom runtime route[%d].fields must contain an object", index)
		}
		if err := requireJSONFields(fields, "session_id"); err != nil {
			return fmt.Errorf("custom runtime route[%d].fields: %w", index, err)
		}
	}
	return nil
}

func requireJSONFields(document map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		value, ok := document[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("field %q is required and must not be null", field)
		}
	}
	return nil
}

func rejectDuplicateJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("custom runtime input must contain a JSON object")
	}
	if err := rejectDuplicateContainerKeys(decoder, '{', 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("custom runtime input must contain exactly one JSON value")
	}
	return nil
}

func rejectDuplicateContainerKeys(decoder *json.Decoder, delimiter json.Delim, depth int) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			text, ok := key.(string)
			if !ok {
				return fmt.Errorf("custom runtime JSON object key is invalid")
			}
			if _, duplicate := seen[text]; duplicate {
				return fmt.Errorf("custom runtime JSON contains duplicate key %q", text)
			}
			seen[text] = struct{}{}
		}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if nested, ok := value.(json.Delim); ok {
			if nested != '{' && nested != '[' {
				return fmt.Errorf("custom runtime JSON contains an unexpected delimiter")
			}
			if depth >= 32 {
				return fmt.Errorf("custom runtime JSON exceeds 32 levels")
			}
			if err := rejectDuplicateContainerKeys(decoder, nested, depth+1); err != nil {
				return err
			}
		}
	}
	_, err := decoder.Token()
	return err
}
