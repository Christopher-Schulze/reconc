package bootstrap

import (
	"fmt"

	"reconc.dev/reconc/internal/atomicfile"
)

func writeRepositoryReceiptAtomic(root string, receipt *RepositoryReceipt) (bool, error) {
	body, err := encodeRepositoryReceipt(receipt)
	if err != nil {
		return false, err
	}
	target, err := safeBootstrapTarget(root, RepositoryReceiptRelativePath)
	if err != nil {
		return false, err
	}
	result, err := atomicfile.WriteIfChanged(target, body, 0o644)
	if err != nil {
		return false, fmt.Errorf("publish repository receipt: %w", err)
	}
	return result.Changed, nil
}
