package schema

import (
	"strings"
	"testing"
	"time"
)

func TestBoundedECMAScriptRegexpTimesOutFailClosed(t *testing.T) {
	compiled, err := CompileBoundedECMAScriptRegexp(`^(a+)+$`)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if compiled.MatchString(strings.Repeat("a", 100_000) + "!") {
		t.Fatal("catastrophic non-match was accepted")
	}
	if elapsed := time.Since(started); elapsed > 5*schemaRegexpTimeout {
		t.Fatalf("regexp timeout took %s", elapsed)
	}
}
