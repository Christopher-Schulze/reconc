//go:build darwin

package pathidentity

import (
	"bytes"
	"errors"
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
	return selectDarwinIdentity(evaluated, buffer, callErr), nil
}

func selectDarwinIdentity(evaluated string, buffer []byte, callErr error) string {
	evaluated = filepath.Clean(evaluated)
	if callErr != nil {
		return evaluated
	}
	length := bytes.IndexByte(buffer, 0)
	if length <= 0 {
		return evaluated
	}
	descriptorPath := filepath.Clean(string(buffer[:length]))
	if strings.EqualFold(evaluated, descriptorPath) {
		return descriptorPath
	}
	return evaluated
}

func existingAliasesOS(string) []string {
	return nil
}

func aliasComparisonKey(path string) string {
	return path
}
