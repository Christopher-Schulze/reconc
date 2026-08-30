package agentsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type singleJSONDiagnostics struct {
	decodePrefix   string
	multipleValues string
	trailingPrefix string
}

func decodeSingleJSONValue(body []byte, target interface{}, useNumber bool, diagnostics singleJSONDiagnostics) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if useNumber {
		decoder.UseNumber()
	}
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", diagnostics.decodePrefix, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New(diagnostics.multipleValues)
		}
		return fmt.Errorf("%s: %w", diagnostics.trailingPrefix, err)
	}
	return nil
}
