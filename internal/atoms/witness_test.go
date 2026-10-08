package atoms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// narcissus is a stand-in for the witness's MCP port: it records every request
// and answers per file, by the path the request names.
type narcissus struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	bodies []string
	live   atomic.Int64
	peak   atomic.Int64
	// answer says what one ask of a file gets; n counts that file's asks from 1.
	answer func(w http.ResponseWriter, path string, n int)
}

func newNarcissus(t *testing.T, answer func(w http.ResponseWriter, path string, n int)) *narcissus {
	t.Helper()
	n := &narcissus{t: t, hits: map[string]int{}, answer: answer}
	n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var call struct {
			Params struct {
				Arguments struct {
					Path string `json:"path"`
				} `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(raw, &call)
		p := call.Params.Arguments.Path
		n.mu.Lock()
		n.hits[p]++
		count := n.hits[p]
		n.bodies = append(n.bodies, string(raw))
		n.mu.Unlock()
		cur := n.live.Add(1)
		for old := n.peak.Load(); cur > old && !n.peak.CompareAndSwap(old, cur); old = n.peak.Load() {
		}
		defer n.live.Add(-1)
		n.answer(w, p, count)
	}))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *narcissus) total() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	total := 0
	for _, c := range n.hits {
		total += c
	}
	return total
}

// said is a witness answer carrying a verdict, as MCP wraps it.
func said(verdict, recommendation string) string {
	inner, _ := json.Marshal(map[string]any{"verdict": verdict, "recommendation": recommendation})
	text, _ := json.Marshal(string(inner))
	return `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":` + string(text) + `}]}}`
}

func reply(verdict string) func(http.ResponseWriter, string, int) {
	return func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, said(verdict, "reuse it"))
	}
}

// dropped closes the connection with no answer: a transport failure.
func dropped(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

// witnessIn is an input over a tree of the named files, a change set naming
// them (and anything else), pointed at the stand-in and with no waiting.
func witnessIn(t *testing.T, n *narcissus, tree map[string]string, changed ...string) (Input, *[]time.Duration) {
	t.Helper()
	in := treeIn(t, tree)
	in.Changed = changed
	in.Origin = "https://git.notusmi.com/rob/some-star.git"
	var pauses []time.Duration
	var mu sync.Mutex
	in.Witness = Witness{Sleep: func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		pauses = append(pauses, d)
		return nil
	}}
	if n != nil {
		in.Witness.URL = n.srv.URL
	}
	return in, &pauses
}

func TestFleetWitnessAsksOnlyForSourceTheStarAuthored(t *testing.T) {
	const id = "fleet:witness"
	for _, tc := range []struct {
		name    string
		changed []string
		needles []string
	}{
		{"a change with no source", []string{"README.md", "go.sum"}, []string{"nothing to witness — no changed .py or .go file the star authored"}},
		{"only a test", []string{"a_test.go", "tests/test_x.py"}, []string{"nothing to witness", "skipped 2 test file(s)"}},
		{"only vendored source", []string{"vendor/x/y.go"}, []string{"nothing to witness", "skipped 1 vendored file(s)"}},
		{"only a language with no analyzer", []string{"lib.rs"}, []string{"nothing to witness", "skipped 1 file(s) in languages the witness has no analyzer for"}},
		{"an empty change set", nil, []string{"nothing to witness"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, reply("novel"))
			in, _ := witnessIn(t, n, map[string]string{"README.md": "x"}, tc.changed...)
			in.Spire = "/run/spire/agent.sock"
			in.Exec = func(_ context.Context, c Cmd) (string, int) {
				t.Errorf("a change with nothing to witness exec'd %s %v", c.Name, c.Args)
				return "", 0
			}
			v := runAtom(t, id, in)
			expect(t, v, stateOf(0), pass, append(tc.needles, "narcissus — fleet:witness:")...)
			if n.total() != 0 {
				t.Errorf("a change with nothing to witness made %d request(s)", n.total())
			}
			if strings.Contains(strings.Join(v.Logs, "\n"), "identity:") {
				t.Errorf("an ask that was never made names an identity:\n%v", v.Logs)
			}
		})
	}
}

func TestFleetWitnessSettlesOnTheWitnessAnswer(t *testing.T) {
	const id = "fleet:witness"
	tree := map[string]string{"a.go": "package a\nfunc A() {}\n", "b.py": "def b():\n  pass\n"}
	status := func(code int) func(http.ResponseWriter, string, int) {
		return func(w http.ResponseWriter, _ string, _ int) { w.WriteHeader(code) }
	}
	for _, tc := range []struct {
		name    string
		answer  func(http.ResponseWriter, string, int)
		state   int
		result  string
		asks    int
		pauses  int
		needles []string
	}{
		{"novel on both axes is a pass", reply("novel"), 0, pass, 2, 0, []string{"clean — 2 file(s) witnessed, novel on both axes"}},
		{"a duplicated standard is a finding", reply("standard"), 1, findings, 2, 0, []string{"findings in 2 of 2 file(s)", "duplicates a standard: reuse it"}},
		{"a convention is advisory and clean", reply("convention"), 0, pass, 2, 0, []string{"2 advisory (reuse, not rewrite)"}},
		{"a 4xx is the witness's own answer and is not asked again", status(400), 2, cannot, 2, 0, []string{"could not consult 2 of 2 file(s)", "narcissus returned HTTP 400"}},
		{"a 500 is not asked again either", status(500), 2, cannot, 2, 0, []string{"narcissus returned HTTP 500"}},
		{"a gateway that cannot reach the witness is asked again, to the bound", status(503), 2, cannot, 6, 4, []string{"narcissus returned HTTP 503"}},
		{"a dropped connection is asked again, to the bound", func(w http.ResponseWriter, _ string, _ int) { dropped(w) }, 2, cannot, 6, 4,
			[]string{"could not ask the witness:", "could not consult 2 of 2 file(s)"}},
		{"one transport failure, then an answer, is a pass", func(w http.ResponseWriter, p string, n int) {
			if n == 1 {
				dropped(w)
				return
			}
			reply("novel")(w, p, n)
		}, 0, pass, 4, 2, []string{"clean — 2 file(s) witnessed"}},
		{"a 502 then an answer is a pass", func(w http.ResponseWriter, p string, n int) {
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			reply("novel")(w, p, n)
		}, 0, pass, 4, 2, []string{"clean"}},
		{"a refusal naming no unit is nothing to witness, clean", func(w http.ResponseWriter, _ string, _ int) {
			inner, _ := json.Marshal(map[string]any{"verdict": "refused", "failure": map[string]any{"detail": "no function/method/class unit"}})
			text, _ := json.Marshal(string(inner))
			_, _ = fmt.Fprint(w, `{"result":{"content":[{"type":"text","text":`+string(text)+`}]}}`)
		}, 0, pass, 2, 0, []string{"nothing to witness — every changed source file had no unit"}},
		{"a finding beside a file that could not be consulted is a finding that says so", func(w http.ResponseWriter, p string, n int) {
			if p == "b.py" {
				w.WriteHeader(http.StatusTeapot)
				return
			}
			reply("standard")(w, p, n)
		}, 1, findings, 2, 0, []string{"findings in 1 of 2 file(s)", "also could not consult 1: b.py"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, tc.answer)
			in, pauses := witnessIn(t, n, tree, "a.go", "b.py", "notes.md")
			v := runAtom(t, id, in)
			expect(t, v, stateOf(tc.state), tc.result, append(tc.needles, "identity: none — asked "+checks.WitnessURL+" in the clear: the lane forwarded no --spire socket")...)
			if n.total() != tc.asks {
				t.Errorf("%d request(s), want %d", n.total(), tc.asks)
			}
			if len(*pauses) != tc.pauses {
				t.Errorf("%d pause(s), want %d", len(*pauses), tc.pauses)
			}
			for _, d := range *pauses {
				if d != checks.WitnessRetryPause {
					t.Errorf("paused %s between attempts, want %s", d, checks.WitnessRetryPause)
				}
			}
		})
	}
}

func TestFleetWitnessAsksAboutTheFileAsTheChainDid(t *testing.T) {
	n := newNarcissus(t, reply("novel"))
	in, _ := witnessIn(t, n, map[string]string{"pkg/a.go": "package a\n"}, "pkg/a.go")
	runAtom(t, "fleet:witness", in)
	if len(n.bodies) != 1 {
		t.Fatalf("%d request(s)", len(n.bodies))
	}
	want := checks.WitnessRequest(1, "package a\n", "go", "ci:gate:some-star@HEAD", "pkg/a.go")
	if n.bodies[0] != want {
		t.Errorf("the request was\n%s\nwant\n%s", n.bodies[0], want)
	}
	t.Run("a star with no origin is unknown", func(t *testing.T) {
		n := newNarcissus(t, reply("novel"))
		in, _ := witnessIn(t, n, map[string]string{"a.py": "x = 1\n"}, "a.py")
		in.Origin = ""
		runAtom(t, "fleet:witness", in)
		if !strings.Contains(n.bodies[0], `"caller":"ci:gate:unknown@HEAD"`) {
			t.Errorf("the caller was not unknown:\n%s", n.bodies[0])
		}
	})
}

func TestFleetWitnessAFileItCannotReadIsThatFilesRowAlone(t *testing.T) {
	n := newNarcissus(t, reply("novel"))
	in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\n"}, "a.go", "gone.go")
	v := runAtom(t, "fleet:witness", in)
	expect(t, v, stateOf(2), cannot, "could not consult 1 of 2 file(s)", "could not ask:", "gone.go")
	if n.total() != 1 {
		t.Errorf("%d request(s): the file that would not read must not be asked about", n.total())
	}
}

func TestFleetWitnessRowsKeepTheChangeSetsOrderWhateverOrderTheAnswersArriveIn(t *testing.T) {
	release := make(chan struct{})
	n := newNarcissus(t, func(w http.ResponseWriter, p string, c int) {
		if p == "a.go" {
			<-release // the first file answers last
		}
		if p == "c.go" {
			close(release)
		}
		reply("novel")(w, p, c)
	})
	in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n"}, "a.go", "b.go", "c.go")
	v := runAtom(t, "fleet:witness", in)
	expect(t, v, stateOf(0), pass, "clean — 3 file(s) witnessed")
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	a, b, c := strings.Index(said, "| a.go"), strings.Index(said, "| b.go"), strings.Index(said, "| c.go")
	if a < 0 || a >= b || b >= c {
		t.Errorf("rows are out of the change set's order (a %d, b %d, c %d):\n%s", a, b, c, said)
	}
	// And each request carried the id of its place in the change set.
	for _, body := range n.bodies {
		for i, p := range []string{"a.go", "b.go", "c.go"} {
			if strings.Contains(body, `"path":"`+p+`"`) && !strings.Contains(body, fmt.Sprintf(`"id":%d,`, i+1)) {
				t.Errorf("the request for %s carried the wrong id:\n%s", p, body)
			}
		}
	}
}

func TestFleetWitnessAsksFourAtOnceAtMost(t *testing.T) {
	n := newNarcissus(t, func(w http.ResponseWriter, p string, c int) {
		time.Sleep(30 * time.Millisecond)
		reply("novel")(w, p, c)
	})
	tree := map[string]string{}
	var changed []string
	for i := range 12 {
		name := fmt.Sprintf("f%02d.go", i)
		tree[name] = "package f\n"
		changed = append(changed, name)
	}
	in, _ := witnessIn(t, n, tree, changed...)
	expect(t, runAtom(t, "fleet:witness", in), stateOf(0), pass, "12 file(s) witnessed")
	if peak := n.peak.Load(); peak > int64(checks.WitnessWorkers) || peak < 2 {
		t.Errorf("%d asks in flight at once, want 2..%d", peak, checks.WitnessWorkers)
	}
}

func TestFleetWitnessAChangeSetThatWouldNotComputeIsSaidAsIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		state   int
		result  string
		needles []string
	}{
		{"a worktree snapshot has no change set, and the verdict says so", SnapshotError{Kind: "worktree"}, 0, pass,
			[]string{"no change set to witness at pre-push: the source is a worktree snapshot with no commits"}},
		{"a base the history does not reach", fmt.Errorf("the base abc is not in this history"), 2, cannot,
			[]string{"CANNOT RUN - could not read the change set: the base abc is not in this history"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, reply("novel"))
			in, _ := witnessIn(t, n, map[string]string{"a.go": "x"}, "a.go")
			in.ChangedErr = tc.err
			expect(t, runAtom(t, "fleet:witness", in), stateOf(tc.state), tc.result, tc.needles...)
			if n.total() != 0 {
				t.Errorf("%d request(s) without a change set", n.total())
			}
		})
	}
}

// witnessHelper stands in for the witnesscall helper.
type witnessHelper struct {
	t      *testing.T
	mu     sync.Mutex
	calls  []Cmd
	whoami func() (string, int)
	post   func(c Cmd, body string) (string, int)
	seen   []string
}

func (h *witnessHelper) exec(_ context.Context, c Cmd) (string, int) {
	h.mu.Lock()
	h.calls = append(h.calls, c)
	h.mu.Unlock()
	if c.Name != "witnesscall" {
		h.t.Errorf("exec of %q", c.Name)
		return "", 0
	}
	if c.Args[0] == "whoami" {
		return h.whoami()
	}
	raw, err := os.ReadFile(c.Args[2])
	if err != nil {
		h.t.Errorf("the request file is not there when the helper reads it: %v", err)
	}
	h.mu.Lock()
	h.seen = append(h.seen, c.Args[2])
	h.mu.Unlock()
	return h.post(c, string(raw))
}

func (h *witnessHelper) posts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls) - 1
}

func TestFleetWitnessAsksAsTheLanesIdentityWhenTheSocketIsForwarded(t *testing.T) {
	const svid = "spiffe://notusmi.com/job/gate/gate-x-1"
	okPost := func(c Cmd, body string) (string, int) {
		return "HTTP 200\napplication/json\n" + said("novel", ""), 0
	}
	for _, tc := range []struct {
		name    string
		whoami  func() (string, int)
		post    func(c Cmd, body string) (string, int)
		state   int
		result  string
		clear   int // requests that reached the clear port
		posts   int
		needles []string
	}{
		{"an identified ask is the whole answer", func() (string, int) { return svid, 0 }, okPost, 0, pass, 0, 1,
			[]string{"identity: asked as " + svid + " at " + checks.WitnessMTLSURL}},
		{"an ask that could not be made falls back to the clear port after its attempts", func() (string, int) { return svid, 0 },
			func(Cmd, string) (string, int) { return "witnesscall: connect refused", 2 }, 0, pass, 1, 3,
			[]string{"asked as " + svid, "1 of 1 ask(s) fell back to " + checks.WitnessURL + " in the clear"}},
		{"a helper that would not start falls back too", func() (string, int) { return svid, 0 },
			func(Cmd, string) (string, int) { return "witnesscall: not found", -1 }, 0, pass, 1, 3,
			[]string{"fell back"}},
		{"an ask that was sent and drew no answer is never repeated in the clear", func() (string, int) { return svid, 0 },
			func(Cmd, string) (string, int) { return "deadline", checks.WitnessNoAnswerExit }, 2, cannot, 0, 1,
			[]string{"could not ask the witness:", "did not answer the identified ask in time", "asked as " + svid}},
		{"no identity is asked in the clear, with the helper's reason", func() (string, int) { return "witnesscall: no identity", 3 }, okPost, 0, pass, 1, 0,
			[]string{"identity: none — asked " + checks.WitnessURL + " in the clear: witnesscall: no identity"}},
		{"a helper that never ran is said", func() (string, int) { return "witnesscall: gone", -1 }, okPost, 0, pass, 1, 0,
			[]string{"in the clear: witnesscall never ran: witnesscall: gone"}},
		{"a helper that prints no status line is an error, then the clear port", func() (string, int) { return svid, 0 },
			func(Cmd, string) (string, int) { return "garbage", 0 }, 0, pass, 1, 3, []string{"fell back"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, reply("novel"))
			in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\n"}, "a.go")
			in.Spire = "/run/spire/agent.sock"
			h := &witnessHelper{t: t, whoami: tc.whoami, post: tc.post}
			in.Exec = h.exec
			expect(t, runAtom(t, "fleet:witness", in), stateOf(tc.state), tc.result, tc.needles...)
			if n.total() != tc.clear {
				t.Errorf("%d request(s) reached the clear port, want %d", n.total(), tc.clear)
			}
			if h.posts() != tc.posts {
				t.Errorf("the helper posted %d time(s), want %d", h.posts(), tc.posts)
			}
			for _, c := range h.calls {
				if strings.Join(c.Env, " ") != "WITNESSCALL_SOCKET=unix:///run/spire/agent.sock" || c.Dir != in.Root {
					t.Errorf("the helper ran with env %v in %q", c.Env, c.Dir)
				}
				if c.Args[0] == "post" && c.Args[1] != checks.WitnessMTLSURL {
					t.Errorf("the helper posted to %s", c.Args[1])
				}
			}
			for _, f := range h.seen {
				if _, err := os.Stat(f); err == nil {
					t.Errorf("the request file %s was left behind", f)
				}
			}
		})
	}
	t.Run("the helper is handed the request the chain would have posted", func(t *testing.T) {
		in, _ := witnessIn(t, nil, map[string]string{"a.go": "package a\n"}, "a.go")
		in.Spire = "/run/spire/agent.sock"
		var got string
		in.Exec = (&witnessHelper{t: t, whoami: func() (string, int) { return svid, 0 }, post: func(_ Cmd, body string) (string, int) {
			got = body
			return "HTTP 200\napplication/json\n" + said("novel", ""), 0
		}}).exec
		runAtom(t, "fleet:witness", in)
		if want := checks.WitnessRequest(1, "package a\n", "go", "ci:gate:some-star@HEAD", "a.go"); got != want {
			t.Errorf("the helper read\n%s\nwant\n%s", got, want)
		}
	})
}
