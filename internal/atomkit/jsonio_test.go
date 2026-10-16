package atomkit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func TestUT_441_MarshalNoHTMLEscape(t *testing.T) {
	b, err := Marshal(checks.Verdict{Atom: "x:y", Reason: "a<b&c", Logs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("a<b&c")) {
		t.Fatalf("reason was escaped: %s", b)
	}
	if !bytes.HasSuffix(b, []byte("}\n")) || bytes.Count(b, []byte("\n")) != 1 {
		t.Fatalf("want exactly one trailing newline: %q", b)
	}
}

func TestUT_443_WriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := WriteFile(p, RP1()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Marshal(RP1())
	if !bytes.Equal(got, want) {
		t.Fatal("file is not Marshal(RP-1)")
	}
	left, _ := filepath.Glob(p + ".tmp.*")
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestWriteFileLeavesNoTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := WriteFile(p, func() {}); err == nil {
		t.Fatal("WriteFile of an unmarshalable value succeeded")
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("directory not empty: %v", es)
	}
}

func TestWriteFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "no", "such", "x.json"), RP1()); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
	// a directory at the target makes the rename fail, and the temp file is cleaned up
	target := filepath.Join(dir, "d")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, RP1()); err == nil {
		t.Fatal("rename over a directory succeeded")
	}
	if left, _ := filepath.Glob(target + ".tmp.*"); len(left) != 0 {
		t.Fatalf("temp left: %v", left)
	}
}
