package castcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, rel))
	return err == nil
}

func TestPackAndUnpackRoundTripAndPrune(t *testing.T) {
	src := t.TempDir()
	write(t, src, "release/deps/libserde-abc.rlib", "rlib")
	write(t, src, "release/.fingerprint/serde-abc/lib-serde", "fp")
	write(t, src, "release/cerberus", "#!bin")
	write(t, src, "release/incremental/cerberus-1/s-abc.lock", "incr")
	write(t, src, "release/.cargo-lock", "")
	write(t, src, "x86_64-unknown-linux-musl/release/incremental/big", "incr")
	write(t, src, "x86_64-unknown-linux-musl/release/deps/libx.rlib", "x")
	write(t, src, "release/deps/incremental.rs", "not the directory")
	if err := os.Symlink("libserde-abc.rlib", filepath.Join(src, "release/deps/libserde.rlib")); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "release/deps/libserde-abc.rlib"), old, old); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	n, err := Pack(src, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("packed %d regular files, want 5", n)
	}
	dst := t.TempDir()
	got, err := Unpack(&buf, dst)
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Errorf("unpacked %d files, want 5", got)
	}
	for rel, body := range map[string]string{
		"release/deps/libserde-abc.rlib":                   "rlib",
		"release/.fingerprint/serde-abc/lib-serde":         "fp",
		"release/cerberus":                                 "#!bin",
		"x86_64-unknown-linux-musl/release/deps/libx.rlib": "x",
		"release/deps/incremental.rs":                      "not the directory",
	} {
		if read(t, dst, rel) != body {
			t.Errorf("%s did not round-trip", rel)
		}
	}
	for _, rel := range []string{"release/incremental", "x86_64-unknown-linux-musl/release/incremental", "release/.cargo-lock"} {
		if exists(dst, rel) {
			t.Errorf("%s was shipped", rel)
		}
	}
	if l, err := os.Readlink(filepath.Join(dst, "release/deps/libserde.rlib")); err != nil || l != "libserde-abc.rlib" {
		t.Errorf("symlink = %q, %v", l, err)
	}
	// Cargo's freshness reads mtimes: they come back.
	if fi, _ := os.Stat(filepath.Join(dst, "release/deps/libserde-abc.rlib")); !fi.ModTime().Equal(old) {
		t.Errorf("mtime %v, want %v", fi.ModTime(), old)
	}
}

// entry is one member of a hand-made archive.
type entry struct {
	h    tar.Header
	body string
}

// archive is a tar.gz of the given entries, for a malicious or damaged one.
func archive(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		e.h.Size = int64(len(e.body))
		if err := tw.WriteHeader(&e.h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestUnpackRefusesEntriesThatLeaveTheTarget(t *testing.T) {
	outside := t.TempDir()
	for name, entries := range map[string][]entry{
		"dot dot":  {{tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}, "x"}},
		"absolute": {{tar.Header{Name: "/tmp/evil", Typeflag: tar.TypeReg, Mode: 0o644}, "x"}},
		"through a symlink": {
			{tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: outside, Mode: 0o777}, ""},
			{tar.Header{Name: "link/evil", Typeflag: tar.TypeReg, Mode: 0o644}, "x"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dst := t.TempDir()
			if _, err := Unpack(archive(t, entries...), dst); err == nil {
				t.Error("an entry outside the target was written")
			}
			if exists(outside, "evil") {
				t.Error("a file landed outside the target")
			}
		})
	}
}

func TestUnpackReadsTheWholeStream(t *testing.T) {
	src := t.TempDir()
	for i := 0; i < 50; i++ {
		write(t, src, "release/deps/f"+strings.Repeat("x", i), strings.Repeat("payload", 500))
	}
	var buf bytes.Buffer
	if _, err := Pack(src, &buf); err != nil {
		t.Fatal(err)
	}
	whole := buf.Bytes()
	for name, cut := range map[string][]byte{
		"truncated":      whole[:len(whole)/2],
		"trailer cut":    whole[:len(whole)-4],
		"checksum wrong": append(append([]byte{}, whole[:len(whole)-8]...), 1, 2, 3, 4, 5, 6, 7, 8),
		"not gzip":       []byte("<html>502</html>"),
	} {
		if _, err := Unpack(bytes.NewReader(cut), t.TempDir()); err == nil {
			t.Errorf("%s: an unreadable archive unpacked", name)
		}
	}
}
