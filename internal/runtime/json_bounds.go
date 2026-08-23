package runtime

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"io"
)

func validateBoundedJSON(data []byte, maxDepth, maxItems int) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	items := 0
	for {
		token, err := decoder.ReadToken()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if decoder.StackDepth() == 0 && decoder.InputOffset() > 0 && len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
				return fmt.Errorf("multiple JSON values are not allowed")
			}
			return err
		}
		if decoder.StackDepth() > maxDepth {
			return fmt.Errorf("JSON nesting exceeds %d levels", maxDepth)
		}
		switch token.Kind() {
		case jsontext.KindEndArray, jsontext.KindEndObject:
			continue
		default:
			items++
			if items > maxItems {
				return fmt.Errorf("JSON contains more than %d aggregate items", maxItems)
			}
		}
		if decoder.StackDepth() == 0 {
			if len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
				return fmt.Errorf("multiple JSON values are not allowed")
			}
			return nil
		}
	}
}
