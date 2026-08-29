//go:build windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestReplaceFileUsesRootedWriteThroughHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalCreate := atomicNtCreateFile
	originalSetInformation := atomicNtSetInformationFile
	t.Cleanup(func() {
		atomicNtCreateFile = originalCreate
		atomicNtSetInformationFile = originalSetInformation
	})
	var access, attributes, options uint32
	var sourceName string
	var sourceRooted, destinationRooted bool
	var renameFlags uint32
	atomicNtCreateFile = func(
		handle *windows.Handle,
		desiredAccess uint32,
		objectAttributes *windows.OBJECT_ATTRIBUTES,
		status *windows.IO_STATUS_BLOCK,
		allocationSize *int64,
		fileAttributes uint32,
		share uint32,
		disposition uint32,
		createOptions uint32,
		extendedAttributes uintptr,
		extendedAttributesLength uint32,
	) error {
		access = desiredAccess
		attributes = objectAttributes.Attributes
		options = createOptions
		sourceName = objectAttributes.ObjectName.String()
		sourceRooted = objectAttributes.RootDirectory != 0
		return originalCreate(
			handle, desiredAccess, objectAttributes, status, allocationSize,
			fileAttributes, share, disposition, createOptions,
			extendedAttributes, extendedAttributesLength,
		)
	}
	atomicNtSetInformationFile = func(
		handle windows.Handle,
		status *windows.IO_STATUS_BLOCK,
		buffer *byte,
		bufferLength uint32,
		class uint32,
	) error {
		if class == fileRenameInformationEx {
			information := (*fileRenameInformationExBuffer)(unsafe.Pointer(buffer))
			destinationRooted = information.rootDirectory != 0
			renameFlags = information.flags
		}
		return originalSetInformation(handle, status, buffer, bufferLength, class)
	}
	written, err := WriteIfChanged(path, []byte("after\n"), 0o600)
	if err != nil || !written.Changed {
		t.Fatalf("write-through replacement: written=%v err=%v", written, err)
	}
	if !sourceRooted || filepath.IsAbs(sourceName) ||
		access&windows.DELETE == 0 ||
		attributes&windows.OBJ_DONT_REPARSE == 0 ||
		options&windows.FILE_WRITE_THROUGH == 0 ||
		options&windows.FILE_OPEN_REPARSE_POINT == 0 ||
		!destinationRooted ||
		renameFlags&windows.FILE_RENAME_REPLACE_IF_EXISTS == 0 ||
		renameFlags&windows.FILE_RENAME_POSIX_SEMANTICS == 0 {
		t.Fatalf(
			"native replacement source=%q access=%#x attributes=%#x options=%#x source_rooted=%v destination_rooted=%v flags=%#x",
			sourceName,
			access,
			attributes,
			options,
			sourceRooted,
			destinationRooted,
			renameFlags,
		)
	}
}

func TestReplaceFileFallsBackToLegacyRootedRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := atomicNtSetInformationFile
	t.Cleanup(func() { atomicNtSetInformationFile = original })
	classes := make([]uint32, 0, 2)
	atomicNtSetInformationFile = func(
		handle windows.Handle,
		status *windows.IO_STATUS_BLOCK,
		buffer *byte,
		bufferLength uint32,
		class uint32,
	) error {
		classes = append(classes, class)
		if class == fileRenameInformationEx {
			return windows.STATUS_INVALID_INFO_CLASS
		}
		return original(handle, status, buffer, bufferLength, class)
	}
	written, err := WriteIfChanged(path, []byte("after\n"), 0o600)
	if err != nil || !written.Changed {
		t.Fatalf("legacy rooted replacement: written=%v err=%v", written, err)
	}
	if len(classes) != 2 || classes[0] != fileRenameInformationEx ||
		classes[1] != windows.FileRenameInformation {
		t.Fatalf("rename information classes = %v", classes)
	}
}

func TestReplaceFileMapsNativeFailureAndCleansTemporary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := atomicNtSetInformationFile
	t.Cleanup(func() { atomicNtSetInformationFile = original })
	atomicNtSetInformationFile = func(
		windows.Handle,
		*windows.IO_STATUS_BLOCK,
		*byte,
		uint32,
		uint32,
	) error {
		return windows.STATUS_ACCESS_DENIED
	}
	written, err := WriteIfChanged(path, []byte("after\n"), 0o600)
	if err == nil || written.Changed || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("native replacement failure: written=%v err=%v", written, err)
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil || string(body) != "before\n" {
		t.Fatalf("failed replacement target = %q, %v", body, readErr)
	}
	temporaries, globErr := filepath.Glob(filepath.Join(directory, ".state.json.*.tmp"))
	if globErr != nil || len(temporaries) != 0 {
		t.Fatalf("failed replacement temporaries = %v, %v", temporaries, globErr)
	}
}

func TestReplaceFileRejectsReparseSourceAndCleansTemporary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	original := atomicGetFileInformationByHandle
	t.Cleanup(func() { atomicGetFileInformationByHandle = original })
	atomicGetFileInformationByHandle = func(
		handle windows.Handle,
		information *windows.ByHandleFileInformation,
	) error {
		if err := original(handle, information); err != nil {
			return err
		}
		information.FileAttributes |= windows.FILE_ATTRIBUTE_REPARSE_POINT
		return nil
	}
	written, err := WriteIfChanged(path, []byte("payload\n"), 0o600)
	if !errors.Is(err, errWindowsRenameSourceReparse) || written.Changed {
		t.Fatalf("reparse-source replacement: written=%v err=%v", written, err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("reparse-source target unexpectedly exists: %v", statErr)
	}
	temporaries, globErr := filepath.Glob(filepath.Join(directory, ".state.json.*.tmp"))
	if globErr != nil || len(temporaries) != 0 {
		t.Fatalf("reparse-source temporaries = %v, %v", temporaries, globErr)
	}
}
