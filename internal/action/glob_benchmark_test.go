package action

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkExpandGlobAlternativesRepresentative(b *testing.B) {
	benchmarkGlobExpansion(b, "{src,test}/{main,util,gen}/*.{go,md}", 12)
}

func BenchmarkExpandGlobAlternativesMaximumLegal(b *testing.B) {
	benchmarkGlobExpansion(b, "{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}", 1024)
}

func BenchmarkExpandGlobAlternativesAdversarialRejected(b *testing.B) {
	pattern := strings.Repeat("{"+strings.Repeat("a", 100)+","+strings.Repeat("b", 100)+"}", 15)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := expandGlobAlternatives(pattern); err == nil {
			b.Fatal("adversarial expansion succeeded")
		}
	}
}

func BenchmarkCompiledGlobRepeatedDirectoryStars(b *testing.B) {
	pattern := strings.Repeat("**/", 8) + "missing"
	compiled, err := compileGlob(pattern)
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{4 << 10, 64 << 10, 1 << 20, MaxJSONStringBytes} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			value := strings.Repeat("x/", size/2-4) + "present"
			b.SetBytes(int64(len(value)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				matched, complete := compiled.Match(value)
				if matched || !complete {
					b.Fatalf("repeated directory-star match = %t complete %t", matched, complete)
				}
			}
		})
	}
}

func BenchmarkCompiledGlobBraceAlternatives(b *testing.B) {
	for _, alternatives := range []int{4, 6, 8, 10} {
		pattern := "**/" + strings.Repeat("{a,b}", alternatives)
		compiled, err := compileGlob(pattern)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("programs-%d", len(compiled.programs)), func(b *testing.B) {
			value := strings.Repeat("x/", (64<<10)/2-4) + "present"
			b.SetBytes(int64(len(value)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				matched, _ := compiled.Match(value)
				if matched {
					b.Fatal("adversarial non-match unexpectedly matched")
				}
			}
		})
	}
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
