package templates

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Variable is one validated single-segment template token in a glob or
// template-bearing rule field. Start and End are byte offsets into the source
// string, with End exclusive.
type Variable struct {
	Name  string
	Start int
	End   int
}

// Variables scans one template-bearing string using Reconc's canonical
// grammar. A variable is exactly {name}, where name matches
// [A-Za-z_][A-Za-z0-9_]*. Glob alternatives such as {js,ts} remain valid and
// may contain variables recursively. Braces that are neither valid variables
// nor balanced alternatives must be escaped with a backslash.
func Variables(input string) ([]Variable, error) {
	if !utf8.ValidString(input) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	variables, err := scanVariables(input, 0, len(input))
	if err != nil {
		return nil, err
	}
	return variables, nil
}

func scanVariables(input string, start, end int) ([]Variable, error) {
	variables := make([]Variable, 0)
	for index := start; index < end; {
		switch input[index] {
		case '\\':
			if index+1 >= end {
				return nil, fmt.Errorf("dangling escape at byte %d", index)
			}
			_, size := utf8.DecodeRuneInString(input[index+1 : end])
			if size == 0 {
				return nil, fmt.Errorf("invalid escape at byte %d", index)
			}
			index += 1 + size
		case '{':
			if nameEnd, ok := validVariableEnd(input, index, end); ok {
				variables = append(variables, Variable{Name: input[index+1 : nameEnd], Start: index, End: nameEnd + 1})
				index = nameEnd + 1
				continue
			}
			groupEnd, hasComma, err := findBraceGroup(input, index, end)
			if err != nil {
				return nil, err
			}
			if !hasComma {
				return nil, fmt.Errorf("invalid brace expression at byte %d; use {name}, {a,b}, or escape literal braces", index)
			}
			nested, err := scanVariables(input, index+1, groupEnd)
			if err != nil {
				return nil, err
			}
			variables = append(variables, nested...)
			index = groupEnd + 1
		case '}':
			return nil, fmt.Errorf("unmatched closing brace at byte %d", index)
		default:
			_, size := utf8.DecodeRuneInString(input[index:end])
			if size == 0 {
				return nil, fmt.Errorf("invalid UTF-8 at byte %d", index)
			}
			index += size
		}
	}
	return variables, nil
}

func validVariableEnd(input string, start, end int) (int, bool) {
	if start+2 > end || start+1 >= end || !isVariableStart(input[start+1]) {
		return 0, false
	}
	index := start + 2
	for index < end && isVariablePart(input[index]) {
		index++
	}
	if index == end || input[index] != '}' {
		return 0, false
	}
	return index, true
}

func isVariableStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isVariablePart(value byte) bool {
	return isVariableStart(value) || value >= '0' && value <= '9'
}

func findBraceGroup(input string, start, end int) (groupEnd int, hasComma bool, err error) {
	depth := 0
	classDepth := 0
	for index := start + 1; index < end; index++ {
		switch input[index] {
		case '\\':
			if index+1 >= end {
				return 0, false, fmt.Errorf("dangling escape at byte %d", index)
			}
			_, size := utf8.DecodeRuneInString(input[index+1 : end])
			index += size
		case '[':
			classDepth++
		case ']':
			if classDepth > 0 {
				classDepth--
			}
		case '{':
			if classDepth == 0 {
				depth++
			}
		case ',':
			if classDepth == 0 && depth == 0 {
				hasComma = true
			}
		case '}':
			if classDepth > 0 {
				continue
			}
			if depth == 0 {
				return index, hasComma, nil
			}
			depth--
		}
	}
	return 0, false, fmt.Errorf("unterminated brace expression at byte %d", start)
}

// MaskVariables replaces every validated variable token with replacement.
// It preserves glob alternatives and escaped literal braces byte-for-byte.
func MaskVariables(input, replacement string) (string, error) {
	variables, err := Variables(input)
	if err != nil {
		return "", err
	}
	if len(variables) == 0 {
		return input, nil
	}
	var builder strings.Builder
	last := 0
	for _, variable := range variables {
		builder.WriteString(input[last:variable.Start])
		builder.WriteString(replacement)
		last = variable.End
	}
	builder.WriteString(input[last:])
	return builder.String(), nil
}

// Substitute replaces every validated variable with its bound value. Missing
// bindings are reported in stable lexical order and leave their tokens intact.
func Substitute(input string, bindings map[string]string) (string, error) {
	variables, err := Variables(input)
	if err != nil {
		return "", err
	}
	if len(variables) == 0 {
		return input, nil
	}
	missingSet := make(map[string]struct{})
	var builder strings.Builder
	last := 0
	for _, variable := range variables {
		builder.WriteString(input[last:variable.Start])
		value, ok := bindings[variable.Name]
		if !ok {
			builder.WriteString(input[variable.Start:variable.End])
			missingSet[variable.Name] = struct{}{}
		} else {
			builder.WriteString(value)
		}
		last = variable.End
	}
	builder.WriteString(input[last:])
	if len(missingSet) == 0 {
		return builder.String(), nil
	}
	missing := make([]string, 0, len(missingSet))
	for name := range missingSet {
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return builder.String(), fmt.Errorf("unresolved template variables: %s", strings.Join(missing, ", "))
}
