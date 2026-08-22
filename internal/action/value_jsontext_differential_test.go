package action

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestParseJSONMatchesEncodingJSONReference(t *testing.T) {
	t.Parallel()
	for _, body := range actionJSONDifferentialCorpus() {
		assertActionJSONDecoderParity(t, body)
	}
}

func FuzzParseJSONMatchesEncodingJSONReference(f *testing.F) {
	for _, seed := range actionJSONDifferentialCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		assertActionJSONDecoderParity(t, body)
	})
}

func FuzzValueMarshalJSONMatchesEncodingJSONEscaping(f *testing.F) {
	for _, seed := range []string{
		"plain ASCII",
		"quotes \" slash / backslash \\",
		"controls \x00\b\t\n\f\r\x1f",
		"HTML <tag>&value>",
		"JavaScript \u2028 and \u2029",
		"Unicode é e\u0301 世界 😀 \u007f",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, err := String(input)
		if err != nil {
			return
		}
		got, err := value.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("jsontext escaping drift for %q: got=%q want=%q", input, got, want)
		}
		size, err := value.CanonicalJSONSize()
		if err != nil || size != len(got) {
			t.Fatalf("canonical size drift for %q: size=%d bytes=%d err=%v", input, size, len(got), err)
		}
	})
}

func actionJSONDifferentialCorpus() [][]byte {
	acceptedDepth := bytes.Repeat([]byte{'['}, MaxJSONDepth)
	acceptedDepth = append(acceptedDepth, "null"...)
	acceptedDepth = append(acceptedDepth, bytes.Repeat([]byte{']'}, MaxJSONDepth)...)
	tooDeep := append([]byte{'['}, acceptedDepth...)
	tooDeep = append(tooDeep, ']')
	return [][]byte{
		[]byte(`null`),
		[]byte(`true`),
		[]byte(`-1200.00e-2`),
		[]byte(`"HTML <>& and JavaScript \u2028"`),
		[]byte(`{"z":10e-1,"a":[true,false,null,"\ud83d\ude80"]}`),
		[]byte(`{"a":1,"a":2}`),
		[]byte(`{"a":1,"\u0061":2}`),
		[]byte(`{} {}`),
		[]byte(`{"unterminated":`),
		[]byte{'"', 0xff, '"'},
		[]byte(`"\ud800"`),
		acceptedDepth,
		tooDeep,
	}
}

func assertActionJSONDecoderParity(tb testing.TB, body []byte) {
	tb.Helper()
	got, gotErr := ParseJSON(body)
	want, wantErr := parseJSONEncodingReference(body)
	if (gotErr == nil) != (wantErr == nil) {
		tb.Fatalf("decoder acceptance drift for %q: jsontext=%v encoding/json=%v", body, gotErr, wantErr)
	}
	if gotErr != nil {
		return
	}
	if !got.Equal(want) {
		tb.Fatalf("decoder value drift for %q: jsontext=%#v encoding/json=%#v", body, got, want)
	}
}

func parseJSONEncodingReference(data []byte) (Value, error) {
	if len(data) == 0 {
		return Value{}, errors.New("JSON value is empty")
	}
	if len(data) > MaxArgumentBytes {
		return Value{}, fmt.Errorf("JSON value exceeds %d bytes", MaxArgumentBytes)
	}
	if err := ValidateJSONUnicode(data); err != nil {
		return Value{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	state := referenceValueDecodeState{}
	value, err := state.read(decoder, 0)
	if err != nil {
		return Value{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Value{}, errors.New("JSON value contains trailing data")
		}
		return Value{}, err
	}
	return value, nil
}

type referenceValueDecodeState struct {
	items int
}

func (s *referenceValueDecodeState) read(decoder *json.Decoder, depth int) (Value, error) {
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
		return s.readContainer(decoder, depth, value)
	default:
		return Value{}, fmt.Errorf("unsupported JSON token %T", token)
	}
}

func (s *referenceValueDecodeState) readContainer(decoder *json.Decoder, depth int, delimiter json.Delim) (Value, error) {
	if delimiter == '[' {
		values := []Value{}
		for decoder.More() {
			s.items++
			if s.items > MaxJSONItems {
				return Value{}, errors.New("JSON item limit exceeded")
			}
			item, err := s.read(decoder, depth+1)
			if err != nil {
				return Value{}, err
			}
			values = append(values, item)
		}
		if err := expectReferenceDelimiter(decoder, ']'); err != nil {
			return Value{}, err
		}
		return Array(values)
	}
	if delimiter != '{' {
		return Value{}, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
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
			return Value{}, errors.New("JSON item limit exceeded")
		}
		memberValue, err := s.read(decoder, depth+1)
		if err != nil {
			return Value{}, err
		}
		members = append(members, Member{Name: name, Value: memberValue})
	}
	if err := expectReferenceDelimiter(decoder, '}'); err != nil {
		return Value{}, err
	}
	return Object(members)
}

func expectReferenceDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("JSON container closed by %q, want %q", token, want)
	}
	return nil
}
