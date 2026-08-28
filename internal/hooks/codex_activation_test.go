package hooks

import (
	"strings"
	"testing"
)

func TestRenderCodexActivationSeparatesFinalFeaturesHeader(t *testing.T) {
	got, err := RenderCodexActivation("[features]", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "[features]\n"+CodexActivationBlockStart+"\n") {
		t.Fatalf("managed block was concatenated onto final features header: %q", got)
	}
	if strings.Contains(got, "[features]"+CodexActivationBlockStart) {
		t.Fatalf("invalid TOML activation: %q", got)
	}
}

func TestCodexActivationParsesQuotedCommentsAndOneCanonicalBoolean(t *testing.T) {
	t.Run("quoted hash and inline comment", func(t *testing.T) {
		input := "model = \"release#candidate\"\nnote = 'literal#value'\n[features] # activation\nhooks = true # enabled\n"
		got, err := RenderCodexActivation(input, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != input {
			t.Fatalf("enabled config changed:\nwant %q\n got %q", input, got)
		}
	})

	t.Run("dotted key", func(t *testing.T) {
		input := "model = \"gpt\"\nfeatures.hooks = true\n"
		got, err := RenderCodexActivation(input, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != input {
			t.Fatalf("enabled dotted config changed:\nwant %q\n got %q", input, got)
		}
	})

	t.Run("forced dotted false restores exactly", func(t *testing.T) {
		input := "features.hooks = false # explicit choice\n"
		installed, err := RenderCodexActivation(input, true)
		if err != nil {
			t.Fatal(err)
		}
		restored, managed, err := RemoveCodexActivation(installed)
		if err != nil {
			t.Fatal(err)
		}
		if !managed || restored != input {
			t.Fatalf("dotted restore managed=%t:\nwant %q\n got %q", managed, input, restored)
		}
	})

	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "duplicate key", content: "[features]\nhooks = true\nhooks = false\n", want: "key hooks is already defined"},
		{name: "duplicate table", content: "[features]\nhooks = true\n[features]\n", want: "table features already exists"},
		{name: "root key", content: "hooks = true\n", want: "TOML root"},
		{name: "mixed dotted and table", content: "features.hooks = true\n[features]\n", want: "table features already exists"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RenderCodexActivation(test.content, false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RenderCodexActivation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCodexActivationIgnoresMultilineStringContentsAndMarkerText(t *testing.T) {
	input := "description = \"\"\"\n# >>> reconc bootstrap hooks\n[features]\nhooks = true\n# <<< reconc bootstrap hooks\n\"\"\"\nliteral = '''\nfeatures.hooks = false\n'''\n"
	cleaned, managed, err := RemoveCodexActivation(input)
	if err != nil || managed || cleaned != input {
		t.Fatalf("multiline marker removal = managed=%t err=%v\nwant %q\n got %q", managed, err, input, cleaned)
	}
	installed, err := RenderCodexActivation(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(installed, input) {
		t.Fatalf("unrelated multiline content changed:\nwant prefix %q\n got %q", input, installed)
	}
	value, err := parseTOMLSectionBoolean(installed, "features", "hooks")
	if err != nil || !value.present || !value.enabled {
		t.Fatalf("installed multiline activation = %+v, %v", value, err)
	}
}

func TestCodexActivationQuotedKeysAndInlineTablesRoundTripExactly(t *testing.T) {
	for _, input := range []string{
		"description = \"\"\"\n# not a comment\nfeatures.hooks = true\n\"\"\"\n[\"features\"]\n  \"hooks\" = false # user choice\n",
		"features = { experimental = true, hooks = false } # inline choice\n",
		"features = { experimental = true } # inline extension\n",
		"features = { experimental = true, } # trailing comma\n",
		"features = {\n  # comment-only table\n}\n",
	} {
		installed, err := RenderCodexActivation(input, true)
		if err != nil {
			t.Fatalf("install %q: %v", input, err)
		}
		value, err := parseTOMLSectionBoolean(installed, "features", "hooks")
		if err != nil || !value.present || !value.enabled {
			t.Fatalf("installed activation = %+v, %v\n%s", value, err, installed)
		}
		restored, managed, err := RemoveCodexActivation(installed)
		if err != nil || !managed || restored != input {
			t.Fatalf("round trip managed=%t err=%v\nwant %q\n got %q", managed, err, input, restored)
		}
	}
}

func TestCodexActivationExtendsImplicitFeaturesBeforeTables(t *testing.T) {
	input := "features.experimental = true\n[other]\nvalue = \"preserved\"\n"
	installed, err := RenderCodexActivation(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(installed, CodexActivationBlockStart) > strings.Index(installed, "[other]") {
		t.Fatalf("root activation was inserted inside [other]:\n%s", installed)
	}
	value, err := parseTOMLSectionBoolean(installed, "features", "hooks")
	if err != nil || !value.present || !value.enabled {
		t.Fatalf("implicit activation = %+v, %v", value, err)
	}
	restored, managed, err := RemoveCodexActivation(installed)
	if err != nil || !managed || restored != input {
		t.Fatalf("implicit round trip managed=%t err=%v\nwant %q\n got %q", managed, err, input, restored)
	}
}

func TestCodexActivationDoesNotTreatQuotedLiteralDottedKeyAsPath(t *testing.T) {
	input := "\"features.hooks\" = false\n"
	installed, err := RenderCodexActivation(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(installed, input) {
		t.Fatalf("literal dotted key changed:\nwant prefix %q\n got %q", input, installed)
	}
	value, err := parseTOMLSectionBoolean(installed, "features", "hooks")
	if err != nil || !value.present || !value.enabled {
		t.Fatalf("literal dotted-key activation = %+v, %v", value, err)
	}
}
