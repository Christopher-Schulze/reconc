//go:build darwin

package atomicfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var syncParentDirOrdered = syncParentDirOrderedImpl

// syncFileOrdered issues F_BARRIERFSYNC: the file's writes reach the drive
// before any write issued after the barrier, without the whole-device cache
// flush of F_FULLFSYNC that File.Sync performs.
func syncFileOrdered(file *os.File) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var syncErr error
	if err := raw.Control(func(fd uintptr) { syncErr = barrierSync(fd) }); err != nil {
		return err
	}
	return syncErr
}

// barrierSync falls back to fsync where the file system rejects barriers,
// such as SMB mounts, as File.Sync does for F_FULLFSYNC.
func barrierSync(fd uintptr) error {
	for {
		_, err := unix.FcntlInt(fd, unix.F_BARRIERFSYNC, 0)
		if errors.Is(err, unix.ENOTSUP) {
			err = unix.Fsync(int(fd))
		}
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func syncParentDirOrderedImpl(directory *os.Root) error {
	dir, err := directory.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(syncFileOrdered(dir), dir.Close())
}
