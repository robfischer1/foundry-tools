package casttarget

import (
	"bufio"
	"bytes"
	"context"
	"dagger/foundry-tools/internal/castcache"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is an in-memory bucket that speaks the S3 subset minio-go uses, and
// checks every request carries a signature and the session token, as SeaweedFS
// would.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	auths   []string
	status  int           // when non-zero, every request answers it
	hang    chan struct{} // when set, every request blocks until closed
	puts    int
}

func s3Error(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>`+code+`</Code><Message>`+msg+`</Message></Error>`)
}

// unchunk decodes the aws-chunked body minio-go sends over plain HTTP.
func unchunk(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	var out []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		n, err := strconv.ParseInt(size, 16, 64)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		out = append(out, buf...)
		br.Discard(2)
	}
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
		s3Error(w, f.status, "AccessDenied", "Access Denied.")
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/"+castcache.Bucket+"/")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		b, ok := f.objects[key]
		if !ok {
			s3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		w.Header().Set("ETag", `"abc123"`)
		http.ServeContent(w, r, "", time.Now(), bytes.NewReader(b))
	case http.MethodPut:
		var b []byte
		var err error
		if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
			b, err = unchunk(r.Body)
		} else {
			b, err = io.ReadAll(r.Body)
		}
		if err != nil || r.Header.Get("X-Amz-Content-Sha256") == "" {
			s3Error(w, http.StatusBadRequest, "BadRequest", "bad body")
			return
		}
		f.objects[key] = b
		f.puts++
		w.Header().Set("ETag", `"abc123"`)
	}
}

func newS3(t *testing.T) (*fakeS3, Config) {
	f := &fakeS3{objects: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, Config{
		Repo: "http://door/rob/tongs.git", Toolchain: "rust-1.97.0", Triple: "x86_64-unknown-linux-gnu",
		Dir: t.TempDir(), Endpoint: srv.URL,
		Creds:   castcache.Creds{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST"},
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
			"cast-cache: restore skipped: AccessDenied: Access Denied. (HTTP 403)"},
		"the store is down": {func(f *fakeS3, c *Config) { f.status = 503; c.Timeout = 1500 * time.Millisecond }, "cast-cache: restore skipped:"},
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
		"credentials refused": {func(f *fakeS3, _ *Config) { f.status = 403 }, "cast-cache: save skipped: the upload: AccessDenied: Access Denied. (HTTP 403)"},
		"the store is down":   {func(f *fakeS3, c *Config) { f.status = 500; c.Timeout = 1500 * time.Millisecond }, "cast-cache: save skipped: the upload:"},
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
	if _, err := store("", castcache.Creds{}, nil); err != nil {
		t.Errorf("the default endpoint does not build a client: %v", err)
	}
	if _, err := store("not a url", castcache.Creds{}, nil); err == nil {
		t.Error("a malformed endpoint built a client")
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
