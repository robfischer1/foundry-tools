package checks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTheDoorURLIsTheArchiveReadWithLiteralSlashes(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://git.notusmi.com", "https://git.notusmi.com/archive?repo=foundry/foundry-dies&path=orbits/a-b.toml"},
		{"https://git.notusmi.com/", "https://git.notusmi.com/archive?repo=foundry/foundry-dies&path=orbits/a-b.toml"},
	} {
		if got := (Door{Base: tc.base}).URL("foundry/foundry-dies", "orbits/a-b.toml"); got != tc.want {
			t.Errorf("Base %q: got %s, want %s", tc.base, got, tc.want)
		}
	}
	// Anything that is not a slash is still escaped: a path cannot smuggle a
	// second parameter into the read.
	if got := (Door{Base: "http://d"}).URL("r", "a b&c=d"); got != "http://d/archive?repo=r&path=a+b%26c%3Dd" {
		t.Errorf("unescaped path: %s", got)
	}
	if NewDoor().Base != "https://git.notusmi.com" {
		t.Errorf("the production door is %q", NewDoor().Base)
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

	if _, _, err := (Door{Base: "http://bad host\x7f", NoRetry: true}).Get(ctx, "r", "p"); err == nil {
		t.Error("an address that is not a URL must be an error")
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	if _, _, err := (Door{Base: dead, NoRetry: true}).Get(ctx, "r", "p"); err == nil {
		t.Error("a door nothing listens at must be an error")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	d := Door{Base: slow.URL, Client: &http.Client{Timeout: 50 * time.Millisecond}, NoRetry: true}
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
	status, body, err := (Door{Base: cut.URL, NoRetry: true}).Get(ctx, "r", "p")
	if err == nil || body != nil || status != 200 {
		t.Errorf("a truncated body must be an error with no body, got %d %q %v", status, body, err)
	}
}

func TestTheDefaultClientIsBoundedByTheDoorTimeout(t *testing.T) {
	if DoorTimeout != 30*time.Second {
		t.Errorf("the door's patience was thirty seconds, it is %v", DoorTimeout)
	}
}

// doorSchedule swaps the door's sleep and jitter for the test and answers the
// waits it was asked for. A draw of 0.5 is no jitter.
func doorSchedule(t *testing.T, draw float64) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	oldSleep, oldRand := doorSleep, doorRand
	doorSleep = func(_ context.Context, d time.Duration) { waits = append(waits, d) }
	doorRand = func() float64 { return draw }
	t.Cleanup(func() { doorSleep, doorRand = oldSleep, oldRand })
	return &waits
}

func TestDoorGetAsksAgainAfterATransient5xx(t *testing.T) {
	waits := doorSchedule(t, 0.5)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("contract"))
	}))
	defer srv.Close()
	status, body, err := (Door{Base: srv.URL}).Get(context.Background(), "r", "p")
	if err != nil || status != 200 || string(body) != "contract" || calls != 3 {
		t.Fatalf("got %d %q %v after %d calls, want 200 contract after 3", status, body, err, calls)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; !slices.Equal(*waits, want) {
		t.Errorf("waited %v, want %v", *waits, want)
	}
}

func TestDoorGetGivesUpAfterTheFourWaitsAndAnswersTheLastStatus(t *testing.T) {
	waits := doorSchedule(t, 0.5)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	status, _, err := (Door{Base: srv.URL}).Get(context.Background(), "r", "p")
	if err != nil || status != 502 || calls != 5 {
		t.Fatalf("got %d %v after %d calls, want 502 after 5", status, err, calls)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}; !slices.Equal(*waits, want) {
		t.Errorf("waited %v, want %v", *waits, want)
	}
}

func TestDoorGetNeverRetriesAnAnswerThatIsFinal(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusBadRequest, http.StatusForbidden, 200} {
		waits := doorSchedule(t, 0.5)
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(code)
		}))
		status, _, err := (Door{Base: srv.URL}).Get(context.Background(), "r", "p")
		srv.Close()
		if err != nil || status != code || calls != 1 || len(*waits) != 0 {
			t.Errorf("%d: got %d %v after %d calls and %v waits, want one call", code, status, err, calls, *waits)
		}
	}
}

func TestDoorGetAsksAgainAfterARefusedConnection(t *testing.T) {
	waits := doorSchedule(t, 0.5)
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	_, _, err := (Door{Base: dead}).Get(context.Background(), "r", "p")
	if err == nil || len(*waits) != len(DoorWaits) {
		t.Errorf("a refused door: err %v after %v, want an error after %d waits", err, *waits, len(DoorWaits))
	}
}

func TestDoorGetJittersEachWaitThirtyPercentEitherWay(t *testing.T) {
	for _, tc := range []struct {
		draw float64
		want time.Duration
	}{{0, 700 * time.Millisecond}, {1, 1300 * time.Millisecond}} {
		waits := doorSchedule(t, tc.draw)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusServiceUnavailable)
		}))
		_, _, _ = (Door{Base: srv.URL}).Get(context.Background(), "r", "p")
		srv.Close()
		if len(*waits) == 0 || (*waits)[0] != tc.want {
			t.Errorf("draw %v: first wait %v, want %v", tc.draw, *waits, tc.want)
		}
	}
}

func TestDoorGetStopsAskingWhenTheCallerGaveUp(t *testing.T) {
	waits := doorSchedule(t, 0.5)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		cancel()
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, _, _ = (Door{Base: srv.URL}).Get(ctx, "r", "p")
	if calls != 1 || len(*waits) != 0 {
		t.Errorf("a cancelled caller was asked for %d calls and %v waits", calls, *waits)
	}
}

func TestDoorNoRetryAsksOnce(t *testing.T) {
	waits := doorSchedule(t, 0.5)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	status, _, _ := (Door{Base: srv.URL, NoRetry: true}).Get(context.Background(), "r", "p")
	if status != 503 || calls != 1 || len(*waits) != 0 {
		t.Errorf("NoRetry: %d after %d calls and %v waits", status, calls, *waits)
	}
}

func TestTransientDoorClassifiesTheErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{"dns", 0, &net.DNSError{Err: "no such host"}, true},
		{"refused", 0, fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{"reset", 0, fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{"cut body", 200, io.ErrUnexpectedEOF, true},
		{"deadline", 0, context.DeadlineExceeded, true},
		{"timeout", 0, &net.OpError{Err: timeoutErr{}}, true},
		{"not a timeout", 0, &net.OpError{Err: errors.New("boom")}, false},
		{"bad url", 0, errors.New("invalid control character in URL"), false},
		{"500", 500, nil, true},
		{"404", 404, nil, false},
		{"200", 200, nil, false},
	} {
		if got := transientDoor(tc.status, tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }
