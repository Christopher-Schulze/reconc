package execfile

import (
	"testing"
)

func TestIsRejectsDirectories(t *testing.T) {
	dir := t.TempDir()
	if Is(dir) {
		t.Fatal("directory must not be dispatchable")
	}
}
