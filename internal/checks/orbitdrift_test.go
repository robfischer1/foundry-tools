package checks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// doorServing answers the archive read from a path -> body map.
func doorServing(t *testing.T, bodies map[string]string) Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Query().Get("path")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return Door{Base: srv.URL}
}

func TestOrbitDriftDecidesEachEdgeByItsOwnDigest(t *testing.T) {
	const contract = "verbs = [\"a\"]\n"
	good := digestOf(contract)
	door := doorServing(t, map[string]string{"orbits/c.toml": contract})
	edge := func(extra string) string { return "[[consumes]]\nfrom = \"p\"\ncontract = \"c\"\n" + extra }

	for _, tc := range []struct {
		name  string
		orbit string
		state int
		has   []string
		not   []string
	}{
		{"agreement", edge("digest = \"" + good + "\"\n"), 0, []string{"1 seam(s) agree"}, []string{"contract moved"}},
		{"two agree", edge("digest = \""+good+"\"\n") + edge("digest = \""+good+"\"\n"), 0, []string{"2 seam(s) agree"}, nil},
		{"drift", edge("digest = \"sha256:x\"\n"), 1, []string{"orbit-drift: p: contract 'c' hashes to " + good}, []string{"compared nothing"}},
		{"unpinned", edge(""), 1, []string{"carries no digest", "compared nothing"}, []string{"Re-lay"}},
		{"drift beats nothing but both are reported", edge("digest = \"sha256:x\"\n") + edge(""), 1,
			[]string{"hashes to", "carries no digest", "Re-lay", "compared nothing"}, nil},
		{"producer peer is read from `to`", "[[produces]]\nto = \"q\"\ncontract = \"c\"\ndigest = \"sha256:x\"\n", 1, []string{"q: contract 'c'"}, nil},
		{"peer falls to ? when neither is named", "[[produces]]\ncontract = \"c\"\n", 1, []string{"?: contract 'c' carries no digest"}, nil},
		{"empty from falls to to", "[[consumes]]\nfrom = \"\"\nto = \"t\"\ncontract = \"c\"\n", 1, []string{"t: contract 'c'"}, nil},
		{"non-string contract is no contract", "[[consumes]]\nfrom = \"p\"\ncontract = 5\n", 1, []string{"consumes p names no contract"}, nil},
		{"a non-table entry is not an edge", "consumes = [1, { from = \"p\" }]\n", 1, []string{"consumes p names no contract"}, nil},
		{"a scalar where edges go declares none", "consumes = 3\n", 0, []string{"declares no seams"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, out := OrbitDrift(context.Background(), tc.orbit, door)
			if state != tc.state {
				t.Fatalf("state %d, want %d\n%s", state, tc.state, out)
			}
			for _, h := range tc.has {
				if !strings.Contains(out, h) {
					t.Errorf("report lacks %q:\n%s", h, out)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(out, n) {
					t.Errorf("report should not say %q:\n%s", n, out)
				}
			}
		})
	}
}

func TestOrbitDriftSettlesWhatItCannotReadAsCannotRun(t *testing.T) {
	edge := "[[consumes]]\nfrom = \"p\"\ncontract = \"c\"\ndigest = \"sha256:x\"\n"
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		has    string
	}{
		{"server error", 500, "x", "answered HTTP 500"},
		{"redirect that was not followed", 304, "", "answered HTTP 304"},
		{"login wall", 200, "<html>", "did not answer a TOML document"},
		{"no verbs", 200, "k = 1", "carries no verbs key"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		state, out := OrbitDrift(ctx, edge, Door{Base: srv.URL})
		srv.Close()
		if state != 2 || !strings.Contains(out, "CANNOT RUN") || !strings.Contains(out, tc.has) {
			t.Errorf("%s: got %d %q", tc.name, state, out)
		}
	}

	// A 2xx other than 200 is a body like any other.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("verbs = []\n"))
	}))
	defer srv.Close()
	if state, out := OrbitDrift(ctx, edge, Door{Base: srv.URL}); state != 1 || !strings.Contains(out, "hashes to") {
		t.Errorf("a 202 carrying a contract is compared: %d %q", state, out)
	}

	if state, out := OrbitDrift(ctx, "= =", Door{}); state != 2 || !strings.Contains(out, "orbit.toml did not parse") {
		t.Errorf("unparseable: %d %q", state, out)
	}
}

func TestOrbitDriftRefusesANotUTF8Body(t *testing.T) {
	door := doorServing(t, map[string]string{"orbits/c.toml": "verbs = \"\xff\"\n"})
	state, out := OrbitDrift(context.Background(), "[[consumes]]\ncontract = \"c\"\n", door)
	if state != 2 || !strings.Contains(out, "not valid UTF-8") {
		t.Errorf("got %d %q", state, out)
	}
}
