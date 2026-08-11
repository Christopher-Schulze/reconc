//go:build !windows

package actionstate

import "os"

func syncStateDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		if closeErr != nil {
			return closeErr
		}
		return err
	}
	return directory.Close()
}
