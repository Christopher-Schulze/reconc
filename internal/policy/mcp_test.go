package policy

import "testing"

func TestResolveJSONPointerTraversesObjectsAndArrays(t *testing.T) {
	root := map[string]interface{}{
		"nested": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"path/name": "src/app.go"},
			},
		},
	}
	value, ok := ResolveJSONPointer(root, "/nested/items/0/path~1name")
	if !ok || value != "src/app.go" {
		t.Fatalf("resolved value = %#v, ok = %v", value, ok)
	}

	for _, pointer := range []string{
		"/nested/items/-1",
		"/nested/items/00",
		"/nested/items/1",
		"/nested/items/-",
		"/nested/items/not-an-index",
	} {
		if value, ok := ResolveJSONPointer(root, pointer); ok {
			t.Fatalf("%s unexpectedly resolved to %#v", pointer, value)
		}
	}
}
