package castcache

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is an in-memory bucket that checks every request carries a signature
// and the session token, as SeaweedFS would.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	auths   []string
	status  int           // when non-zero, every request answers it
	hang    chan struct{} // when set, every request blocks until closed
	puts    int
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.hang != nil {
		select {
		case <-f.hang:
		case <-r.Context().Done():
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auths = append(f.auths, r.Header.Get("Authorization")+"|"+r.Header.Get("X-Amz-Security-Token"))
	if f.status != 0 {
		w.WriteHeader(f.status)
		io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>Access Denied.</Message></Error>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/"+Bucket+"/")
	switch r.Method {
	case http.MethodGet:
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(b)
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		if int64(len(b)) != r.ContentLength || r.Header.Get("X-Amz-Content-Sha256") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.objects[key] = b
		f.puts++
	}
}

func newS3(t *testing.T) (*fakeS3, Config) {
	f := &fakeS3{objects: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, Config{
		Repo: "http://door/rob/tongs.git", Toolchain: "rust-1.97.0", Triple: "x86_64-unknown-linux-gnu",
		Dir: t.TempDir(), Endpoint: srv.URL,
		Creds:   Creds{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"},
		Timeout: 5 * time.Second,
	}
}

const tongsKey = "tongs/rust-1.97.0/x86_64-unknown-linux-gnu.tar.gz"

// target fills dir with n deps files and cargo's lock, as a finished build.
func target(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		write(t, dir, "release/deps/lib"+strings.Repeat("a", i+1)+".rlib", "artifact")
	}
	write(t, dir, "release/.cargo-lock", "")
	write(t, dir, "release/incremental/x/y", "dropped")
}

func TestSaveThenRestoreRoundTripsThroughTheBucket(t *testing.T) {
	f, c := newS3(t)
	target(t, c.Dir, 30)
	line := Save(context.Background(), c)
	if !strings.HasPrefix(line, "cast-cache: saved s3://cast-cache/"+tongsKey+": 30 files, ") || !strings.Contains(line, "MiB archive") {
		t.Fatalf("save line %q", line)
	}
	if f.puts != 1 || len(f.objects[tongsKey]) == 0 {
		t.Fatalf("%d puts, objects %v", f.puts, f.objects)
	}
	// A second save overwrites the same key: one archive per repo+toolchain+triple.
	Save(context.Background(), c)
	if len(f.objects) != 1 || f.puts != 2 {
		t.Errorf("%d objects after two saves (%d puts)", len(f.objects), f.puts)
	}
	for _, a := range f.auths {
		if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AK/") || !strings.HasSuffix(a, "|ST") {
			t.Errorf("an unsigned request or one without the session token: %q", a)
		}
	}

	fresh := c
	fresh.Dir = t.TempDir()
	line = Restore(context.Background(), fresh)
	if !strings.HasPrefix(line, "cast-cache: restored s3://cast-cache/"+tongsKey+": 30 files from a ") {
		t.Fatalf("restore line %q", line)
	}
	if read(t, fresh.Dir, "release/deps/libaaa.rlib") != "artifact" {
		t.Error("the target did not come back")
	}
	if exists(fresh.Dir, "release/incremental") {
		t.Error("incremental was shipped")
	}
}

func TestRestoreDegradesToAColdBuild(t *testing.T) {
	type tc struct {
		prep func(f *fakeS3, c *Config)
		want string
	}
	cases := map[string]tc{
		"no object yet": {func(*fakeS3, *Config) {}, "cast-cache: restore skipped: no archive at s3://cast-cache/" + tongsKey},
		"credentials refused": {func(f *fakeS3, _ *Config) { f.status = 403 },
			"cast-cache: restore skipped: GET answered HTTP 403: <Error><Code>AccessDenied</Code>"},
		"the store is down": {func(f *fakeS3, _ *Config) { f.status = 503 }, "cast-cache: restore skipped: GET answered HTTP 503"},
		"nothing listens":   {func(_ *fakeS3, c *Config) { c.Endpoint = "http://127.0.0.1:1" }, "cast-cache: restore skipped:"},
		"a hung store": {func(f *fakeS3, c *Config) {
			f.hang = make(chan struct{})
			c.Timeout = 100 * time.Millisecond
		}, "cast-cache: restore skipped:"},
		"no repo": {func(_ *fakeS3, c *Config) { c.Repo = "" }, "cast-cache: restore skipped: no repository"},
		"a warm volume": {func(_ *fakeS3, c *Config) { write(t, c.Dir, "release/deps/warm.rlib", "w") },
			"cast-cache: restore skipped: the engine's volume is already warm (1 entries)"},
		"a damaged archive": {func(f *fakeS3, _ *Config) { f.objects[tongsKey] = []byte("not gzip at all") },
			"cast-cache: restore skipped: s3://cast-cache/" + tongsKey + " was unreadable"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f, cfg := newS3(t)
			c.prep(f, &cfg)
			if f.hang != nil {
				defer close(f.hang)
			}
			before, _ := os.ReadDir(cfg.Dir)
			line := Restore(context.Background(), cfg)
			if !strings.HasPrefix(line, c.want) {
				t.Errorf("line %q\nwant prefix %q", line, c.want)
			}
			// "SK" and "ST" are the test's secret key and session token.
			if strings.Contains(line, "SK") || strings.Contains(line, "|ST") || strings.Contains(line, "Signature=") {
				t.Errorf("a credential or signature reached the log line: %q", line)
			}
			after, _ := os.ReadDir(cfg.Dir)
			if name != "a warm volume" && len(after) != 0 {
				t.Errorf("a failed restore left %d entries behind", len(after))
			}
			if name == "a warm volume" && len(after) != len(before) {
				t.Error("a warm volume was touched")
			}
		})
	}
}

func TestAPartialRestoreIsWipedNotLeftHalfFilled(t *testing.T) {
	f, c := newS3(t)
	target(t, c.Dir, 25)
	Save(context.Background(), c)
	whole := f.objects[tongsKey]
	f.objects[tongsKey] = whole[:len(whole)*2/3]
	fresh := c
	fresh.Dir = t.TempDir()
	line := Restore(context.Background(), fresh)
	if !strings.Contains(line, "was unreadable after") || !strings.Contains(line, "the target was cleared, building cold") {
		t.Errorf("line %q", line)
	}
	if entries, _ := os.ReadDir(fresh.Dir); len(entries) != 0 {
		t.Errorf("%d entries survived a damaged restore", len(entries))
	}
}

func TestSaveDegradesAndNeverFails(t *testing.T) {
	cases := map[string]struct {
		prep func(f *fakeS3, c *Config)
		want string
	}{
		"credentials refused": {func(f *fakeS3, _ *Config) { f.status = 403 }, "cast-cache: save skipped: the upload: PUT answered HTTP 403"},
		"the store is down":   {func(f *fakeS3, _ *Config) { f.status = 500 }, "cast-cache: save skipped: the upload: PUT answered HTTP 500"},
		"nothing listens":     {func(_ *fakeS3, c *Config) { c.Endpoint = "http://127.0.0.1:1" }, "cast-cache: save skipped: the upload:"},
		"a hung store": {func(f *fakeS3, c *Config) {
			f.hang = make(chan struct{})
			c.Timeout = 100 * time.Millisecond
		}, "cast-cache: save skipped: the upload:"},
		"too few files": {func(_ *fakeS3, c *Config) { os.RemoveAll(filepath.Join(c.Dir, "release", "deps")); target(t, c.Dir, 3) },
			"cast-cache: save skipped: the target holds 3 file(s), too few"},
		"no cargo build": {func(_ *fakeS3, c *Config) { os.RemoveAll(c.Dir); os.MkdirAll(c.Dir, 0o755) },
			"cast-cache: save skipped: the target holds no cargo build"},
		"no repo": {func(_ *fakeS3, c *Config) { c.Repo = "" }, "cast-cache: save skipped: no repository"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f, cfg := newS3(t)
			target(t, cfg.Dir, 30)
			c.prep(f, &cfg)
			if f.hang != nil {
				defer close(f.hang)
			}
			line := Save(context.Background(), cfg)
			if !strings.HasPrefix(line, c.want) {
				t.Errorf("line %q\nwant prefix %q", line, c.want)
			}
			if len(f.objects) != 0 {
				t.Errorf("a skipped save stored %d object(s)", len(f.objects))
			}
		})
	}
}

// An archive is never cut from a target another cargo is building in.
func TestSaveSkipsATargetAnotherBuildHolds(t *testing.T) {
	f, c := newS3(t)
	target(t, c.Dir, 30)
	unlock, err := lockTarget(c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	line := Save(context.Background(), c)
	unlock()
	if !strings.HasPrefix(line, "cast-cache: save skipped: another build holds the target's lock") || len(f.objects) != 0 {
		t.Errorf("line %q, %d objects", line, len(f.objects))
	}
	if line := Save(context.Background(), c); !strings.HasPrefix(line, "cast-cache: saved ") {
		t.Errorf("a released lock still blocked the save: %q", line)
	}
}

func TestSaveLeavesTheTargetAsItFoundIt(t *testing.T) {
	_, c := newS3(t)
	target(t, c.Dir, 30)
	Save(context.Background(), c)
	if !exists(c.Dir, "release/incremental/x/y") || !exists(c.Dir, "release/deps/libaaa.rlib") {
		t.Error("a save changed the target")
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{Repo: "http://x/a.git", Toolchain: "t"}
	if key, err := c.key(); err != nil || !strings.HasPrefix(key, "a/t/") || !strings.HasSuffix(key, "-unknown-linux-gnu.tar.gz") {
		t.Errorf("key %q, %v", key, err)
	}
	if s := c.s3(); s.Endpoint != DefaultEndpoint || s.HTTP == nil {
		t.Errorf("s3 %+v", s)
	}
	ctx, cancel := c.deadline(context.Background())
	defer cancel()
	if d, ok := ctx.Deadline(); !ok || time.Until(d) < 9*time.Minute {
		t.Errorf("default deadline %v", d)
	}
	if size(3<<20) != "3.0 MiB" {
		t.Errorf("size %q", size(3<<20))
	}
}
