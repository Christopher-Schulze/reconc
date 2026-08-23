package actionevidence

import "testing"

func BenchmarkValidateCanonicalSelectors(b *testing.B) {
	selectors := AllFactIDs()
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if err := validateCanonicalSelectors(selectors); err != nil {
			b.Fatal(err)
		}
	}
}
