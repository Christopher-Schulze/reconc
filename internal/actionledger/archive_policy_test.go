package actionledger

import (
	"strconv"
	"strings"
	"testing"
)

func TestAllowedLedgerEntryDerivesArchiveRangeFromPolicy(t *testing.T) {
	for _, maximum := range []int{0, 1, 2, 4} {
		t.Run(strconv.Itoa(maximum), func(t *testing.T) {
			for _, name := range []string{liveFileName, headFileName, lockFileName} {
				if !allowedLedgerEntry(name, false, maximum) {
					t.Fatalf("stable ledger member %q was rejected", name)
				}
			}
			if allowedLedgerEntry(transactionFileName, false, maximum) ||
				!allowedLedgerEntry(transactionFileName, true, maximum) {
				t.Fatal("transaction journal ownership did not follow transactionFiles")
			}
			for index := 1; index <= maximum; index++ {
				name := liveFileName + "." + strconv.Itoa(index)
				if !allowedLedgerEntry(name, false, maximum) {
					t.Fatalf("archive %q was rejected", name)
				}
			}
			for index := 0; index <= maximum; index++ {
				name := transactionBackup + "." + strconv.Itoa(index)
				if allowedLedgerEntry(name, false, maximum) || !allowedLedgerEntry(name, true, maximum) {
					t.Fatalf("transaction backup %q has the wrong ownership", name)
				}
			}
			if allowedLedgerEntry(liveFileName+"."+strconv.Itoa(maximum+1), false, maximum) ||
				allowedLedgerEntry(transactionBackup+"."+strconv.Itoa(maximum+1), true, maximum) {
				t.Fatal("out-of-range ledger member was accepted")
			}
		})
	}
}

func TestAllowedLedgerEntryRejectsNonCanonicalSuffixes(t *testing.T) {
	for _, suffix := range []string{"", "0", "00", "01", "+1", "-1", " 1", "1 ", "1.0", "x", strings.Repeat("9", 128)} {
		if allowedLedgerEntry(liveFileName+"."+suffix, true, 4) {
			t.Errorf("archive suffix %q was accepted", suffix)
		}
		if suffix != "0" && allowedLedgerEntry(transactionBackup+"."+suffix, true, 4) {
			t.Errorf("transaction-backup suffix %q was accepted", suffix)
		}
	}
	if !allowedLedgerEntry(transactionBackup+".0", true, 4) {
		t.Fatal("canonical live-file transaction backup was rejected")
	}
}

func TestArchiveSetContinuityForConfiguredRange(t *testing.T) {
	for _, maximum := range []int{0, 1, 2, 4} {
		for mask := 0; mask < 1<<(maximum+1); mask++ {
			exists := make([]bool, maximum+1)
			want := true
			missingSeen := false
			for index := range exists {
				exists[index] = mask&(1<<index) != 0
				if !exists[index] {
					missingSeen = true
				} else if missingSeen {
					want = false
				}
			}
			if got := archiveSetContiguous(exists); got != want {
				t.Fatalf("maximum=%d mask=%b continuity=%t want %t", maximum, mask, got, want)
			}
		}
	}
}

func FuzzBoundedLedgerIndex(f *testing.F) {
	f.Add("ledger.jsonl.1", uint8(2))
	f.Add("ledger.jsonl.01", uint8(4))
	f.Add("ledger.jsonl.999999999999999999999999999999", uint8(32))
	f.Fuzz(func(t *testing.T, name string, rawMaximum uint8) {
		maximum := int(rawMaximum % 33)
		index, ok := boundedLedgerIndex(name, liveFileName+".", 1, maximum)
		if !ok {
			return
		}
		if index < 1 || index > maximum || name != liveFileName+"."+strconv.Itoa(index) {
			t.Fatalf("accepted non-canonical archive name %q as %d/%d", name, index, maximum)
		}
	})
}
