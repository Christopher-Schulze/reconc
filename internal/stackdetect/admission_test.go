package stackdetect

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCanonicalDiscoveryPathAdmitsDetectableFilesOnly(t *testing.T) {
	ignored := make([]string, 0, len(ignoredDirectories))
	for name := range ignoredDirectories {
		ignored = append(ignored, name)
	}
	sort.Strings(ignored)

	tests := []struct {
		name string
		path string
		ok   bool
		want string
	}{
		{name: "root manifest", path: "go.mod", ok: true, want: "go.mod"},
		{name: "nested manifest", path: "services/api/go.mod", ok: true, want: "services/api/go.mod"},
		{name: "windows separators", path: `services\api\Cargo.toml`, ok: true, want: "services/api/Cargo.toml"},
		{name: "deepest admissible", path: "a/b/c/d/e/go.mod", ok: true, want: "a/b/c/d/e/go.mod"},
		{name: "beyond depth", path: "a/b/c/d/e/f/go.mod", ok: false},
		{name: "empty", path: "", ok: false},
		{name: "dot", path: ".", ok: false},
		{name: "parent", path: "../go.mod", ok: false},
		{name: "absolute", path: "/go.mod", ok: false},
		{name: "non-canonical slashes", path: "services//api/go.mod", ok: false},
		{name: "dot segment", path: "services/./api/go.mod", ok: false},
		{name: "parent segment", path: "vendor/../go.mod", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := CanonicalDiscoveryPath(test.path)
			if ok != test.ok || got != test.want {
				t.Fatalf("CanonicalDiscoveryPath(%q) = (%q, %v), want (%q, %v)", test.path, got, ok, test.want, test.ok)
			}
		})
	}

	for _, name := range ignored {
		for _, test := range []struct {
			name string
			path string
		}{
			{name: "first", path: name + "/go.mod"},
			{name: "middle", path: "src/" + name + "/pkg/go.mod"},
			{name: "upper", path: strings.ToUpper(name) + "/go.mod"},
			{name: "title", path: "src/" + strings.ToUpper(name[:1]) + name[1:] + "/go.mod"},
		} {
			t.Run(name+"/"+test.name, func(t *testing.T) {
				if _, ok := CanonicalDiscoveryPath(test.path); ok {
					t.Fatalf("CanonicalDiscoveryPath(%q) admitted ignored tree", test.path)
				}
			})
		}
	}
}

func TestCanonicalDiscoveryPathAgreesWithDetectBounds(t *testing.T) {
	root := t.TempDir()
	writeDetectionFile(t, root, "services/api/go.mod", "module example/api\n")
	writeDetectionFile(t, root, "vendor/go.mod", "module example/vendor\n")
	writeDetectionFile(t, root, "a/b/c/d/e/go.mod", "module example/deep\n")
	writeDetectionFile(t, root, "a/b/c/d/e/f/go.mod", "module example/deeper\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, module := range result.Modules {
		found[module.Manifest] = true
	}
	if !found["services/api/go.mod"] || !found["a/b/c/d/e/go.mod"] {
		t.Fatalf("admissible manifests missing from detection: %#v", result.Modules)
	}
	if found["vendor/go.mod"] || found["a/b/c/d/e/f/go.mod"] {
		t.Fatalf("ignored manifests leaked into detection: %#v", result.Modules)
	}
	if _, ok := CanonicalDiscoveryPath("vendor/go.mod"); ok {
		t.Fatal("vendor manifest was admitted")
	}
	if _, ok := CanonicalDiscoveryPath(filepath.ToSlash("a/b/c/d/e/f/go.mod")); ok {
		t.Fatal("over-depth manifest was admitted")
	}
}
