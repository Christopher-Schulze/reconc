package policy

import (
	"encoding/json"
	"testing"
)

func TestModeIsBlocking(t *testing.T) {
	cases := []struct {
		mode Mode
		want bool
	}{
		{ModeObserve, false},
		{ModeWarn, false},
		{ModeBlock, true},
		{ModeFix, true},
	}
	for _, c := range cases {
		if got := c.mode.IsBlocking(); got != c.want {
			t.Errorf("Mode(%q).IsBlocking() = %v, want %v", c.mode, got, c.want)
		}
	}
}

func TestModeValid(t *testing.T) {
	for _, m := range AllModes() {
		if !m.Valid() {
			t.Errorf("canonical mode %q must be Valid()", m)
		}
	}
	if Mode("nope").Valid() {
		t.Error("Mode(\"nope\") should be invalid")
	}
}

func TestAllModesLen(t *testing.T) {
	if got := len(AllModes()); got != 4 {
		t.Errorf("expected 4 modes, got %d", got)
	}
}

func TestKindValid(t *testing.T) {
	for _, k := range AllKinds() {
		if !k.Valid() {
			t.Errorf("canonical kind %q must be Valid()", k)
		}
	}
	if Kind("mystery").Valid() {
		t.Error("Kind(\"mystery\") should be invalid")
	}
}

func TestAllKindsLen(t *testing.T) {
	// 7 core + 2 evidence + 3 composite + 1 script = 13.
	if got := len(AllKinds()); got != 13 {
		t.Errorf("expected 13 rule kinds, got %d", got)
	}
}

func TestKindIsComposite(t *testing.T) {
	for _, k := range []Kind{KindAllOf, KindAnyOf, KindNot} {
		if !k.IsComposite() {
			t.Errorf("kind %s should be composite", k)
		}
	}
	for _, k := range []Kind{KindDenyWrite, KindRequireFreshFile, KindRequireEvidence} {
		if k.IsComposite() {
			t.Errorf("kind %s should NOT be composite", k)
		}
	}
}

func TestSourcePrecedenceLen(t *testing.T) {
	if got := len(SourcePrecedence()); got != 8 {
		t.Errorf("expected 8 source precedence entries, got %d", got)
	}
}

func TestSourcePrecedenceOrder(t *testing.T) {
	want := []SourceKind{
		SourceGlobal,
		SourceClaudeMD,
		SourceAgentsMD,
		SourceStartMD,
		SourceInlineBlock,
		SourceCompilerConfig,
		SourcePreset,
		SourcePolicyFile,
	}
	got := SourcePrecedence()
	for i, w := range want {
		if got[i] != w {
			t.Errorf("SourcePrecedence()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestRuleStringIncludesAllIdentity(t *testing.T) {
	r := Rule{ID: "no-secrets", Kind: KindDenyWrite, Mode: ModeBlock}
	got := r.String()
	if got != "no-secrets (deny_write, block)" {
		t.Errorf("got %q", got)
	}
}

func TestRuleJSONOmitsEmptySlices(t *testing.T) {
	r := Rule{
		ID:      "minimal",
		Kind:    KindDenyWrite,
		Mode:    ModeBlock,
		Message: "do not write",
		Paths:   []string{"generated/**"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Only paths should appear; before_paths, when_paths, commands,
	// claims should be omitted.
	str := string(data)
	for _, forbidden := range []string{
		`"before_paths"`,
		`"when_paths"`,
		`"commands"`,
		`"claims"`,
	} {
		if contains(str, forbidden) {
			t.Errorf("JSON should omit empty slice %q, got: %s", forbidden, str)
		}
	}
	if !contains(str, `"paths":["generated/**"]`) {
		t.Errorf("JSON should include paths, got: %s", str)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
