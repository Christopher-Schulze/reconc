package jsonl

import (
	"context"
	"os"
)

func tailDataWithLayout(path string, maxBytes int64, layout Layout) (int64, int64, []byte, os.FileMode, error) {
	return tailDataForLayout(path, path, maxBytes, layout)
}

func archiveCandidates(path string) ([]archiveCandidate, error) {
	return archiveCandidatesContext(context.Background(), path)
}

func readArchiveDirectoryWith(
	directory string,
	read func(string, int) ([]os.DirEntry, error),
) ([]os.DirEntry, error) {
	return readArchiveDirectoryContextWith(context.Background(), directory, read)
}
