package retention

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Shared temporary roots contain other applications' state. Bound traversal
// separately from retained candidates, and never inspect unrelated subtrees.
func readOwnedTempDirectories(path string, maxScanned, maxOwned int) ([]os.DirEntry, error) {
	if maxScanned <= 0 || maxOwned <= 0 {
		return nil, errors.New("temporary directory scan requires positive limits")
	}
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("%s must resolve to a directory", path)
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := directory.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		return nil, errors.Join(err, errors.New("temporary root changed identity while opening"), directory.Close())
	}
	entries, scanErr := collectOwnedTempDirectories(directory, maxScanned, maxOwned)
	after, statErr := os.Stat(path)
	closeErr := directory.Close()
	if statErr == nil && (!after.IsDir() || !os.SameFile(opened, after)) {
		statErr = errors.New("temporary root changed identity while scanning")
	}
	if err := errors.Join(scanErr, statErr, closeErr); err != nil {
		return nil, err
	}
	return entries, nil
}

func collectOwnedTempDirectories(directory *os.File, maxScanned, maxOwned int) ([]os.DirEntry, error) {
	var owned []os.DirEntry
	scanned := 0
	for {
		entries, err := directory.ReadDir(min(255, maxScanned-scanned) + 1)
		scanned += len(entries)
		if scanned > maxScanned {
			return nil, fmt.Errorf("temporary root exceeds %d scanned entries", maxScanned)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		for _, entry := range entries {
			if !isOwnedTempRoot(entry.Name()) || !entry.IsDir() {
				continue
			}
			if len(owned) == maxOwned {
				return nil, fmt.Errorf("temporary root exceeds %d owned directories", maxOwned)
			}
			owned = append(owned, entry)
		}
		if errors.Is(err, io.EOF) {
			return owned, nil
		}
	}
}
