package catalogue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EVERY ATOM DIRECTORY HOLDS <name>.go AND ITS TESTS, NOTHING ELSE. The scan that
// generates the registry used to look only for <name>.go, so a second Go file
// passed here and froze the atom index at the daedalus builder.
func TestEveryAtomDirectoryHoldsOnlyItsOwnFile(t *testing.T) {
	if _, _, err := scan(filepath.Join("..", "..", "atoms")); err != nil {
		t.Fatal(err)
	}
}

func TestAnAtomDirectoryWithASecondGoFileIsRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ns", "name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"name.go", "name_test.go", "other_test.go", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := onlyItsOwnFile(dir, "name"); err != nil {
		t.Fatalf("its own file and tests are fine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helpers.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := onlyItsOwnFile(dir, "name"); err == nil || !strings.Contains(err.Error(), "helpers.go") {
		t.Errorf("a second Go file must be refused: %v", err)
	}
	if _, _, err := scan(root); err == nil || !strings.Contains(err.Error(), "helpers.go") {
		t.Errorf("the scan must refuse it too: %v", err)
	}
	if err := onlyItsOwnFile(filepath.Join(root, "gone"), "gone"); err == nil {
		t.Error("an unreadable directory is an error")
	}
}
