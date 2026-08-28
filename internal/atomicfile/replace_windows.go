//go:build windows

package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileRenameInformationEx = 65

type fileRenameInformation struct {
	replaceIfExists byte
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [1]uint16
}

type fileRenameInformationExBuffer struct {
	flags          uint32
	rootDirectory  windows.Handle
	fileNameLength uint32
	fileName       [1]uint16
}

var (
	errWindowsRenameSourceReparse    = errors.New("temporary file is a reparse point")
	atomicGetFileInformationByHandle = windows.GetFileInformationByHandle
	atomicNtCreateFile               = windows.NtCreateFile
	atomicNtSetInformationFile       = windows.NtSetInformationFile
)

func replaceFile(directory *os.Root, source, destination string) (resultErr error) {
	if directory == nil || !filepath.IsLocal(source) || !filepath.IsLocal(destination) {
		return &os.LinkError{Op: "renameat-write-through", Old: source, New: destination, Err: fs.ErrInvalid}
	}
	directoryFile, err := directory.Open(".")
	if err != nil {
		return &os.LinkError{Op: "renameat-write-through", Old: source, New: destination, Err: err}
	}
	defer func() { resultErr = errors.Join(resultErr, directoryFile.Close()) }()
	directoryHandle := windows.Handle(directoryFile.Fd())
	sourceHandle, err := openWriteThroughRenameSource(directoryHandle, source)
	if err != nil {
		return &os.LinkError{
			Op: "renameat-write-through", Old: source, New: destination,
			Err: windowsRenameError(err),
		}
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(sourceHandle)) }()
	if err := setWriteThroughRename(sourceHandle, directoryHandle, destination); err != nil {
		return &os.LinkError{
			Op: "renameat-write-through", Old: source, New: destination,
			Err: windowsRenameError(err),
		}
	}
	return nil
}

func openWriteThroughRenameSource(directory windows.Handle, source string) (windows.Handle, error) {
	name, err := windows.NewNTUnicodeString(source)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: directory,
		ObjectName:    name,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	err = atomicNtCreateFile(
		&handle,
		windows.SYNCHRONIZE|windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		&attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|
			windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT|
			windows.FILE_WRITE_THROUGH,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, err
	}
	var information windows.ByHandleFileInformation
	if err := atomicGetFileInformationByHandle(handle, &information); err != nil {
		return windows.InvalidHandle, errors.Join(err, windows.CloseHandle(handle))
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return windows.InvalidHandle, errors.Join(
			errWindowsRenameSourceReparse,
			windows.CloseHandle(handle),
		)
	}
	return handle, nil
}

func setWriteThroughRename(source, directory windows.Handle, destination string) error {
	name, err := windows.UTF16FromString(destination)
	if err != nil {
		return err
	}
	name = name[:len(name)-1]
	extendedLayout := fileRenameInformationExBuffer{}
	extendedBytes := max(
		int(unsafe.Sizeof(extendedLayout)),
		int(unsafe.Offsetof(extendedLayout.fileName))+len(name)*2,
	)
	extendedBuffer := make([]byte, extendedBytes)
	extended := (*fileRenameInformationExBuffer)(unsafe.Pointer(&extendedBuffer[0]))
	extended.flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	extended.rootDirectory = directory
	extended.fileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&extended.fileName[0], len(name)), name)
	err = atomicNtSetInformationFile(
		source,
		&windows.IO_STATUS_BLOCK{},
		&extendedBuffer[0],
		uint32(len(extendedBuffer)),
		fileRenameInformationEx,
	)
	if err == nil {
		return nil
	}
	legacyLayout := fileRenameInformation{}
	legacyBytes := max(
		int(unsafe.Sizeof(legacyLayout)),
		int(unsafe.Offsetof(legacyLayout.fileName))+len(name)*2,
	)
	legacyBuffer := make([]byte, legacyBytes)
	legacy := (*fileRenameInformation)(unsafe.Pointer(&legacyBuffer[0]))
	legacy.replaceIfExists = 1
	legacy.rootDirectory = directory
	legacy.fileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&legacy.fileName[0], len(name)), name)
	return atomicNtSetInformationFile(
		source,
		&windows.IO_STATUS_BLOCK{},
		&legacyBuffer[0],
		uint32(len(legacyBuffer)),
		windows.FileRenameInformation,
	)
}

func windowsRenameError(err error) error {
	var status windows.NTStatus
	if errors.As(err, &status) {
		return status.Errno()
	}
	return err
}
