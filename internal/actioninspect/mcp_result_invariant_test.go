package actioninspect

import (
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestMCPResultAccessorsFailClosed(t *testing.T) {
	t.Parallel()
	text, err := action.String("not-a-container")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "content item", run: func() error {
			_, err := requiredArrayItem(text, 0)
			return err
		}},
		{name: "object member", run: func() error {
			_, err := requiredObjectMember(text, 0)
			return err
		}},
		{name: "annotation collection", run: func() error {
			_, err := collectAnnotationFields(text)
			return err
		}},
		{name: "metadata collection", run: func() error {
			_, err := collectMetadataPointers(action.Value{}, text)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.run(); !IsMalformedResult(err) {
				t.Fatalf("inaccessible MCP result container error = %v", err)
			}
		})
	}
}
