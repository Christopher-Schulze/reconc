//go:build darwin

package actionstate

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
	if file == nil {
		return fmt.Errorf("private file handle is unavailable")
	}
	//lint:ignore SA1019 Direct syscall keeps release builds CGO-free while inspecting native ACL metadata.
	buffer, err := readDarwinFileSecurity(uintptr(file.Fd()), nil, unix.SYS_FGETATTRLIST, 0)
	if err != nil {
		return err
	}
	return rejectDarwinExtendedACL(buffer)
}

func validatePrivatePathACL(path string) error {
	pathPointer, err := unix.BytePtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode private path for ACL inspection: %w", err)
	}
	//lint:ignore SA1019 Direct syscall keeps release builds CGO-free while inspecting native ACL metadata.
	trap := uintptr(unix.SYS_GETATTRLIST)
	buffer, err := readDarwinFileSecurity(
		uintptr(unsafe.Pointer(pathPointer)), pathPointer, trap, unix.FSOPT_NOFOLLOW,
	)
	if err != nil {
		return err
	}
	return rejectDarwinExtendedACL(buffer)
}

func readDarwinFileSecurity(
	target uintptr,
	keepAlive *byte,
	trap uintptr,
	options uintptr,
) ([]byte, error) {
	attributes := darwinAttributeList{
		BitmapCount: darwinAttributeBitmapCount,
		Common:      unix.ATTR_CMN_EXTENDED_SECURITY,
	}
	buffer := make([]byte, darwinAttributeBufferSize)
	_, _, callErr := unix.Syscall6(
		trap,
		target,
		uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		options,
		0,
	)
	if callErr != 0 {
		return nil, fmt.Errorf("inspect private filesystem ACL: %w", callErr)
	}
	runtime.KeepAlive(keepAlive)
	return buffer, nil
}

func rejectDarwinExtendedACL(buffer []byte) error {
	const referenceOffset = 4
	if len(buffer) < referenceOffset+8 {
		return fmt.Errorf("private filesystem ACL response is truncated")
	}
	total := int(binary.LittleEndian.Uint32(buffer[:referenceOffset]))
	dataOffset := int(int32(binary.LittleEndian.Uint32(buffer[referenceOffset : referenceOffset+4])))
	dataLength := int(binary.LittleEndian.Uint32(buffer[referenceOffset+4 : referenceOffset+8]))
	dataStart := referenceOffset + dataOffset
	if total < referenceOffset+8 || total > len(buffer) || dataOffset < 8 ||
		dataStart < referenceOffset+8 || dataStart > total || dataLength > total-dataStart {
		return fmt.Errorf(
			"private filesystem ACL response is invalid: total=%d offset=%d length=%d",
			total, dataOffset, dataLength,
		)
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
	entryCount := binary.LittleEndian.Uint32(data[36:40])
	if entryCount != darwinFileSecurityNoACL {
		return fmt.Errorf("private filesystem object must not have an extended ACL")
	}
	return nil
}
