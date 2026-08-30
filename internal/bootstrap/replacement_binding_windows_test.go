//go:build windows

package bootstrap

import (
	"errors"

	"golang.org/x/sys/windows"
)

func replacementBindingDenied(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
