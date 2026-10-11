package casttarget

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"dagger/foundry-tools/internal/castcache"
)

var errInjected = errors.New("injected failure")

// failWriter fails every write, as a full disk or a closed pipe would.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errInjected }

// seams restores every test seam when the test ends.
func seams(t *testing.T) {
	t.Helper()
	ops, arch, pack, temp, fl, files, out, sock := fsOps, goarch, packTarget, createTemp, flock, secretFiles, credsOut, defaultSocket
	t.Cleanup(func() {
		fsOps, goarch, packTarget, createTemp, flock, secretFiles, credsOut, defaultSocket = ops, arch, pack, temp, fl, files, out, sock
	})
}

// oneFile is a dir holding a regular file and, sorted before it, a symlink to it,
// so the file is the last entry a walk visits.
func oneFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "release/deps/liba.rlib", "artifact")
	if err := os.Symlink("liba.rlib", filepath.Join(dir, "release/deps/a.link")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackFailuresAreReturnedNotSwallowed(t *testing.T) {
	cases := map[string]struct {
		stub func()
		w    io.Writer
		want string // a substring of the error
		is   error
	}{
		"the writer fails at once": {func() {}, failWriter{}, "injected failure", errInjected},
		"an entry's info cannot be read": {func() {
			fsOps.info = func(fs.DirEntry) (fs.FileInfo, error) { return nil, errInjected }
		}, nil, "injected failure", errInjected},
		"a symlink cannot be read": {func() {
			fsOps.readlink = func(string) (string, error) { return "", errInjected }
		}, nil, "injected failure", errInjected},
		"no tar header can be made": {func() {
			fsOps.header = func(fs.FileInfo, string) (*tar.Header, error) { return nil, errInjected }
		}, nil, "injected failure", errInjected},
		"a file cannot be opened": {func() {
			fsOps.open = func(string) (*os.File, error) { return nil, errInjected }
		}, nil, "injected failure", errInjected},
		// A file that grew since its header was cut: tar refuses the extra bytes,
		// and the error names the file.
		"a file grew under the walk": {func() {
			fsOps.header = func(i fs.FileInfo, l string) (*tar.Header, error) {
				h, err := tar.FileInfoHeader(i, l)
				if h != nil && i.Mode().IsRegular() {
					h.Size--
				}
				return h, err
			}
		}, nil, "release/deps/liba.rlib", tar.ErrWriteTooLong},
		// A file that shrank: the last entry is short, and the tar writer says so
		// when it closes.
		"a file shrank under the walk": {func() {
			fsOps.header = func(i fs.FileInfo, l string) (*tar.Header, error) {
				h, err := tar.FileInfoHeader(i, l)
				if h != nil && i.Mode().IsRegular() {
					h.Size += 5
				}
				return h, err
			}
		}, nil, "missed writing 5 bytes", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			seams(t)
			c.stub()
			var w io.Writer = &bytes.Buffer{}
			if c.w != nil {
				w = c.w
			}
			_, err := Pack(oneFile(t), w)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want one containing %q", err, c.want)
			}
			if c.is != nil && !errors.Is(err, c.is) {
				t.Errorf("error %v does not wrap %v", err, c.is)
			}
		})
	}
}

func TestPackOfAMissingDirectoryIsAnError(t *testing.T) {
	n, err := Pack(filepath.Join(t.TempDir(), "gone"), &bytes.Buffer{})
	if err == nil || n != 0 {
		t.Errorf("packed %d files, error %v", n, err)
	}
}

// Only directories, regular files and symlinks are carried: a FIFO in a
// target would block a reader, and a socket has no tar form.
func TestPackLeavesOutFilesThatAreNotFilesDirsOrSymlinks(t *testing.T) {
	dir := oneFile(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "release", "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	n, err := Pack(dir, &buf)
	if err != nil || n != 1 {
		t.Fatalf("packed %d files, error %v", n, err)
	}
	dst := t.TempDir()
	if _, err := Unpack(&buf, dst); err != nil {
		t.Fatal(err)
	}
	if exists(dst, "release/pipe") || !exists(dst, "release/deps/liba.rlib") {
		t.Error("the FIFO was carried, or the file was not")
	}
}

func TestUnpackFailuresAreReturned(t *testing.T) {
	reg := func(name string) entry { return entry{tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}, "x"} }
	cases := map[string]struct {
		dir     func(t *testing.T) string
		entries []entry
		want    string
	}{
		"the target does not exist": {func(t *testing.T) string { return filepath.Join(t.TempDir(), "gone") }, []entry{reg("a")}, "no such file"},
		// "a" is a file, so "a/b" has no directory to live in.
		"a path through a file": {func(t *testing.T) string { return t.TempDir() }, []entry{reg("a"), reg("a/b")}, "not a directory"},
		// "d" is a directory by the time a file wants its name.
		"a file over a directory": {func(t *testing.T) string { return t.TempDir() },
			[]entry{{tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}, ""}, reg("d")}, "is a directory"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Unpack(archive(t, c.entries...), c.dir(t))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestSaveFailuresEachSayWhyAndStoreNothing(t *testing.T) {
	const pre = "cast-cache: save skipped: "
	cases := map[string]struct {
		stub func(c *Config)
		want string
	}{
		"an architecture with no triple": {func(c *Config) { goarch, c.Triple = "riscv64", "" }, pre + "no Rust target triple is known for GOARCH=riscv64"},
		"the endpoint is not a URL":      {func(c *Config) { c.Endpoint = "not a url" }, pre + `the endpoint "not a url" is not a URL`},
		"a lock file that cannot open": {func(c *Config) {
			os.Remove(filepath.Join(c.Dir, "release", ".cargo-lock"))
			os.Mkdir(filepath.Join(c.Dir, "release", ".cargo-lock"), 0o755)
		}, pre + "open "},
		"flock fails for another reason": {func(*Config) { flock = func(int, int) error { return syscall.EBADF } }, pre + "bad file descriptor"},
		"no temp file":                   {func(*Config) { createTemp = func(string, string) (*os.File, error) { return nil, errInjected } }, pre + "injected failure"},
		"packing fails": {func(*Config) {
			packTarget = func(string, io.Writer) (int, error) { return 0, errInjected }
		}, pre + "packing the target: injected failure"},
		// The archive's file is closed under Save, so rewinding it fails.
		"the archive cannot be rewound": {func(*Config) {
			createTemp = func(dir, pattern string) (*os.File, error) {
				f, err := os.CreateTemp(dir, pattern)
				if err == nil {
					f.Close()
				}
				return f, err
			}
			packTarget = func(string, io.Writer) (int, error) { return castcache.MinFiles, nil }
		}, pre + "seek "},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			seams(t)
			f, cfg := newS3(t)
			target(t, cfg.Dir, 30)
			c.stub(&cfg)
			if line := Save(context.Background(), cfg); !strings.HasPrefix(line, c.want) {
				t.Errorf("line %q\nwant prefix %q", line, c.want)
			}
			if len(f.objects) != 0 {
				t.Errorf("a failed save stored %d object(s)", len(f.objects))
			}
		})
	}
}

func TestRestoreFailuresEachSayWhyAndLeaveTheTargetEmpty(t *testing.T) {
	const pre = "cast-cache: restore skipped: "
	cases := map[string]struct {
		stub func(c *Config)
		want string
	}{
		"an architecture with no triple": {func(c *Config) { goarch, c.Triple = "riscv64", "" }, pre + "no Rust target triple is known for GOARCH=riscv64"},
		"the target directory is gone":   {func(c *Config) { c.Dir = filepath.Join(c.Dir, "gone") }, pre + "the target directory: "},
		"the endpoint is not a URL":      {func(c *Config) { c.Endpoint = "not a url" }, pre + `the endpoint "not a url" is not a URL`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			seams(t)
			_, cfg := newS3(t)
			c.stub(&cfg)
			if line := Restore(context.Background(), cfg); !strings.HasPrefix(line, c.want) {
				t.Errorf("line %q\nwant prefix %q", line, c.want)
			}
		})
	}
}

func TestLockTargetNamesAnUnexpectedFlockError(t *testing.T) {
	seams(t)
	flock = func(int, int) error { return syscall.EBADF }
	dir := t.TempDir()
	write(t, dir, "release/.cargo-lock", "")
	if _, err := lockTarget(dir); !errors.Is(err, syscall.EBADF) {
		t.Errorf("error %v, want EBADF (and not the busy-target message)", err)
	}
}

// fetch refuses a name the store would not accept before asking it anything.
func TestFetchOfAnInvalidKeyIsAnErrorLine(t *testing.T) {
	cl, err := store("http://127.0.0.1:1", castcache.Creds{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := fetch(context.Background(), cl, "")
	if obj != nil || err == nil || !strings.Contains(err.Error(), "Object name cannot be empty (HTTP 400)") {
		t.Errorf("object %v, error %v", obj, err)
	}
}

func TestEndpointOfDefaultsToSeaweedFS(t *testing.T) {
	if endpointOf("") != castcache.DefaultEndpoint || endpointOf("http://x:1") != "http://x:1" {
		t.Error("endpointOf does not default an empty endpoint and keep a given one")
	}
}
