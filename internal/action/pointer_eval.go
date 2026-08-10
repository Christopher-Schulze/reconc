package action

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

type PointerResult struct {
	State PointerState
	Value Value
}

func ResolvePointer(root Value, pointer string) (PointerResult, error) {
	tokens, err := CompilePointer(pointer)
	if err != nil {
		return PointerResult{}, err
	}
	return resolvePointerTokens(root, tokens), nil
}

func resolvePointerTokens(root Value, tokens []string) PointerResult {
	current := root
	for _, token := range tokens {
		switch current.Kind() {
		case ValueObject:
			selected, ok := current.Lookup(token)
			if !ok {
				return PointerResult{State: PointerMissing}
			}
			current = selected
		case ValueArray:
			items, _ := current.Items()
			index, ok := canonicalArrayIndex(token)
			if !ok {
				return PointerResult{State: PointerInvalidIndex}
			}
			if index >= len(items) {
				return PointerResult{State: PointerMissing}
			}
			current = items[index]
		default:
			return PointerResult{State: PointerWrongContainer}
		}
	}
	if current.Kind() == ValueNull {
		return PointerResult{State: PointerNull, Value: current}
	}
	return PointerResult{State: PointerPresent, Value: current}
}

func canonicalArrayIndex(token string) (int, bool) {
	if token == "" || token == "-" || len(token) > 1 && token[0] == '0' {
		return 0, false
	}
	for _, character := range token {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(token, 10, strconv.IntSize)
	if err != nil {
		return 0, false
	}
	index := int(value)
	if index < 0 {
		return 0, false
	}
	return index, true
}

func validateCompiledPointer(tokens []string) error {
	if len(tokens) > MaxPointerBytes {
		return fmt.Errorf("compiled pointer token count exceeds the byte bound")
	}
	bytes := 0
	for _, token := range tokens {
		if !utf8.ValidString(token) {
			return fmt.Errorf("compiled pointer token is not valid UTF-8")
		}
		bytes += len(token)
		if bytes > MaxPointerBytes {
			return fmt.Errorf("compiled pointer tokens exceed the byte bound")
		}
	}
	return nil
}
