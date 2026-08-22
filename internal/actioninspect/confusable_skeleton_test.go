package actioninspect

import "testing"

func TestInspectionTextFoldsProtectedSVariants(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "small capital", value: "ꜱecret"},
		{name: "fullwidth", value: "ｓecret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := inspectionText(test.value, true); got != "secret" {
				t.Fatalf("inspectionText(%q) = %q, want secret", test.value, got)
			}
		})
	}
}
