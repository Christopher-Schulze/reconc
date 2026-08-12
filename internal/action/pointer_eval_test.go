package action

import (
	"math"
	"strconv"
	"testing"
)

func TestResolvePointerRFC6901States(t *testing.T) {
	t.Parallel()
	root := mustTestValue(t, `{"a/b":{"~":[null,{"value":"ok"}]},"01":"object-key"}`)
	tests := []struct {
		name    string
		pointer string
		state   PointerState
		text    string
	}{
		{name: "root", pointer: "", state: PointerPresent},
		{name: "escaped null", pointer: "/a~1b/~0/0", state: PointerNull},
		{name: "array member", pointer: "/a~1b/~0/1/value", state: PointerPresent, text: "ok"},
		{name: "missing member", pointer: "/missing", state: PointerMissing},
		{name: "out of range", pointer: "/a~1b/~0/2", state: PointerMissing},
		{name: "wrong container", pointer: "/a~1b/~0/0/x", state: PointerWrongContainer},
		{name: "leading zero object key", pointer: "/01", state: PointerPresent, text: "object-key"},
		{name: "leading zero array index", pointer: "/a~1b/~0/01", state: PointerInvalidIndex},
		{name: "dash array index", pointer: "/a~1b/~0/-", state: PointerInvalidIndex},
		{name: "signed array index", pointer: "/a~1b/~0/+1", state: PointerInvalidIndex},
		{name: "overflow array index", pointer: "/a~1b/~0/999999999999999999999999", state: PointerInvalidIndex},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := ResolvePointer(root, test.pointer)
			if err != nil {
				t.Fatal(err)
			}
			if result.State != test.state {
				t.Fatalf("state = %s, want %s", result.State, test.state)
			}
			if test.text != "" {
				text, ok := result.Value.Text()
				if !ok || text != test.text {
					t.Fatalf("text = %q, %v; want %q", text, ok, test.text)
				}
			}
		})
	}
}

func TestCanonicalArrayIndexEnforcesNativeIntRange(t *testing.T) {
	t.Parallel()
	maximum := strconv.FormatUint(uint64(math.MaxInt), 10)
	if index, ok := canonicalArrayIndex(maximum); !ok || index != math.MaxInt {
		t.Fatalf("native maximum = %d, %v; want %d, true", index, ok, math.MaxInt)
	}
	if strconv.IntSize == 32 {
		if _, ok := canonicalArrayIndex("2147483648"); ok {
			t.Fatal("value above 32-bit native int range was accepted")
		}
		return
	}
	if _, ok := canonicalArrayIndex("9223372036854775808"); ok {
		t.Fatal("value above 64-bit native int range was accepted")
	}
}

func TestResolvePointerRejectsInvalidSyntax(t *testing.T) {
	t.Parallel()
	root := mustTestValue(t, `{}`)
	for _, pointer := range []string{"relative", "/bad~", "/bad~2"} {
		pointer := pointer
		t.Run(pointer, func(t *testing.T) {
			t.Parallel()
			if _, err := ResolvePointer(root, pointer); err == nil {
				t.Fatal("invalid pointer unexpectedly resolved")
			}
		})
	}
}

func TestResolveCompiledPointerUsesValidatedTokens(t *testing.T) {
	t.Parallel()
	root := mustTestValue(t, `{"items":[{"name":"ok"}]}`)
	tokens, err := CompilePointer("/items/0/name")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ResolveCompiledPointer(root, tokens)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := result.Value.Text()
	if result.State != PointerPresent || !ok || text != "ok" {
		t.Fatalf("compiled pointer result = %#v", result)
	}
	if _, err := ResolveCompiledPointer(root, []string{string([]byte{0xff})}); err == nil {
		t.Fatal("invalid compiled token unexpectedly resolved")
	}
}
