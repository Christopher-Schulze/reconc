package assurance

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestPackageManagerAncestryMemoPreservesNearestAndInvalidatesMutation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"packages/one", "packages/two"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := newEvaluationState(nil, 1)
	first, err := packageManagersForManifest(root, filepath.Join(root, "packages/one"), "", state)
	if err != nil || !reflect.DeepEqual(first, []string{"npm"}) {
		t.Fatalf("first ancestry = %#v, %v", first, err)
	}
	second, err := packageManagersForManifest(root, filepath.Join(root, "packages/two"), "", state)
	if err != nil || !reflect.DeepEqual(second, []string{"npm"}) {
		t.Fatalf("sibling ancestry = %#v, %v", second, err)
	}
	if len(state.packageManagerDirectories) != 4 {
		t.Fatalf("shared parent was not memoized: %d directory observations", len(state.packageManagerDirectories))
	}
	if err := os.Remove(filepath.Join(root, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	changed, err := packageManagersForManifest(root, filepath.Join(root, "packages/two"), "", state)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Fatalf("removed lockfile remained cached: %#v", changed)
	}

	if err := os.WriteFile(filepath.Join(root, "packages/one", "pnpm-lock.yaml"), []byte("lockfileVersion: 9"), 0o600); err != nil {
		t.Fatal(err)
	}
	nearest, err := packageManagersForManifest(root, filepath.Join(root, "packages/one"), "", state)
	if err != nil || !reflect.DeepEqual(nearest, []string{"pnpm"}) {
		t.Fatalf("nearest nested manager = %#v, %v", nearest, err)
	}
}

func TestPackageManagerAncestryMemoDoesNotCacheMissingDirectoryErrors(t *testing.T) {
	state := newEvaluationState(nil, 1)
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := packageManagersForManifest(filepath.Dir(missing), missing, "", state); err == nil {
		t.Fatal("missing directory was accepted")
	}
	if len(state.packageManagerDirectories) != 0 || len(state.packageManagerAncestry) != 0 {
		t.Fatalf("failed directory was cached: directories=%d ancestry=%d", len(state.packageManagerDirectories), len(state.packageManagerAncestry))
	}
}

func BenchmarkPackageManagerAncestryMemoSiblings(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("{}"), 0o600); err != nil {
		b.Fatal(err)
	}
	manifests := make([]string, 64)
	for index := range manifests {
		directory := filepath.Join(root, "packages", "pkg-"+strconv.Itoa(index))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			b.Fatal(err)
		}
		manifests[index] = directory
	}
	state := newEvaluationState(nil, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := packageManagersForManifest(root, manifests[index%len(manifests)], "", state); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(state.stats.packageManagerDirectoryProbes.Load()), "directory-probes")
	b.ReportMetric(float64(state.stats.packageManagerLockProbes.Load()), "lockfile-probes")
}
