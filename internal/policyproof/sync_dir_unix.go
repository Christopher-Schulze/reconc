//go:build !windows

package policyproof

import (
	"errors"
	"os"
)

func syncProofDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		return errors.Join(err, directory.Close())
	}
	return directory.Close()
}
