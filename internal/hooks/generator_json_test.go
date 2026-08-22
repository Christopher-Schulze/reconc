package hooks

import (
	"strings"
	"testing"
)

func TestJSONHookArtifactPropagatesEncodingFailure(t *testing.T) {
	t.Parallel()

	artifact, err := jsonHookArtifact(&Artifact{Kind: "test", TargetPath: ".test/hooks.json"}, make(chan struct{}))
	if err == nil {
		t.Fatal("jsonHookArtifact() error = nil, want unsupported-value error")
	}
	if artifact != nil {
		t.Fatalf("jsonHookArtifact() artifact = %#v, want nil", artifact)
	}
	if !strings.Contains(err.Error(), "marshal test hook artifact") {
		t.Fatalf("jsonHookArtifact() error = %q, want contextual marshal error", err)
	}
}
