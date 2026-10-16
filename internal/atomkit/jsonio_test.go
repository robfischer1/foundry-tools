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
	// the directory is made, as the pod's /work/out is by whoever runs first
	made := filepath.Join(dir, "no", "such", "x.json")
	if err := WriteFile(made, RP1()); err != nil {
		t.Fatalf("write into a missing directory: %v", err)
	}
	if _, err := os.Stat(made); err != nil {
		t.Fatal(err)
	}
	// a file where a directory is needed cannot be made into one
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(dir, "file", "x.json"), RP1()); err == nil {
		t.Fatal("write beneath a regular file succeeded")
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
