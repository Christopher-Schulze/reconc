package actioninspect

import (
	"context"
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/action"
)

func TestSharedBuiltinDetectorPackMatchesFreshCompilation(t *testing.T) {
	t.Parallel()
	shared, err := loadSharedBuiltinPack()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := compileBuiltinPack()
	if err != nil {
		t.Fatal(err)
	}
	if shared.identity != fresh.identity || len(shared.rules) != len(fresh.rules) {
		t.Fatalf("shared pack identity or rules differ: shared=%q/%d fresh=%q/%d", shared.identity, len(shared.rules), fresh.identity, len(fresh.rules))
	}
	for _, test := range loadDetectorCorpus(t) {
		categories := make(map[action.DetectorCategory]struct{}, len(test.Categories))
		for _, category := range test.Categories {
			categories[category] = struct{}{}
		}
		sharedFindings, sharedErr := shared.scan(
			context.Background(), test.Text, categories, test.ForbiddenTerms, action.MaxArgumentBytes,
		)
		freshFindings, freshErr := fresh.scan(
			context.Background(), test.Text, categories, test.ForbiddenTerms, action.MaxArgumentBytes,
		)
		if sharedErr != nil || freshErr != nil || !reflect.DeepEqual(sharedFindings, freshFindings) {
			t.Fatalf("%s: shared=%v/%v fresh=%v/%v", test.Name, sharedFindings, sharedErr, freshFindings, freshErr)
		}
	}
}

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
