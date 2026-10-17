package atoms

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunHelperKnowsTheHelpersByTheNameTheyAreRunUnder(t *testing.T) {
	for _, argv := range [][]string{nil, {"atom"}, {"/work/atom", "--describe"}, {"/usr/bin/psx"}} {
		if code, ok := RunHelper(argv); ok || code != 0 {
			t.Errorf("%q ran as a helper: %d", argv, code)
		}
	}
	// ps answers gomutants' question: a pgid no process holds exits 1 as ps does.
	if code, ok := RunHelper([]string{"/work/bin/ps", "-o", "rss=", "-g", "2147483646"}); !ok || code != 1 {
		t.Errorf("ps: %d %v", code, ok)
	}
	// execmem's bare "--" is its usage error.
	if code, ok := RunHelper([]string{"/work/bin/execmem", "--"}); !ok || code != 2 {
		t.Errorf("execmem: %d %v", code, ok)
	}
}

func TestLinkHelperLinksThisBinaryUnderTheName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second ask leaves the same link
		link, err := LinkHelper(dir, "ps")
		if err != nil || link != filepath.Join(dir, "ps") {
			t.Fatalf("%q %v", link, err)
		}
		if got, err := os.Readlink(link); err != nil || got != want {
			t.Fatalf("links to %q (%v), want %q", got, err, want)
		}
	}
}

func TestLinkHelperRefusesWhatItCannotMake(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkHelper(filepath.Join(blocker, "bin"), "ps"); err == nil {
		t.Error("a directory under a file was made")
	}
	if _, err := LinkHelper(dir, "a/b/c"); err == nil {
		t.Error("a link under a directory that is not there was made")
	}
	executable = func() (string, error) { return "", errors.New("no /proc") }
	t.Cleanup(func() { executable = os.Executable })
	if _, err := LinkHelper(dir, "ps"); err == nil {
		t.Error("a link was made to a binary that cannot be found")
	}
}
