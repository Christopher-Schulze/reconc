package action

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
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

type JSONErrorKind string

const (
	JSONErrorInvalid      JSONErrorKind = "invalid"
	JSONErrorInvalidUTF8  JSONErrorKind = "invalid_utf8"
	JSONErrorDuplicateKey JSONErrorKind = "duplicate_key"
	JSONErrorLimit        JSONErrorKind = "limit_exceeded"
)

type JSONValueError struct {
	Kind  JSONErrorKind
	Cause error
}

func (e *JSONValueError) Error() string {
	if e == nil || e.Cause == nil {
		return "invalid JSON value"
	}
	return e.Cause.Error()
}

func (e *JSONValueError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func JSONErrorKindOf(err error) JSONErrorKind {
	var valueError *JSONValueError
	if errors.As(err, &valueError) {
		return valueError.Kind
	}
	return JSONErrorInvalid
}

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
	if d.coefficient == "" || d.coefficient == "0" {
		d = Decimal{coefficient: "0"}
	}
	if other.coefficient == "" || other.coefficient == "0" {
		other = Decimal{coefficient: "0"}
	}
	return d.negative == other.negative && d.coefficient == other.coefficient && d.exponent == other.exponent
}

// Compare returns -1, 0, or 1 when d is less than, equal to, or greater than
// other. Decimal comparison never converts through a floating-point value.
func (d Decimal) Compare(other Decimal) int {
	if d.Equal(other) {
		return 0
	}
	if d.coefficient == "" {
		d.coefficient = "0"
	}
	if other.coefficient == "" {
		other.coefficient = "0"
	}
	if d.coefficient == "0" {
		if other.negative {
			return 1
		}
		return -1
	}
	if other.coefficient == "0" {
		if d.negative {
			return -1
		}
		return 1
	}
	if d.negative != other.negative {
		if d.negative {
			return -1
		}
		return 1
	}
	comparison := compareDecimalMagnitude(d, other)
	if d.negative {
		return -comparison
	}
	return comparison
}

func compareDecimalMagnitude(left, right Decimal) int {
	leftMagnitude := len(left.coefficient) + left.exponent
	rightMagnitude := len(right.coefficient) + right.exponent
	if leftMagnitude < rightMagnitude {
		return -1
	}
	if leftMagnitude > rightMagnitude {
		return 1
	}
	return compareDecimalDigits(left.coefficient, right.coefficient)
}

func compareDecimalDigits(left, right string) int {
	width := max(len(left), len(right))
	for index := 0; index < width; index++ {
		leftDigit := byte('0')
		if index < len(left) {
			leftDigit = left[index]
		}
		rightDigit := byte('0')
		if index < len(right) {
			rightDigit = right[index]
		}
		if leftDigit < rightDigit {
			return -1
		}
		if leftDigit > rightDigit {
			return 1
		}
	}
	return 0
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

func Null() Value              { return Value{kind: ValueNull} }
func Boolean(value bool) Value { return Value{kind: ValueBool, bool: value} }
func Number(value Decimal) Value {
	if value.coefficient == "" || value.coefficient == "0" {
		value = Decimal{coefficient: "0"}
	}
	return Value{kind: ValueNumber, number: value}
}
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

// ArrayLen returns the number of array items without exposing mutable backing
// storage. Callers use ArrayItem for allocation-free read-only traversal.
func (v Value) ArrayLen() (int, bool) {
	return len(v.array), v.kind == ValueArray
}

// ArrayItem returns one array item by value. Nested collection storage remains
// encapsulated by Value's defensive collection accessors.
func (v Value) ArrayItem(index int) (Value, bool) {
	if v.kind != ValueArray || index < 0 || index >= len(v.array) {
		return Value{}, false
	}
	return v.array[index], true
}

func (v Value) Members() ([]Member, bool) {
	if v.kind != ValueObject {
		return nil, false
	}
	return append([]Member(nil), v.object...), true
}

// ObjectLen returns the number of object members without cloning them.
func (v Value) ObjectLen() (int, bool) {
	return len(v.object), v.kind == ValueObject
}

// ObjectMember returns one sorted object member by value for allocation-free
// read-only traversal.
func (v Value) ObjectMember(index int) (Member, bool) {
	if v.kind != ValueObject || index < 0 || index >= len(v.object) {
		return Member{}, false
	}
	return v.object[index], true
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
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case ValueNull:
		return true
	case ValueBool:
		return v.bool == other.bool
	case ValueNumber:
		return v.number.Equal(other.number)
	case ValueString:
		return v.string == other.string
	case ValueArray:
		if len(v.array) != len(other.array) {
			return false
		}
		for index := range v.array {
			if !v.array[index].Equal(other.array[index]) {
				return false
			}
		}
		return true
	case ValueObject:
		if len(v.object) != len(other.object) {
			return false
		}
		for index := range v.object {
			if v.object[index].Name != other.object[index].Name ||
				!v.object[index].Value.Equal(other.object[index].Value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (v Value) MarshalJSON() ([]byte, error) {
	sizeHint, err := v.jsonSizeHint(0)
	if err != nil {
		return nil, err
	}
	return v.appendJSON(make([]byte, 0, sizeHint), 0)
}

func (v Value) jsonSizeHint(depth int) (int, error) {
	if depth > MaxJSONDepth {
		return 0, fmt.Errorf("value exceeds %d container levels", MaxJSONDepth)
	}
	switch v.kind {
	case ValueNull:
		return len("null"), nil
	case ValueBool:
		if v.bool {
			return len("true"), nil
		}
		return len("false"), nil
	case ValueNumber:
		return len(v.number.String()), nil
	case ValueString:
		return addJSONSize(len(v.string), 2)
	case ValueArray:
		return v.jsonArraySizeHint(depth)
	case ValueObject:
		return v.jsonObjectSizeHint(depth)
	default:
		return 0, fmt.Errorf("value kind %q is invalid", v.kind)
	}
}

func (v Value) jsonArraySizeHint(depth int) (int, error) {
	total := 2
	for index := range v.array {
		if index > 0 {
			var err error
			total, err = addJSONSize(total, 1)
			if err != nil {
				return 0, err
			}
		}
		itemSize, err := v.array[index].jsonSizeHint(depth + 1)
		if err != nil {
			return 0, err
		}
		total, err = addJSONSize(total, itemSize)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func (v Value) jsonObjectSizeHint(depth int) (int, error) {
	total := 2
	for index := range v.object {
		if index > 0 {
			var err error
			total, err = addJSONSize(total, 1)
			if err != nil {
				return 0, err
			}
		}
		nameSize, err := addJSONSize(len(v.object[index].Name), 2)
		if err != nil {
			return 0, err
		}
		nameSize, err = addJSONSize(nameSize, 1)
		if err != nil {
			return 0, err
		}
		total, err = addJSONSize(total, nameSize)
		if err != nil {
			return 0, err
		}
		valueSize, err := v.object[index].Value.jsonSizeHint(depth + 1)
		if err != nil {
			return 0, err
		}
		total, err = addJSONSize(total, valueSize)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func addJSONSize(total, increment int) (int, error) {
	if increment < 0 || total > int(^uint(0)>>1)-increment {
		return 0, errors.New("canonical JSON size exceeds addressable memory")
	}
	return total + increment, nil
}

func (v Value) appendJSON(out []byte, depth int) ([]byte, error) {
	if depth > MaxJSONDepth {
		return nil, fmt.Errorf("value exceeds %d container levels", MaxJSONDepth)
	}
	switch v.kind {
	case ValueNull:
		return append(out, "null"...), nil
	case ValueBool:
		if v.bool {
			return append(out, "true"...), nil
		}
		return append(out, "false"...), nil
	case ValueNumber:
		return append(out, v.number.String()...), nil
	case ValueString:
		return appendJSONString(out, v.string)
	case ValueArray:
		return v.appendJSONArray(out, depth)
	case ValueObject:
		return v.appendJSONObject(out, depth)
	default:
		return nil, fmt.Errorf("value kind %q is invalid", v.kind)
	}
}

func (v Value) appendJSONArray(out []byte, depth int) ([]byte, error) {
	out = append(out, '[')
	for index := range v.array {
		if index > 0 {
			out = append(out, ',')
		}
		var err error
		out, err = v.array[index].appendJSON(out, depth+1)
		if err != nil {
			return nil, err
		}
	}
	return append(out, ']'), nil
}

func (v Value) appendJSONObject(out []byte, depth int) ([]byte, error) {
	out = append(out, '{')
	for index := range v.object {
		if index > 0 {
			out = append(out, ',')
		}
		var err error
		out, err = appendJSONString(out, v.object[index].Name)
		if err != nil {
			return nil, err
		}
		out = append(out, ':')
		out, err = v.object[index].Value.appendJSON(out, depth+1)
		if err != nil {
			return nil, err
		}
	}
	return append(out, '}'), nil
}

func appendJSONString(out []byte, value string) ([]byte, error) {
	if strings.ContainsAny(value, "<>&") || strings.ContainsRune(value, '\u2028') || strings.ContainsRune(value, '\u2029') {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return append(out, encoded...), nil
	}
	return jsontext.AppendQuote(out, value)
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
	if len(data) == 0 {
		return Value{}, jsonValueError(JSONErrorInvalid, errors.New("JSON value is empty"))
	}
	if len(data) > MaxArgumentBytes {
		return Value{}, jsonValueError(JSONErrorLimit, fmt.Errorf("JSON value must contain 1 to %d bytes", MaxArgumentBytes))
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	state := valueDecodeState{}
	value, err := state.read(decoder, 0)
	if err != nil {
		return Value{}, classifyJSONValueError(data, err)
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Value{}, jsonValueError(JSONErrorInvalid, errors.New("JSON value contains trailing data"))
		}
		return Value{}, classifyJSONValueError(data, fmt.Errorf("read trailing JSON: %w", err))
	}
	return value, nil
}

func ParseObjectJSON(data []byte) (Value, error) {
	value, err := ParseJSON(data)
	if err != nil {
		return Value{}, err
	}
	if value.kind != ValueObject {
		return Value{}, jsonValueError(JSONErrorInvalid, errors.New("JSON value must contain one object"))
	}
	return value, nil
}

func jsonValueError(kind JSONErrorKind, cause error) error {
	return &JSONValueError{Kind: kind, Cause: cause}
}

func classifyJSONValueError(data []byte, err error) error {
	if errors.Is(err, jsontext.ErrDuplicateName) {
		return jsonValueError(JSONErrorDuplicateKey, err)
	}
	if unicodeErr := ValidateJSONUnicode(data); unicodeErr != nil {
		return jsonValueError(JSONErrorInvalidUTF8, unicodeErr)
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "maximum"), strings.Contains(message, "exceeds"),
		strings.Contains(message, "at most"), strings.Contains(message, "must contain 1 to"),
		strings.Contains(message, "outside the supported range"):
		return jsonValueError(JSONErrorLimit, err)
	default:
		return jsonValueError(JSONErrorInvalid, err)
	}
}

type valueDecodeState struct {
	items int
}

func (s *valueDecodeState) read(decoder *jsontext.Decoder, depth int) (Value, error) {
	if depth > MaxJSONDepth {
		return Value{}, fmt.Errorf("JSON value exceeds %d container levels", MaxJSONDepth)
	}
	token, err := decoder.ReadToken()
	if err != nil {
		return Value{}, err
	}
	switch token.Kind() {
	case jsontext.KindNull:
		return Null(), nil
	case jsontext.KindFalse:
		return Boolean(false), nil
	case jsontext.KindTrue:
		return Boolean(true), nil
	case jsontext.KindString:
		return String(token.String())
	case jsontext.KindNumber:
		decimal, err := ParseDecimal(token.String())
		if err != nil {
			return Value{}, err
		}
		return Number(decimal), nil
	case jsontext.KindBeginArray:
		return s.readArray(decoder, depth)
	case jsontext.KindBeginObject:
		return s.readObject(decoder, depth)
	default:
		return Value{}, fmt.Errorf("unsupported JSON token %q", token.Kind())
	}
}

func (s *valueDecodeState) readArray(decoder *jsontext.Decoder, depth int) (Value, error) {
	values := []Value{}
	for decoder.PeekKind() != jsontext.KindEndArray {
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
	closing, err := decoder.ReadToken()
	if err != nil {
		return Value{}, err
	}
	if closing.Kind() != jsontext.KindEndArray {
		return Value{}, fmt.Errorf("JSON array closed by %q", closing.Kind())
	}
	return Array(values)
}

func (s *valueDecodeState) readObject(decoder *jsontext.Decoder, depth int) (Value, error) {
	members := []Member{}
	for decoder.PeekKind() != jsontext.KindEndObject {
		nameToken, err := decoder.ReadToken()
		if err != nil {
			return Value{}, err
		}
		if nameToken.Kind() != jsontext.KindString {
			return Value{}, errors.New("JSON object key is not a string")
		}
		name := nameToken.String()
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
	closing, err := decoder.ReadToken()
	if err != nil {
		return Value{}, err
	}
	if closing.Kind() != jsontext.KindEndObject {
		return Value{}, fmt.Errorf("JSON object closed by %q", closing.Kind())
	}
	return Object(members)
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
