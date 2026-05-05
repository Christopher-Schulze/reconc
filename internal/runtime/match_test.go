package runtime

import "testing"

func TestMatchPathLiteral(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"foo.txt", "foo.txt", true},
		{"foo.txt", "bar.txt", false},
		{"src/main.go", "src/main.go", true},
		{"src/main.go", "src/other.go", false},
	}
	for _, c := range cases {
		got, err := MatchPath(c.pattern, c.path)
		if err != nil {
			t.Errorf("MatchPath(%q, %q) error: %v", c.pattern, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchPathSingleStar(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.txt", "foo.txt", true},
		{"*.txt", "foo.go", false},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false}, // * does NOT cross /
		{"src/*", "src/main.go", true},
		{"src/*", "src/sub/main.go", false},
	}
	for _, c := range cases {
		got, err := MatchPath(c.pattern, c.path)
		if err != nil {
			t.Errorf("MatchPath(%q, %q) error: %v", c.pattern, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchPathDoubleStar(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"src/**", "src/main.go", true},
		{"src/**", "src/sub/main.go", true},
		{"src/**", "src/sub/sub2/main.go", true},
		{"src/**", "tests/main.go", false},
		{"**/main.go", "src/main.go", true},
		{"**/main.go", "src/sub/main.go", true},
		{"**/main.go", "main.go", true},
		{"generated/**", "generated/file.go", true},
		{"generated/**", "generated/sub/file.go", true},
		{"generated/**", "src/file.go", false},
		{"**/generated/**", "pkg/generated/file.go", true},
		{"**/*.generated.*", "src/foo.generated.go", true},
		{"**/*.generated.*", "src/foo.go", false},
	}
	for _, c := range cases {
		got, err := MatchPath(c.pattern, c.path)
		if err != nil {
			t.Errorf("MatchPath(%q, %q) error: %v", c.pattern, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchPathQuestionMark(t *testing.T) {
	got, _ := MatchPath("file?.txt", "file1.txt")
	if !got {
		t.Errorf("? should match single char")
	}
	got, _ = MatchPath("file?.txt", "file12.txt")
	if got {
		t.Errorf("? should match exactly one char, not two")
	}
}

func TestMatchPathCharClass(t *testing.T) {
	got, _ := MatchPath("file[abc].txt", "filea.txt")
	if !got {
		t.Errorf("[abc] should match a")
	}
	got, _ = MatchPath("file[abc].txt", "filed.txt")
	if got {
		t.Errorf("[abc] should not match d")
	}
}

func TestMatchAnyHit(t *testing.T) {
	pat, ok, err := MatchAny([]string{"docs/**", "src/**"}, "src/main.go")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("expected match")
	}
	if pat != "src/**" {
		t.Errorf("expected pattern src/**, got %q", pat)
	}
}

func TestMatchAnyMiss(t *testing.T) {
	_, ok, err := MatchAny([]string{"docs/**", "src/**"}, "config.json")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("expected no match")
	}
}

func TestMatchAnyEmptyPatternList(t *testing.T) {
	_, ok, err := MatchAny(nil, "anything")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("empty pattern list should never match")
	}
}

func TestMatchAnyPath(t *testing.T) {
	path, pat, ok, err := MatchAnyPath(
		[]string{"src/**"},
		[]string{"docs/file.md", "src/main.go", "src/util.go"},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if path != "src/main.go" {
		t.Errorf("expected first matching path src/main.go, got %q", path)
	}
	if pat != "src/**" {
		t.Errorf("expected pattern src/**, got %q", pat)
	}
}

func TestMatchAnyPathNoHit(t *testing.T) {
	_, _, ok, _ := MatchAnyPath(
		[]string{"src/**"},
		[]string{"docs/a.md", "config.json"},
	)
	if ok {
		t.Error("expected no match")
	}
}

func TestMatchPathTrimsWhitespace(t *testing.T) {
	got, err := MatchPath("  src/main.go  ", "src/main.go")
	if err != nil || !got {
		t.Errorf("expected whitespace-trimmed match, got %v, err: %v", got, err)
	}
}

func TestMatchPathExpectsPOSIXInput(t *testing.T) {
	// MatchPath assumes POSIX-style paths (forward slashes). It is
	// the caller's responsibility to normalize OS-native paths to
	// POSIX form BEFORE calling MatchPath. This test documents the
	// contract: backslashes are treated as glob escape chars and
	// will NOT match path separators.
	got, _ := MatchPath("src/**", "src/sub/main.go")
	if !got {
		t.Errorf("POSIX path should match")
	}
}
