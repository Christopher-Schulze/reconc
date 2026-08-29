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
	if err := scanManifestShape(body); err != nil {
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

func scanManifestShape(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("custom runtime input must contain a JSON object")
	}
	if err := scanManifestObject(decoder, 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("custom runtime input must contain exactly one JSON value")
	}
	return nil
}

func scanManifestObject(decoder *json.Decoder, depth int) error {
	return scanObject(decoder, depth, "custom runtime manifest", map[string]struct{}{
		"$schema": {}, "format_version": {}, "name": {}, "display_name": {}, "routes": {},
	}, []string{"$schema", "format_version", "name", "display_name", "routes"}, func(key string, value any) error {
		if key != "routes" {
			return scanJSONValue(decoder, value, depth)
		}
		if value != json.Delim('[') {
			return fmt.Errorf("custom runtime routes must contain a list of objects")
		}
		return scanRoutes(decoder, depth+1)
	})
}

func scanRoutes(decoder *json.Decoder, depth int) error {
	if depth >= 32 {
		return fmt.Errorf("custom runtime JSON exceeds 32 levels")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token != json.Delim('{') {
			return fmt.Errorf("custom runtime routes must contain a list of objects")
		}
		if err := scanRouteObject(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func scanRouteObject(decoder *json.Decoder, depth int) error {
	return scanObject(decoder, depth, "custom runtime route", map[string]struct{}{
		"host_event": {}, "event": {}, "support": {}, "response": {}, "error_policy": {},
		"timeout_policy": {}, "timeout_seconds": {}, "max_output_bytes": {}, "max_continuations": {},
		"guarantees": {}, "fields": {},
	}, []string{"host_event", "event", "support", "response", "error_policy", "timeout_policy", "timeout_seconds", "max_output_bytes", "guarantees", "fields"}, func(key string, value any) error {
		switch key {
		case "guarantees":
			if value != json.Delim('{') {
				return fmt.Errorf("custom runtime route.guarantees must contain an object")
			}
			return scanGuaranteesObject(decoder, depth+1)
		case "fields":
			if value != json.Delim('{') {
				return fmt.Errorf("custom runtime route.fields must contain an object")
			}
			return scanFieldsObject(decoder, depth+1)
		default:
			return scanJSONValue(decoder, value, depth)
		}
	})
}

func scanGuaranteesObject(decoder *json.Decoder, depth int) error {
	return scanObject(decoder, depth, "custom runtime route.guarantees", map[string]struct{}{
		"pre_execution": {}, "synchronous_response": {}, "authoritative_outcome": {},
		"continuation": {}, "continuation_ack": {}, "mcp_identity": {},
	}, []string{"pre_execution", "synchronous_response", "authoritative_outcome", "continuation", "continuation_ack", "mcp_identity"}, func(_ string, value any) error {
		return scanJSONValue(decoder, value, depth)
	})
}

func scanFieldsObject(decoder *json.Decoder, depth int) error {
	return scanObject(decoder, depth, "custom runtime route.fields", map[string]struct{}{
		"session_id": {}, "tool_name": {}, "tool_input": {}, "tool_response": {}, "tool_use_id": {},
		"error": {}, "is_interrupt": {}, "stop_hook_active": {}, "strict_continuation": {},
		"exit_code": {}, "mcp_tool": {}, "mcp_server_fingerprint": {}, "mcp_outcome": {},
	}, []string{"session_id"}, func(_ string, value any) error {
		return scanJSONValue(decoder, value, depth)
	})
}

func scanObject(decoder *json.Decoder, depth int, label string, allowed map[string]struct{}, required []string, consume func(string, any) error) error {
	if depth > 32 {
		return fmt.Errorf("custom runtime JSON exceeds 32 levels")
	}
	seen := map[string]struct{}{}
	for decoder.More() {
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
		_, known := allowed[text]
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if !known {
			if err := scanJSONValue(decoder, value, depth); err != nil {
				return err
			}
			return fmt.Errorf("%s contains unknown field %q", label, text)
		}
		for _, requiredField := range required {
			if text == requiredField && value == nil {
				return fmt.Errorf("%s: field %q is required and must not be null", label, text)
			}
		}
		if err := consume(text, value); err != nil {
			return err
		}
	}
	for _, requiredField := range required {
		if _, present := seen[requiredField]; !present {
			return fmt.Errorf("%s: field %q is required and must not be null", label, requiredField)
		}
	}
	_, err := decoder.Token()
	return err
}

func scanJSONValue(decoder *json.Decoder, token any, depth int) error {
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= 32 {
		return fmt.Errorf("custom runtime JSON exceeds 32 levels")
	}
	switch delimiter {
	case '{':
		return scanGenericObject(decoder, depth+1)
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(decoder, value, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("custom runtime JSON contains an unexpected delimiter")
	}
}

func scanGenericObject(decoder *json.Decoder, depth int) error {
	seen := map[string]struct{}{}
	for decoder.More() {
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
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if err := scanJSONValue(decoder, value, depth); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
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
