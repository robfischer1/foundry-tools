package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// doorAsk is one request the fake door saw.
type doorAsk struct{ Repo, Path string }

// fakeDoor stands up an Ourea door that answers the archive read with h and
// points the atoms at it for the length of the test. It answers what the door
// was asked, in order. A test that never reaches the door reads an empty list,
// which is how "the door was not consulted" is asserted.
func fakeDoor(t *testing.T, h http.HandlerFunc) *[]doorAsk {
	t.Helper()
	var mu sync.Mutex
	asks := &[]doorAsk{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/archive" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		*asks = append(*asks, doorAsk{r.URL.Query().Get("repo"), r.URL.Query().Get("path")})
		mu.Unlock()
		h(w, r)
	}))
	prev := oureaDoor
	oureaDoor = checks.Door{Base: srv.URL, Client: srv.Client()}
	t.Cleanup(func() { oureaDoor = prev; srv.Close() })
	return asks
}

// deadDoor points the atoms at an address nothing listens on.
func deadDoor(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	prev := oureaDoor
	oureaDoor = checks.Door{Base: base}
	t.Cleanup(func() { oureaDoor = prev })
}

// wantReport is wantState for an atom whose verdict carries a report: a pass
// keeps its lines in Logs (the Reason is just "<id>: PASS"), anything else in
// the Reason.
func wantReport(t *testing.T, v checks.Verdict, state int, needles ...string) {
	t.Helper()
	if v.State != state {
		t.Fatalf("%s: state %d (%s), want %d\n%s\n%s", v.Atom, v.State, v.Result, state, v.Reason, strings.Join(v.Logs, "\n"))
	}
	text := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(text, n) {
			t.Errorf("%s: report lacks %q:\n%s", v.Atom, n, text)
		}
	}
}
