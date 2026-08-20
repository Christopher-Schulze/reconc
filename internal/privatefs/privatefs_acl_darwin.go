//go:build darwin

package privatefs

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinAttributeBitmapCount = 5
	darwinAttributeBufferSize  = 8192
	darwinFileSecurityMagic    = 0x012cc16d
	darwinFileSecurityNoACL    = ^uint32(0)
)

type darwinAttributeList struct {
	BitmapCount uint16
	Reserved    uint16
	Common      uint32
	Volume      uint32
	Directory   uint32
	File        uint32
	Fork        uint32
}

func validatePrivateFileACL(file *os.File) error {
	return validateDarwinACL(uintptr(file.Fd()), nil, unix.SYS_FGETATTRLIST, 0)
}
func validatePrivateDirectoryACL(file *os.File) error {
	return validateDarwinACL(uintptr(file.Fd()), nil, unix.SYS_FGETATTRLIST, 0)
}

func validateDarwinACL(target uintptr, keepAlive *byte, trap, options uintptr) error {
	attributes := darwinAttributeList{BitmapCount: darwinAttributeBitmapCount, Common: unix.ATTR_CMN_EXTENDED_SECURITY}
	buffer := make([]byte, darwinAttributeBufferSize)
	_, _, callErr := unix.Syscall6(trap, target, uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), options, 0)
	runtime.KeepAlive(keepAlive)
	if callErr != 0 {
		return fmt.Errorf("inspect private filesystem ACL: %w", callErr)
	}
	if len(buffer) < 12 {
		return fmt.Errorf("private filesystem ACL response is truncated")
	}
	total := int(binary.LittleEndian.Uint32(buffer[:4]))
	dataOffset := int(int32(binary.LittleEndian.Uint32(buffer[4:8])))
	dataLength := int(binary.LittleEndian.Uint32(buffer[8:12]))
	dataStart := 4 + dataOffset
	if total < 12 || total > len(buffer) || dataOffset < 8 || dataStart < 12 || dataStart > total || dataLength > total-dataStart {
		return fmt.Errorf("private filesystem ACL response is invalid")
	}
	if dataLength == 0 {
		return nil
	}
	if dataLength < 44 {
		return fmt.Errorf("private filesystem security record is truncated")
	}
	data := buffer[dataStart : dataStart+dataLength]
	if binary.LittleEndian.Uint32(data[:4]) != darwinFileSecurityMagic {
		return fmt.Errorf("private filesystem security record is invalid")
	}
	if binary.LittleEndian.Uint32(data[36:40]) != darwinFileSecurityNoACL {
		return fmt.Errorf("private filesystem object must not have an extended ACL")
	}
	return nil
}
