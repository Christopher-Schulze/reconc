package action

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type ValueKind string

const (
	ValueNull   ValueKind = "null"
	ValueBool   ValueKind = "boolean"
	ValueNumber ValueKind = "number"
	ValueString ValueKind = "string"
	ValueArray  ValueKind = "array"
	ValueObject ValueKind = "object"
)

type Decimal struct {
	negative    bool
	coefficient string
	exponent    int
}

var jsonNumberPattern = regexpMustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

func ParseDecimal(lexeme string) (Decimal, error) {
	if len(lexeme) == 0 || len(lexeme) > MaxNumberLexemeBytes {
		return Decimal{}, fmt.Errorf("number lexeme must contain 1 to %d bytes", MaxNumberLexemeBytes)
	}
	parts := jsonNumberPattern.FindStringSubmatch(lexeme)
	if parts == nil {
		return Decimal{}, fmt.Errorf("number %q is not valid JSON decimal syntax", lexeme)
	}
	exponent := int64(0)
	if parts[4] != "" {
		parsed, err := strconv.ParseInt(parts[4], 10, 32)
		if err != nil {
			return Decimal{}, fmt.Errorf("number exponent is outside the supported range")
		}
		exponent = parsed
	}
	coefficient := strings.TrimLeft(parts[2]+parts[3], "0")
	if coefficient == "" {
		return Decimal{coefficient: "0"}, nil
	}
	exponent -= int64(len(parts[3]))
	for strings.HasSuffix(coefficient, "0") {
		coefficient = strings.TrimSuffix(coefficient, "0")
		exponent++
	}
	if len(coefficient) > MaxNumberDigits {
		return Decimal{}, fmt.Errorf("number has %d significant digits; maximum is %d", len(coefficient), MaxNumberDigits)
	}
	if exponent < -MaxNumberExponent || exponent > MaxNumberExponent {
		return Decimal{}, fmt.Errorf("normalized number exponent %d exceeds %d", exponent, MaxNumberExponent)
	}
	return Decimal{
		negative: parts[1] == "-", coefficient: coefficient, exponent: int(exponent),
	}, nil
}

func (d Decimal) String() string {
	coefficient := d.coefficient
	if coefficient == "" || coefficient == "0" {
		return "0"
	}
	prefix := ""
	if d.negative {
		prefix = "-"
	}
	if d.exponent == 0 {
		return prefix + coefficient
	}
	return prefix + coefficient + "e" + strconv.Itoa(d.exponent)
}

func (d Decimal) Equal(other Decimal) bool {
	return d.negative == other.negative && d.coefficient == other.coefficient && d.exponent == other.exponent
}

type Member struct {
	Name  string
	Value Value
}

// Value is a closed canonical JSON value. Its representation cannot contain
// arbitrary Go maps, floats, duplicate object keys, or non-normalized numbers.
type Value struct {
	kind   ValueKind
	bool   bool
	number Decimal
	string string
	array  []Value
	object []Member
}

func Null() Value                { return Value{kind: ValueNull} }
func Boolean(value bool) Value   { return Value{kind: ValueBool, bool: value} }
func Number(value Decimal) Value { return Value{kind: ValueNumber, number: value} }
func String(value string) (Value, error) {
	if !utf8.ValidString(value) || len(value) > MaxJSONStringBytes {
		return Value{}, fmt.Errorf("string must be valid UTF-8 and at most %d bytes", MaxJSONStringBytes)
	}
	return Value{kind: ValueString, string: value}, nil
}

func Array(values []Value) (Value, error) {
	if len(values) > MaxJSONItems {
		return Value{}, fmt.Errorf("array contains %d items; maximum is %d", len(values), MaxJSONItems)
	}
	return Value{kind: ValueArray, array: append([]Value(nil), values...)}, nil
}

func Object(members []Member) (Value, error) {
	if len(members) > MaxJSONItems {
		return Value{}, fmt.Errorf("object contains %d keys; maximum is %d", len(members), MaxJSONItems)
	}
	out := append([]Member(nil), members...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for index := range out {
		if !utf8.ValidString(out[index].Name) || len(out[index].Name) > MaxJSONStringBytes {
			return Value{}, fmt.Errorf("object key must be valid UTF-8 and at most %d bytes", MaxJSONStringBytes)
		}
		if index > 0 && out[index-1].Name == out[index].Name {
			return Value{}, fmt.Errorf("duplicate object key %q", out[index].Name)
		}
	}
	return Value{kind: ValueObject, object: out}, nil
}

func (v Value) Kind() ValueKind { return v.kind }

func (v Value) Bool() (bool, bool) {
	return v.bool, v.kind == ValueBool
}

func (v Value) Decimal() (Decimal, bool) {
	return v.number, v.kind == ValueNumber
}

func (v Value) Text() (string, bool) {
	return v.string, v.kind == ValueString
}

func (v Value) Items() ([]Value, bool) {
	if v.kind != ValueArray {
		return nil, false
	}
	return append([]Value(nil), v.array...), true
}

func (v Value) Members() ([]Member, bool) {
	if v.kind != ValueObject {
		return nil, false
	}
	return append([]Member(nil), v.object...), true
}

func (v Value) Lookup(name string) (Value, bool) {
	if v.kind != ValueObject {
		return Value{}, false
	}
	index := sort.Search(len(v.object), func(index int) bool { return v.object[index].Name >= name })
	if index == len(v.object) || v.object[index].Name != name {
		return Value{}, false
	}
	return v.object[index].Value, true
}

func (v Value) Scalar() bool {
	return v.kind == ValueBool || v.kind == ValueNumber || v.kind == ValueString
}

func (v Value) Equal(other Value) bool {
	left, err := v.MarshalJSON()
	if err != nil {
		return false
	}
	right, err := other.MarshalJSON()
	return err == nil && bytes.Equal(left, right)
}

func (v Value) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	if err := v.appendJSON(&out, 0); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (v Value) appendJSON(out *bytes.Buffer, depth int) error {
	if depth > MaxJSONDepth {
		return fmt.Errorf("value exceeds %d container levels", MaxJSONDepth)
	}
	switch v.kind {
	case ValueNull:
		out.WriteString("null")
	case ValueBool:
		if v.bool {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case ValueNumber:
		out.WriteString(v.number.String())
	case ValueString:
		encoded, err := json.Marshal(v.string)
		if err != nil {
			return err
		}
		out.Write(encoded)
	case ValueArray:
		out.WriteByte('[')
		for index := range v.array {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := v.array[index].appendJSON(out, depth+1); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case ValueObject:
		out.WriteByte('{')
		for index := range v.object {
			if index > 0 {
				out.WriteByte(',')
			}
			name, err := json.Marshal(v.object[index].Name)
			if err != nil {
				return err
			}
			out.Write(name)
			out.WriteByte(':')
			if err := v.object[index].Value.appendJSON(out, depth+1); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("value kind %q is invalid", v.kind)
	}
	return nil
}

func (v *Value) UnmarshalJSON(data []byte) error {
	parsed, err := ParseJSON(data)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

func ParseJSON(data []byte) (Value, error) {
	if len(data) == 0 || len(data) > MaxArgumentBytes {
		return Value{}, fmt.Errorf("JSON value must contain 1 to %d bytes", MaxArgumentBytes)
	}
	if err := ValidateJSONUnicode(data); err != nil {
		return Value{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	state := valueDecodeState{}
	value, err := state.read(decoder, 0)
	if err != nil {
		return Value{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Value{}, errors.New("JSON value contains trailing data")
		}
		return Value{}, fmt.Errorf("read trailing JSON: %w", err)
	}
	return value, nil
}

func ParseObjectJSON(data []byte) (Value, error) {
	value, err := ParseJSON(data)
	if err != nil {
		return Value{}, err
	}
	if value.kind != ValueObject {
		return Value{}, errors.New("JSON value must contain one object")
	}
	return value, nil
}

type valueDecodeState struct {
	items int
}

func (s *valueDecodeState) read(decoder *json.Decoder, depth int) (Value, error) {
	if depth > MaxJSONDepth {
		return Value{}, fmt.Errorf("JSON value exceeds %d container levels", MaxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return Value{}, err
	}
	switch value := token.(type) {
	case nil:
		return Null(), nil
	case bool:
		return Boolean(value), nil
	case string:
		return String(value)
	case json.Number:
		decimal, err := ParseDecimal(string(value))
		if err != nil {
			return Value{}, err
		}
		return Number(decimal), nil
	case json.Delim:
		switch value {
		case '[':
			values := []Value{}
			for decoder.More() {
				s.items++
				if s.items > MaxJSONItems {
					return Value{}, fmt.Errorf("JSON value exceeds %d object keys plus array items", MaxJSONItems)
				}
				item, err := s.read(decoder, depth+1)
				if err != nil {
					return Value{}, err
				}
				values = append(values, item)
			}
			if err := expectDelimiter(decoder, ']'); err != nil {
				return Value{}, err
			}
			return Array(values)
		case '{':
			members := []Member{}
			seen := map[string]struct{}{}
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return Value{}, err
				}
				name, ok := nameToken.(string)
				if !ok {
					return Value{}, errors.New("JSON object key is not a string")
				}
				if _, duplicate := seen[name]; duplicate {
					return Value{}, fmt.Errorf("duplicate object key %q", name)
				}
				seen[name] = struct{}{}
				s.items++
				if s.items > MaxJSONItems {
					return Value{}, fmt.Errorf("JSON value exceeds %d object keys plus array items", MaxJSONItems)
				}
				memberValue, err := s.read(decoder, depth+1)
				if err != nil {
					return Value{}, err
				}
				members = append(members, Member{Name: name, Value: memberValue})
			}
			if err := expectDelimiter(decoder, '}'); err != nil {
				return Value{}, err
			}
			return Object(members)
		default:
			return Value{}, fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	default:
		return Value{}, fmt.Errorf("unsupported JSON token %T", token)
	}
}

func expectDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("JSON container closed by %q, want %q", token, want)
	}
	return nil
}

// ValidateJSONUnicode rejects byte sequences and escaped surrogate forms that
// encoding/json would otherwise replace with U+FFFD. Callers can then apply
// their own JSON shape and resource contracts without losing source identity.
func ValidateJSONUnicode(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("JSON value contains invalid UTF-8")
	}
	for index := 0; index < len(data); index++ {
		if data[index] != '"' {
			continue
		}
		index++
		for index < len(data) && data[index] != '"' {
			if data[index] != '\\' {
				index++
				continue
			}
			index++
			if index >= len(data) {
				return errors.New("JSON string ends after an escape prefix")
			}
			if data[index] != 'u' {
				index++
				continue
			}
			code, next, err := readUnicodeEscape(data, index)
			if err != nil {
				return err
			}
			index = next
			if utf16.IsSurrogate(rune(code)) {
				if code < 0xD800 || code > 0xDBFF {
					return errors.New("JSON string contains an unpaired low surrogate")
				}
				if index+2 >= len(data) || data[index] != '\\' || data[index+1] != 'u' {
					return errors.New("JSON string contains an unpaired high surrogate")
				}
				low, afterLow, err := readUnicodeEscape(data, index+1)
				if err != nil || low < 0xDC00 || low > 0xDFFF {
					return errors.New("JSON string contains an unpaired high surrogate")
				}
				index = afterLow
			}
		}
		if index >= len(data) {
			return errors.New("JSON string is not terminated")
		}
	}
	return nil
}

func readUnicodeEscape(data []byte, uIndex int) (uint16, int, error) {
	if uIndex+4 >= len(data) {
		return 0, 0, errors.New("JSON Unicode escape is truncated")
	}
	value, err := strconv.ParseUint(string(data[uIndex+1:uIndex+5]), 16, 16)
	if err != nil {
		return 0, 0, errors.New("JSON Unicode escape is invalid")
	}
	return uint16(value), uIndex + 5, nil
}

// regexpMustCompile is isolated here so the public type file need not own
// parser-only globals.
func regexpMustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}
