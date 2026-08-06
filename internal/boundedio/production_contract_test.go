package boundedio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInternalProductionCodeAvoidsUnboundedWholeInputHelpers(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "internal"))
	forbidden := []string{"os.ReadFile(", "os.ReadDir("}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range forbidden {
			if strings.Contains(string(body), needle) {
				t.Errorf("%s uses %s; use an explicit bounded reader", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
