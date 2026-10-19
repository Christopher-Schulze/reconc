package usercli

import (
	"strings"
	"testing"
)

func TestDowngradeRefusesReleasesThatCannotReadTheReceipt(t *testing.T) {
	for _, target := range []string{"0.10.0", "0.10.2", "1.0.0"} {
		if err := downgradeKeepsReceiptReadable(target); err != nil {
			t.Fatalf("downgrade to %s refused: %v", target, err)
		}
	}
	for _, target := range []string{"0.9.8", "0.1.0"} {
		err := downgradeKeepsReceiptReadable(target)
		if err == nil || !strings.Contains(err.Error(), "cannot read the format "+ReceiptFormatVersion) {
			t.Fatalf("downgrade to %s that cannot read the receipt was allowed: %v", target, err)
		}
	}
	if ReceiptFormatVersion != "2" {
		t.Fatalf("receipt format changed to %s; update minimumReceiptReaderVersion to the first release that reads it", ReceiptFormatVersion)
	}
}
