//go:build darwin

package pathidentity

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

func resolveExistingOS(path string) (resolved string, err error) {
	evaluated, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(evaluated)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()

	buffer := make([]byte, unix.PathMax)
	_, callErr := unix.FcntlInt(
		file.Fd(),
		unix.F_GETPATH,
		int(uintptr(unsafe.Pointer(&buffer[0]))),
	)
	runtime.KeepAlive(buffer)
	if callErr != nil {
		return "", fmt.Errorf("resolve Darwin file-descriptor path: %w", callErr)
	}
	length := bytes.IndexByte(buffer, 0)
	if length <= 0 {
		return "", errors.New("resolve Darwin file-descriptor path: empty or unterminated response")
	}
	descriptorPath := filepath.Clean(string(buffer[:length]))
	if strings.EqualFold(evaluated, descriptorPath) {
		return descriptorPath, nil
	}
	return filepath.Clean(evaluated), nil
}

func existingAliasesOS(string) []string {
	return nil
}

func aliasComparisonKey(path string) string {
	return path
}
