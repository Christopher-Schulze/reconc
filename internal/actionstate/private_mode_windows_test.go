//go:build windows

package actionstate

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateWindowsDirectoryAttributesRejectReparsePoints(t *testing.T) {
	tests := []struct {
		name       string
		attributes uint32
		valid      bool
	}{
		{name: "directory", attributes: windows.FILE_ATTRIBUTE_DIRECTORY, valid: true},
		{name: "regular file", attributes: windows.FILE_ATTRIBUTE_NORMAL},
		{name: "junction", attributes: windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePrivateWindowsDirectoryAttributes(test.attributes)
			if (err == nil) != test.valid {
				t.Fatalf("validatePrivateWindowsDirectoryAttributes(%#x) = %v", test.attributes, err)
			}
		})
	}
}
