package castcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Config is one restore or save.
type Config struct {
	Repo      string // the repo URL the lane was constructed on
	Toolchain string // ToolchainKey of the lane's image
	Dir       string // the release target directory
	Endpoint  string
	Creds     Creds
	Triple    string        // empty: this platform's
	Timeout   time.Duration // empty: 10 minutes
	HTTP      *http.Client  // nil: a client with dial and header timeouts
	Now       func() time.Time
}

func (c Config) key() (string, error) {
	triple := c.Triple
	if triple == "" {
		var err error
		if triple, err = Triple(runtime.GOARCH); err != nil {
			return "", err
		}
	}
	return Key(c.Repo, c.Toolchain, triple)
}

func (c Config) s3() S3 {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			ResponseHeaderTimeout: 2 * time.Minute,
		}}
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return S3{Endpoint: endpoint, Creds: c.Creds, HTTP: client, Now: c.Now}
}

func (c Config) deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	d := c.Timeout
	if d <= 0 {
		d = 10 * time.Minute
	}
	return context.WithTimeout(ctx, d)
}

// Restore unpacks the repo's archive into Dir, and answers the one line the
// lane logs. It never fails: every way it cannot restore is a line beginning
// "cast-cache: restore skipped:" and a target left empty for a cold build.
//
// A warm Dir is left alone, the engine's volume being newer than any archive.
func Restore(ctx context.Context, c Config) string {
	key, err := c.key()
	if err != nil {
		return skip("restore", err.Error())
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return skip("restore", "the target directory: "+err.Error())
	}
	if n := countEntries(entries); n > 0 {
		return skip("restore", fmt.Sprintf("the engine's volume is already warm (%d entries); a warm engine beats the archive", n))
	}
	start := time.Now()
	ctx, cancel := c.deadline(ctx)
	defer cancel()
	resp, err := c.s3().Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return skip("restore", "no archive at "+URI(key)+" yet; building cold")
	}
	if err != nil {
		return skip("restore", err.Error())
	}
	defer resp.Body.Close()
	body := &counter{r: resp.Body}
	files, err := Unpack(body, c.Dir)
	if err != nil {
		wipe(c.Dir)
		return skip("restore", fmt.Sprintf("%s was unreadable after %d file(s) (%v); the target was cleared, building cold", URI(key), files, err))
	}
	return fmt.Sprintf("cast-cache: restored %s: %d files from a %s archive in %s", URI(key), files, size(body.n), time.Since(start).Round(100*time.Millisecond))
}

// Save archives Dir to the repo's key, overwriting the last, and answers the
// one line the lane logs ("cast-cache: save skipped: …" when it did not).
// It never fails, and it takes no more than Timeout.
func Save(ctx context.Context, c Config) string {
	key, err := c.key()
	if err != nil {
		return skip("save", err.Error())
	}
	unlock, err := lockTarget(c.Dir)
	if err != nil {
		return skip("save", err.Error())
	}
	defer unlock()
	ctx, cancel := c.deadline(ctx)
	defer cancel()
	tmp, err := os.CreateTemp("", "castcache-*.tar.gz")
	if err != nil {
		return skip("save", err.Error())
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	start := time.Now()
	sum := sha256.New()
	files, err := Pack(c.Dir, io.MultiWriter(tmp, sum))
	unlock() // the upload reads the archive, not the target
	if err != nil {
		return skip("save", "packing the target: "+err.Error())
	}
	if files < MinFiles {
		return skip("save", fmt.Sprintf("the target holds %d file(s), too few to be a build worth shipping", files))
	}
	n, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return skip("save", err.Error())
	}
	if n > MaxArchive {
		return skip("save", fmt.Sprintf("the archive is %s, past the single-upload limit", size(n)))
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return skip("save", err.Error())
	}
	packed := time.Since(start)
	if err := c.s3().Put(ctx, key, tmp, n, hex.EncodeToString(sum.Sum(nil))); err != nil {
		return skip("save", "the upload: "+err.Error())
	}
	return fmt.Sprintf("cast-cache: saved %s: %d files, %s archive (packed in %s, uploaded in %s)", URI(key), files, size(n), packed.Round(100*time.Millisecond), (time.Since(start) - packed).Round(100*time.Millisecond))
}

func skip(verb, why string) string { return "cast-cache: " + verb + " skipped: " + why }

// countEntries is the entries of a fresh volume's root, less the lost+found a
// filesystem root carries.
func countEntries(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if e.Name() != "lost+found" {
			n++
		}
	}
	return n
}

// wipe removes everything under dir, leaving dir (a mount point).
func wipe(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// lockTarget takes cargo's own build-directory locks (<dir>/<profile>/.cargo-lock)
// without waiting, so an archive is never cut from a target another build is
// writing. It refuses an empty target.
func lockTarget(dir string) (func(), error) {
	locks, _ := filepath.Glob(filepath.Join(dir, "*", ".cargo-lock"))
	cross, _ := filepath.Glob(filepath.Join(dir, "*", "*", ".cargo-lock"))
	locks = append(locks, cross...)
	if len(locks) == 0 {
		return nil, errors.New("the target holds no cargo build to archive")
	}
	var held []*os.File
	release := func() {
		for _, f := range held {
			f.Close()
		}
	}
	for _, l := range locks {
		f, err := os.OpenFile(l, os.O_RDWR, 0)
		if err != nil {
			release()
			return nil, err
		}
		held = append(held, f)
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			release()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, errors.New("another build holds the target's lock, so it is not archived mid-write")
			}
			return nil, err
		}
	}
	return release, nil
}

type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func size(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
