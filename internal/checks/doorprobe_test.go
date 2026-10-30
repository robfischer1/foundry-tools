package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestDoorProbeReadsTheFirstRemoteCopyOfTheFirstNamedContract(t *testing.T) {
	// "zeta" is declared first and sorts last; the walk is by name, so the
	// target is the same on every run: alpha's.
	manifest := `
[contracts.zeta]
copies = [
  { source = { local = "a" } },
  { source = { repo = "only-repo" } },
  { source = { repo = "zr", path = "zp.toml" } },
]

[contracts.alpha]
copies = [{ source = { repo = "ar", path = "ap.toml" } }]
`
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.URL.Query().Get("repo")+":"+r.URL.Query().Get("path"))
	}))
	defer srv.Close()

	code, out := DoorProbe(context.Background(), manifest, Door{Base: srv.URL})
	if code != 0 || !strings.Contains(out, "ar:ap.toml -> HTTP 200") {
		t.Errorf("got %d %q", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || asked[0] != "ar:ap.toml" {
		t.Errorf("asked %v, want one read of ar:ap.toml", asked)
	}
}

func TestDoorProbeFindsACopyInAnArrayOfTables(t *testing.T) {
	manifest := "[[contracts.a.copies]]\nsource = { repo = \"r\", path = \"p\" }\n"
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if code, out := DoorProbe(context.Background(), manifest, Door{Base: srv.URL}); code != 0 || !strings.Contains(out, "r:p") {
		t.Errorf("got %d %q", code, out)
	}
}

func TestDoorProbeLetsTheCheckerJudgeWhatItCannotRead(t *testing.T) {
	ctx := context.Background()
	dead := httptest.NewServer(http.NotFoundHandler())
	base := dead.URL
	dead.Close()
	door := Door{Base: base} // would be a 2 if it were ever asked

	for name, manifest := range map[string]string{
		"unparseable":  "= =",
		"empty":        "",
		"all local":    "[contracts.a]\ncopies = [{ source = { local = \"x\" } }]\n",
		"no contracts": "title = \"x\"\n",
		"no path":      "[contracts.a]\ncopies = [{ source = { repo = \"r\" } }]\n",
		"non-string":   "[contracts.a]\ncopies = [{ source = { repo = 1, path = 2 } }]\n",
	} {
		code, out := DoorProbe(ctx, manifest, door)
		if code != 0 || out == "" {
			t.Errorf("%s: got %d %q, want a pass that says why", name, code, out)
		}
	}
	if _, out := DoorProbe(ctx, "= =", door); !strings.Contains(out, "letting the checker be the judge") {
		t.Errorf("unparseable manifest: %q", out)
	}
	if _, out := DoorProbe(ctx, "", door); !strings.Contains(out, "every declared copy is local") {
		t.Errorf("empty manifest: %q", out)
	}
}

func TestDoorProbeIsTwoOnlyWhenNothingAnswered(t *testing.T) {
	manifest := "[contracts.a]\ncopies = [{ source = { repo = \"r\", path = \"p\" } }]\n"
	ctx := context.Background()

	dead := httptest.NewServer(http.NotFoundHandler())
	base := dead.URL
	dead.Close()
	code, out := DoorProbe(ctx, manifest, Door{Base: base})
	if code != 2 || !strings.Contains(out, "unreachable (r:p)") {
		t.Errorf("dead door: %d %q", code, out)
	}

	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, status := range []int{200, 204, 301, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		code, out := DoorProbe(ctx, manifest, Door{Base: srv.URL, Client: &http.Client{CheckRedirect: noRedirect}})
		srv.Close()
		if code != 0 {
			t.Errorf("status %d: an answer is reachable, got %d %q", status, code, out)
		}
		if want := "reachable (r:p -> HTTP " + strconv.Itoa(status) + ")"; !strings.Contains(out, want) {
			t.Errorf("status %d: report %q lacks %q", status, out, want)
		}
	}
}

// A 5xx is the door failing, not the door answering about a file: the checker's
// fetch would fail the same way and land as a finding, so the probe settles
// could-not-run and names the status.
func TestDoorProbeReadsA5xxAsTheDoorBeingDown(t *testing.T) {
	manifest := "[contracts.a]\ncopies = [{ source = { repo = \"r\", path = \"p\" } }]\n"
	for _, status := range []int{500, 502, 503, 504} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		code, out := DoorProbe(context.Background(), manifest, Door{Base: srv.URL, NoRetry: true})
		srv.Close()
		if code != 2 {
			t.Errorf("status %d: the door failed, want 2, got %d %q", status, code, out)
		}
		if want := "HTTP " + strconv.Itoa(status); !strings.Contains(out, want) || !strings.Contains(out, "r:p") {
			t.Errorf("status %d: report %q must name the target and %q", status, out, want)
		}
	}
}
