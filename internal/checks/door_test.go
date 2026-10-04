package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTheDoorURLIsTheArchiveReadWithLiteralSlashes(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://git.notusmi.com", "https://git.notusmi.com/archive?repo=foundry/foundry-dies&path=orbits/a-b.toml"},
		{"https://git.notusmi.com/", "https://git.notusmi.com/archive?repo=foundry/foundry-dies&path=orbits/a-b.toml"},
	} {
		if got := (Door{Base: tc.base}).URL(OrbitsRepo, "orbits/a-b.toml"); got != tc.want {
			t.Errorf("Base %q: got %s, want %s", tc.base, got, tc.want)
		}
	}
	// Anything that is not a slash is still escaped: a path cannot smuggle a
	// second parameter into the read.
	if got := (Door{Base: "http://d"}).URL("r", "a b&c=d"); got != "http://d/archive?repo=r&path=a+b%26c%3Dd" {
		t.Errorf("unescaped path: %s", got)
	}
	if NewDoor().Base != "https://git.notusmi.com" || OrbitsRepo != "foundry/foundry-dies" {
		t.Errorf("the production door is %q / %q", NewDoor().Base, OrbitsRepo)
	}
}

func TestDoorGetAnswersAnyStatusAndTheBodyUndecoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("path") {
		case "ok":
			if r.Header.Get("Authorization") != "" || r.Method != http.MethodGet {
				t.Errorf("the read is anonymous and a GET: %s %v", r.Method, r.Header)
			}
			_, _ = w.Write([]byte("bytes\xff"))
		case "moved":
			http.Redirect(w, r, "/archive?repo=r&path=ok", http.StatusMovedPermanently)
		default:
			http.Error(w, "gone", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	d := Door{Base: srv.URL}
	ctx := context.Background()

	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"ok", 200, "bytes\xff"},
		{"moved", 200, "bytes\xff"}, // redirects are followed
		{"absent", 404, "gone\n"},
	} {
		status, body, err := d.Get(ctx, "r", tc.path)
		if err != nil || status != tc.status || string(body) != tc.body {
			t.Errorf("%s: got %d %q %v, want %d %q", tc.path, status, body, err, tc.status, tc.body)
		}
	}
}

func TestDoorGetReportsWhatKeptItFromAsking(t *testing.T) {
	ctx := context.Background()

	if _, _, err := (Door{Base: "http://bad host\x7f"}).Get(ctx, "r", "p"); err == nil {
		t.Error("an address that is not a URL must be an error")
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	if _, _, err := (Door{Base: dead}).Get(ctx, "r", "p"); err == nil {
		t.Error("a door nothing listens at must be an error")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	d := Door{Base: slow.URL, Client: &http.Client{Timeout: 50 * time.Millisecond}}
	if _, _, err := d.Get(ctx, "r", "p"); err == nil || !strings.Contains(err.Error(), "Timeout") {
		t.Errorf("a door that never answers must time out, got %v", err)
	}

	// A body cut short is an error, not a short contract.
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
			}
		}
	}))
	defer cut.Close()
	status, body, err := (Door{Base: cut.URL}).Get(ctx, "r", "p")
	if err == nil || body != nil || status != 200 {
		t.Errorf("a truncated body must be an error with no body, got %d %q %v", status, body, err)
	}
}

func TestTheDefaultClientIsBoundedByTheDoorTimeout(t *testing.T) {
	if DoorTimeout != 30*time.Second {
		t.Errorf("the door's patience was thirty seconds, it is %v", DoorTimeout)
	}
}
