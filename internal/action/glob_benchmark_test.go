package action

import "testing"

func BenchmarkExpandGlobAlternativesRepresentative(b *testing.B) {
	benchmarkGlobExpansion(b, "{src,test}/{main,util,gen}/*.{go,md}", 12)
}

func BenchmarkExpandGlobAlternativesMaximumLegal(b *testing.B) {
	benchmarkGlobExpansion(b, "{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}", 1024)
}

func benchmarkGlobExpansion(b *testing.B, pattern string, want int) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		expanded, err := expandGlobAlternatives(pattern)
		if err != nil {
			b.Fatal(err)
		}
		if len(expanded) != want {
			b.Fatalf("expanded alternatives = %d, want %d", len(expanded), want)
		}
	}
}
