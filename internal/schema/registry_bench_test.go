package schema

import "testing"

func BenchmarkRegistryLookup(b *testing.B) {
	b.ReportAllocs()
	b.Run("current-contract", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			_, _ = CurrentContract(PolicyLock)
		}
	})
	b.Run("exact-version", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			_, _ = ContractVersion(PolicyLock, "6")
		}
	})
	b.Run("identity-and-format", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			_ = AcceptsFormat(PolicyLock, PolicyLockURL, "6")
		}
	})
}

func BenchmarkRegistryBuild(b *testing.B) {
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_ = buildRegistry()
	}
}
