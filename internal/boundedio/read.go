// Package boundedio provides exact-size file reads for untrusted or
// repository-controlled inputs.
package boundedio

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ReadFile reads at most maxBytes and reports a stable error instead of
// allocating the complete contents of an oversized file.
func ReadFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("bounded file read requires a positive byte limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return body, nil
}
