package hooks

import (
	"bytes"
	"encoding/json"
)

// marshalHostJSON renders a host configuration document indented and without
// HTML escaping, so shell operators in hook commands (`2>/dev/null`, `&&`)
// stay readable. Earlier releases wrote the same JSON with Go's default
// `\u003e`-style escapes; matchesGeneratedContent accepts that form.
func marshalHostJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// matchesGeneratedContent reports whether installed bytes equal the current
// generator output or, for JSON artifacts, the HTML-escaped encoding of the
// same document that earlier releases installed.
func matchesGeneratedContent(installed []byte, generated string, isJSON bool) bool {
	if string(installed) == generated {
		return true
	}
	if !isJSON {
		return false
	}
	var legacy bytes.Buffer
	json.HTMLEscape(&legacy, []byte(generated))
	return bytes.Equal(installed, legacy.Bytes())
}
