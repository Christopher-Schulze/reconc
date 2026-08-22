package customruntime

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	maxHostPayloadBytes         = 8 << 20
	maxHostJSONDepth            = 32
	maxHostObjectMembers        = 65_536
	maxHostArrayItems           = 65_536
	maxSelectedHostFields       = 13
	maxRetainedHostPayloadBytes = 2 << 20
)

type selectedHostValues map[string]interface{}

type hostJSONBudget struct {
	objectMembers int
	arrayItems    int
	retainedBytes int
}

type hostPointerNode struct {
	children map[string]*hostPointerNode
	pointer  string
	terminal bool
}

func selectHostValues(fields FieldMappings, body []byte) (selectedHostValues, error) {
	if err := validateHostJSON(body); err != nil {
		return nil, err
	}
	root, err := buildHostPointerTrie(fields)
	if err != nil {
		return nil, err
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(body))
	selected := selectedHostValues{}
	budget := hostJSONBudget{}
	if err := selectHostJSONValue(decoder, root, selected, &budget); err != nil {
		return nil, translateHostJSONError(err)
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("custom host payload must contain exactly one JSON object")
		}
		return nil, translateHostJSONError(err)
	}
	return selected, nil
}

func validateHostJSON(body []byte) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(body))
	if decoder.PeekKind() != jsontext.KindBeginObject {
		return fmt.Errorf("custom host payload must contain a JSON object")
	}
	budget := hostJSONBudget{}
	if err := validateHostJSONValue(decoder, 0, &budget); err != nil {
		return translateHostJSONError(err)
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("custom host payload must contain exactly one JSON value")
		}
		return translateHostJSONError(err)
	}
	return nil
}

func validateHostJSONValue(decoder *jsontext.Decoder, depth int, budget *hostJSONBudget) error {
	kind := decoder.PeekKind()
	switch kind {
	case jsontext.KindBeginObject:
		if depth >= maxHostJSONDepth {
			return fmt.Errorf("custom runtime JSON exceeds %d levels", maxHostJSONDepth)
		}
		if _, err := decoder.ReadToken(); err != nil {
			return err
		}
		for decoder.PeekKind() != jsontext.KindEndObject {
			name, err := decoder.ReadToken()
			if err != nil {
				return err
			}
			if name.Kind() != jsontext.KindString {
				return fmt.Errorf("custom runtime JSON object key is invalid")
			}
			budget.objectMembers++
			if budget.objectMembers > maxHostObjectMembers {
				return fmt.Errorf("custom runtime JSON exceeds %d object members", maxHostObjectMembers)
			}
			if err := validateHostJSONValue(decoder, depth+1, budget); err != nil {
				return err
			}
		}
		_, err := decoder.ReadToken()
		return err
	case jsontext.KindBeginArray:
		if depth >= maxHostJSONDepth {
			return fmt.Errorf("custom runtime JSON exceeds %d levels", maxHostJSONDepth)
		}
		if _, err := decoder.ReadToken(); err != nil {
			return err
		}
		for decoder.PeekKind() != jsontext.KindEndArray {
			budget.arrayItems++
			if budget.arrayItems > maxHostArrayItems {
				return fmt.Errorf("custom runtime JSON exceeds %d array items", maxHostArrayItems)
			}
			if err := validateHostJSONValue(decoder, depth+1, budget); err != nil {
				return err
			}
		}
		_, err := decoder.ReadToken()
		return err
	default:
		_, err := decoder.ReadToken()
		return err
	}
}

func buildHostPointerTrie(fields FieldMappings) (*hostPointerNode, error) {
	root := &hostPointerNode{children: map[string]*hostPointerNode{}}
	selectedFields := 0
	for _, pointer := range fieldMappingPointers(fields) {
		if pointer == "" {
			continue
		}
		selectedFields++
		if selectedFields > maxSelectedHostFields {
			return nil, fmt.Errorf("custom runtime route exceeds %d selected fields", maxSelectedHostFields)
		}
		if !validJSONPointer(pointer) {
			return nil, fmt.Errorf("invalid JSON Pointer %q", pointer)
		}
		node := root
		for _, raw := range strings.Split(pointer[1:], "/") {
			segment := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
			child := node.children[segment]
			if child == nil {
				child = &hostPointerNode{children: map[string]*hostPointerNode{}}
				node.children[segment] = child
			}
			node = child
		}
		node.pointer = pointer
		node.terminal = true
	}
	return root, nil
}

func selectHostJSONValue(decoder *jsontext.Decoder, node *hostPointerNode, selected selectedHostValues, budget *hostJSONBudget) error {
	if node.terminal {
		value, err := decoder.ReadValue()
		if err != nil {
			return err
		}
		budget.retainedBytes += len(value)
		if budget.retainedBytes > maxRetainedHostPayloadBytes {
			return fmt.Errorf("custom runtime selected host payload exceeds %d bytes", maxRetainedHostPayloadBytes)
		}
		decoded, err := decodeSelectedHostValue(value)
		if err != nil {
			return err
		}
		assignSelectedHostValues(node, decoded, selected)
		return nil
	}

	switch decoder.PeekKind() {
	case jsontext.KindBeginObject:
		if _, err := decoder.ReadToken(); err != nil {
			return err
		}
		for decoder.PeekKind() != jsontext.KindEndObject {
			name, err := decoder.ReadToken()
			if err != nil {
				return err
			}
			child := node.children[name.String()]
			if child == nil {
				if err := decoder.SkipValue(); err != nil {
					return err
				}
				continue
			}
			if err := selectHostJSONValue(decoder, child, selected, budget); err != nil {
				return err
			}
		}
		_, err := decoder.ReadToken()
		return err
	case jsontext.KindBeginArray:
		if _, err := decoder.ReadToken(); err != nil {
			return err
		}
		index := 0
		for decoder.PeekKind() != jsontext.KindEndArray {
			child := node.children[strconv.Itoa(index)]
			if child == nil {
				if err := decoder.SkipValue(); err != nil {
					return err
				}
			} else if err := selectHostJSONValue(decoder, child, selected, budget); err != nil {
				return err
			}
			index++
		}
		_, err := decoder.ReadToken()
		return err
	default:
		return decoder.SkipValue()
	}
}

func decodeSelectedHostValue(value jsontext.Value) (interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode selected custom host value: %w", err)
	}
	return decoded, nil
}

func assignSelectedHostValues(node *hostPointerNode, value interface{}, selected selectedHostValues) {
	if node.terminal {
		selected[node.pointer] = value
	}
	for segment, child := range node.children {
		nested, ok := selectHostSegment(value, segment)
		if ok {
			assignSelectedHostValues(child, nested, selected)
		}
	}
}

func selectHostSegment(value interface{}, segment string) (interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		selected, ok := typed[segment]
		return selected, ok
	case []interface{}:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(typed) || strconv.Itoa(index) != segment {
			return nil, false
		}
		return typed[index], true
	default:
		return nil, false
	}
}

func translateHostJSONError(err error) error {
	if errors.Is(err, jsontext.ErrDuplicateName) {
		if syntax, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
			return fmt.Errorf("custom runtime JSON contains duplicate key %q", syntax.JSONPointer.LastToken())
		}
		return fmt.Errorf("custom runtime JSON contains a duplicate key")
	}
	return fmt.Errorf("decode custom host payload: %w", err)
}
