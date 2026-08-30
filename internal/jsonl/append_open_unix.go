//go:build !windows

package jsonl

import "os"

func openAppendBackupSourceFile(path string) (*os.File, error) {
	return os.Open(path)
}
