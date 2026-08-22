package bootstrap

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"reconc.dev/reconc/internal/boundedio"
)

func decodeStrictJSONSnapshot(path, label string, maxBytes int64, target any) error {
	return boundedio.WithRegularFileSnapshot(path, maxBytes, func(file *os.File, _ os.FileInfo) error {
		decoder := json.NewDecoder(io.LimitReader(file, maxBytes+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return fmt.Errorf("decode %s: %w", label, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fmt.Errorf("%s must contain exactly one JSON document", label)
		}
		return nil
	})
}
