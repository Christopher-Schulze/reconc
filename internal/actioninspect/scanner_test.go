package actioninspect

import (
	"context"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestTextScannerClassifiesPrivateCategoriesWithoutReturningContent(t *testing.T) {
	t.Parallel()
	scanner, err := NewTextScanner()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		text string
		want action.DetectorCategory
	}{
		{name: "email", text: "person@example.test", want: action.DetectorPIIEmail},
		{name: "phone", text: "phone: 493012345678", want: action.DetectorPIIPhone},
		{name: "payment card", text: "4111 1111 1111 1111", want: action.DetectorPIIPaymentCard},
		{name: "secret", text: "api_key=Q7m9V2p4R8x6L3n5", want: action.DetectorSecret},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			categories, err := scanner.PrivateCategories(context.Background(), test.text, action.MaxArgumentBytes)
			if err != nil || !containsCategory(categories, test.want) {
				t.Fatalf("categories = %v, error = %v", categories, err)
			}
		})
	}
}

func containsCategory(values []action.DetectorCategory, want action.DetectorCategory) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
