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
		{name: "duplicate key", content: "[features]\nhooks = true\nhooks = false\n", want: "duplicate features.hooks"},
		{name: "duplicate table", content: "[features]\nhooks = true\n[features]\n", want: "duplicate [features] table"},
		{name: "root key", content: "hooks = true\n", want: "TOML root"},
		{name: "mixed dotted and table", content: "features.hooks = true\n[features]\n", want: "duplicate [features] table"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RenderCodexActivation(test.content, false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RenderCodexActivation error = %v, want %q", err, test.want)
			}
		})
	}
}
