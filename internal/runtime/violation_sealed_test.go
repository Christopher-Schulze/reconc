package runtime

import (
	"strings"
	"testing"
)

func TestSealedViolationCollectorCountsWithoutSanitizing(t *testing.T) {
	collector := newViolationTextCollector(violationOmissionReserve+8, "; ", "failures")
	collector.add(strings.Repeat("x", 64))
	if !collector.sealed {
		t.Fatal("an over-limit value must seal the collector")
	}
	invalid := "bad\xffbyte" + strings.Repeat("y", 256)
	allocations := testing.AllocsPerRun(100, func() { collector.add(invalid) })
	if allocations != 0 {
		t.Fatalf("sealed add allocated %.0f times, want 0", allocations)
	}
	if collector.omitted != 101 || !strings.Contains(collector.text(), "101 additional failures omitted") {
		t.Fatalf("omitted=%d text=%q", collector.omitted, collector.text())
	}
}
