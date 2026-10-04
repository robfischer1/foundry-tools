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

func TestDoorProbeReadsTheManifestsFirstRemoteCopyInDocumentOrder(t *testing.T) {
	// "zeta" is declared first and sorts last; a Go map would visit "alpha".
	manifest := `
[meta.alpha]
note = "a table that is not a contract, named like one"

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
	if code != 0 || !strings.Contains(out, "zr:zp.toml -> HTTP 200") {
		t.Errorf("got %d %q", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || asked[0] != "zr:zp.toml" {
		t.Errorf("asked %v, want one read of zr:zp.toml", asked)
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

	for _, status := range []int{200, 204, 301, 404, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		code, out := DoorProbe(ctx, manifest, Door{Base: srv.URL, Client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
		srv.Close()
		if code != 0 {
			t.Errorf("status %d: an answer is reachable, got %d %q", status, code, out)
		}
		if want := "reachable (r:p -> HTTP " + strconv.Itoa(status) + ")"; !strings.Contains(out, want) {
			t.Errorf("status %d: report %q lacks %q", status, out, want)
		}
	}
}
