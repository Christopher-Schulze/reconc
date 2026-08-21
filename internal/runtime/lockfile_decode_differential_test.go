package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestDecodeStrictLockfileJSONMatchesTwoPassReference(t *testing.T) {
	t.Parallel()
	for _, body := range strictLockfileJSONCorpus() {
		assertStrictLockfileJSONParity(t, body)
	}
}

func FuzzDecodeStrictLockfileJSONParity(f *testing.F) {
	for _, seed := range strictLockfileJSONCorpus() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		assertStrictLockfileJSONParity(t, body)
	})
}

func assertStrictLockfileJSONParity(t testing.TB, body []byte) {
	t.Helper()
	got, gotErr := decodeStrictLockfileJSON(body)
	want, wantErr := decodeStrictLockfileJSONReference(body)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("strict decoder acceptance drift for %q: got=%v want=%v", body, gotErr, wantErr)
	}
	if gotErr == nil && !reflect.DeepEqual(got, want) {
		t.Fatalf("strict decoder value drift for %q:\ngot=%#v\nwant=%#v", body, got, want)
	}
}

func strictLockfileJSONCorpus() [][]byte {
	deep := []byte(`{"root":0}`)
	for range maxLockfileJSONDepth - 2 {
		deep = append([]byte(`{"nested":`), append(deep, '}')...)
	}
	tooDeep := append([]byte(`{"nested":`), append(deep, '}')...)
	return [][]byte{
		[]byte(`{}`),
		[]byte(`{"integer":9007199254740993,"decimal":1.25,"exponent":1e3}`),
		[]byte(`{"object":{"array":[true,false,null,"text","\ud83d\ude80"]}}`),
		[]byte(`{"duplicate":1,"duplicate":2}`),
		[]byte(`{"nested":{"duplicate":1,"duplicate":2}}`),
		[]byte(`[]`),
		[]byte(`null`),
		[]byte(`{} {}`),
		[]byte(`{"unterminated":`),
		[]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
		[]byte(`{"surrogate":"\ud800"}`),
		deep,
		tooDeep,
	}
}

// decodeStrictLockfileJSONReference intentionally preserves the former
// validate-then-decode ownership as an independent differential oracle. It is
// test-only because production must retain the single-pass decoder.
func decodeStrictLockfileJSONReference(data []byte) (map[string]interface{}, error) {
	if err := action.ValidateJSONUnicode(data); err != nil {
		return nil, err
	}
	validator := json.NewDecoder(bytes.NewReader(data))
	validator.UseNumber()
	first, err := validator.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("root value must be an object")
	}
	if err := validateStrictJSONReferenceContainer(validator, '{', 1); err != nil {
		return nil, err
	}
	if _, err := validator.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload map[string]interface{}
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, errors.New("root value must be an object")
	}
	if err := decoder.Decode(new(interface{})); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return payload, nil
}

func validateStrictJSONReferenceContainer(decoder *json.Decoder, delimiter json.Delim, depth int) error {
	if depth > maxLockfileJSONDepth {
		return errors.New("JSON nesting exceeds the lockfile limit")
	}
	seen := map[string]struct{}{}
	for decoder.More() {
		if delimiter == '{' {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
		}
		if depth+1 > maxLockfileJSONDepth {
			return errors.New("JSON nesting exceeds the lockfile limit")
		}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if nested, ok := value.(json.Delim); ok {
			if nested != '{' && nested != '[' {
				return errors.New("unexpected JSON delimiter")
			}
			if err := validateStrictJSONReferenceContainer(decoder, nested, depth+1); err != nil {
				return err
			}
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return errors.New("JSON container has the wrong closing delimiter")
	}
	return nil
}
