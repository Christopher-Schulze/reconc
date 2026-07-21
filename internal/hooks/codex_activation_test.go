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
