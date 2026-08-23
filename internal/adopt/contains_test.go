package adopt

import (
	"bytes"
	"testing"
)

func TestContainsSearchesLargeByteBuffers(t *testing.T) {
	body := append(bytes.Repeat([]byte{'x'}, maxAdoptManifestBytes-4), []byte("ruff")...)
	if !contains(body, "ruff") || contains(body, "pytest") {
		t.Fatal("byte-buffer search returned the wrong result")
	}
}

func BenchmarkContainsLargeByteBuffer(b *testing.B) {
	body := append(bytes.Repeat([]byte{'x'}, maxAdoptManifestBytes-4), []byte("ruff")...)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		if !contains(body, "ruff") {
			b.Fatal("needle not found")
		}
	}
}
