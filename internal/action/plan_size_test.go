package action

import "testing"

func TestCompiledPlanLogicalByteBoundaryIncludesCanonicalBody(t *testing.T) {
	t.Parallel()
	if got, err := extendCompiledPlanBytes(0, MaxCompiledPlanBytes); err != nil || got != MaxCompiledPlanBytes {
		t.Fatalf("exact boundary = %d, %v", got, err)
	}
	for _, test := range []struct {
		current    int
		additional int
	}{
		{current: 0, additional: MaxCompiledPlanBytes + 1},
		{current: MaxCompiledPlanBytes, additional: 1},
		{current: -1, additional: 0},
		{current: 0, additional: -1},
	} {
		if _, err := extendCompiledPlanBytes(test.current, test.additional); err == nil {
			t.Fatalf("overflow accepted for current=%d additional=%d", test.current, test.additional)
		}
	}
}
