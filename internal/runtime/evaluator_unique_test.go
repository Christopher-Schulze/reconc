package runtime

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func TestStableStringCollectorPreservesFirstSeenOrderAndEmptyValues(t *testing.T) {
	t.Parallel()
	collector := newStableStringCollector([]string{"first", "first", ""})
	collector.add("second")
	collector.add("")
	collector.add("first")
	collector.add("third")
	want := []string{"first", "first", "", "second", "third"}
	if got := collector.values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stable collector = %#v, want %#v", got, want)
	}
}

func TestStableStringCollectorMatchesReferenceAcrossSequences(t *testing.T) {
	t.Parallel()
	random := rand.New(rand.NewSource(211))
	for sequence := 0; sequence < 128; sequence++ {
		initial := make([]string, random.Intn(16))
		for index := range initial {
			initial[index] = "value-" + strconv.Itoa(random.Intn(8))
		}
		collector := newStableStringCollector(initial)
		want := append([]string(nil), initial...)
		for index := 0; index < 256; index++ {
			value := "value-" + strconv.Itoa(random.Intn(32))
			collector.add(value)
			found := false
			for _, existing := range want {
				if existing == value {
					found = true
					break
				}
			}
			if !found {
				want = append(want, value)
			}
		}
		if got := collector.values(); !reflect.DeepEqual(got, want) {
			t.Fatalf("sequence %d = %#v, want %#v", sequence, got, want)
		}
	}
}

func TestStableStringCollectorDefersEmptyMembershipIndex(t *testing.T) {
	t.Parallel()
	collector := newStableStringCollector([]string{})
	if collector.seen != nil {
		t.Fatal("empty collector allocated a membership index")
	}
	collector.add("first")
	if collector.seen == nil || len(collector.seen) != 1 {
		t.Fatalf("first retained value did not initialize membership: %#v", collector.seen)
	}
}

func BenchmarkStableStringCollector(b *testing.B) {
	b.Run("empty", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			collector := newStableStringCollector([]string{})
			if collector.seen != nil || len(collector.values()) != 0 {
				b.Fatal("empty collector initialized storage")
			}
		}
	})
	for _, size := range []int{128, 512, 2048, 8192} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			values := make([]string, size)
			for index := range values {
				values[index] = "path-" + strconv.Itoa(index)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				collector := newStableStringCollector([]string{})
				for _, value := range values {
					collector.add(value)
				}
				if len(collector.values()) != size {
					b.Fatalf("collector length = %d, want %d", len(collector.values()), size)
				}
			}
		})
	}
}
