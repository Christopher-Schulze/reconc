//go:build darwin

package pathidentity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveExistingCanonicalizesDarwinCase(t *testing.T) {
	root := t.TempDir()
	mixed := filepath.Join(root, "MiXeD")
	if err := os.Mkdir(mixed, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "mixed")
	if _, err := os.Stat(alias); err != nil {
		t.Skip("test volume is case-sensitive")
	}

	want, err := ResolveExisting(mixed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("case alias resolved to %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, string(filepath.Separator)+"MiXeD") {
		t.Fatalf("resolved path did not preserve filesystem case: %q", got)
	}
}

func TestSelectDarwinIdentityFallsBackWhenDescriptorLookupIsUnavailable(t *testing.T) {
	evaluated := filepath.Join(string(filepath.Separator), "tmp", "MiXeD")
	for _, test := range []struct {
		name    string
		buffer  []byte
		callErr error
	}{
		{name: "syscall error", buffer: []byte("ignored\x00"), callErr: errors.New("not supported")},
		{name: "empty response", buffer: make([]byte, 32)},
		{name: "unterminated response", buffer: []byte("unterminated")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := selectDarwinIdentity(evaluated, test.buffer, test.callErr); got != evaluated {
				t.Fatalf("fallback identity = %q, want %q", got, evaluated)
			}
		})
	}
}

func TestSelectDarwinIdentityUsesOnlyCaseEquivalentDescriptorPath(t *testing.T) {
	evaluated := filepath.Join(string(filepath.Separator), "tmp", "mixed")
	canonical := filepath.Join(string(filepath.Separator), "tmp", "MiXeD")
	if got := selectDarwinIdentity(evaluated, append([]byte(canonical), 0), nil); got != canonical {
		t.Fatalf("case-canonical identity = %q, want %q", got, canonical)
	}
	unrelated := filepath.Join(string(filepath.Separator), "tmp", "other")
	if got := selectDarwinIdentity(evaluated, append([]byte(unrelated), 0), nil); got != evaluated {
		t.Fatalf("unrelated descriptor path replaced entry identity: %q", got)
	}
}

func TestResolveExistingPreservesDistinctHardlinkNames(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("shared inode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	resolvedFirst, err := ResolveExisting(first)
	if err != nil {
		t.Fatal(err)
	}
	resolvedSecond, err := ResolveExisting(second)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedFirst == resolvedSecond {
		t.Fatalf("distinct hardlink entries collapsed to one path identity: %q", resolvedFirst)
	}
	if filepath.Base(resolvedFirst) != "first" || filepath.Base(resolvedSecond) != "second" {
		t.Fatalf("hardlink entry names changed: first=%q second=%q", resolvedFirst, resolvedSecond)
	}
}

func TestProspectiveResolverRelistsDirectoryAfterCaseOnlyRename(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolver := NewProspectiveResolver()
	// The second sibling caches the parent and the third lists it; c.txt is
	// never resolved itself.
	for _, name := range []string{"a.txt", "b.txt", "d.txt"} {
		if _, err := resolver.Resolve(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(root, "c.txt"), filepath.Join(root, "C.txt")); err != nil {
		t.Fatal(err)
	}
	// e.txt finds the parent changed and caches it again with its new
	// metadata, so only the listing's own binding can expose the rename.
	if _, err := resolver.Resolve(filepath.Join(root, "e.txt")); err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(filepath.Join(root, "c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := ResolveProspective(filepath.Join(root, "c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want || filepath.Base(got) != "C.txt" {
		t.Fatalf("resolver answered from a stale directory listing: got %q, want %q", got, want)
	}
}
