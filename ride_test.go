package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/dagger"
)

// post is one record the door took: the bearer it came under and the record.
type post struct{ auth, body string }

// postLog is every post a test door received, in arrival order.
type postLog struct {
	mu    sync.Mutex
	url   string
	posts []post
}

// lines is what the run printed with this door's address made constant, so two
// runs against two doors compare.
func (l *postLog) lines(out *strings.Builder) string {
	return strings.ReplaceAll(out.String(), l.url, "http://door")
}

func (l *postLog) all() []post {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]post(nil), l.posts...)
}

// rideDoor stands up a door that takes every post, and points m at it.
func rideDoor(t *testing.T, m *FoundryTools) *postLog {
	t.Helper()
	got := &postLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.posts = append(got.posts, post{r.Header.Get("Authorization"), string(b)})
		got.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	got.url = srv.URL
	m.Repo = srv.URL + "/rob/ares.git"
	return got
}

// rideLines captures every line the record posts and the rider print.
func rideLines(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := recordPostOut
	recordPostOut = &buf
	t.Cleanup(func() { recordPostOut = prev })
	return &buf
}

// gradeBy answers the gate's clean vector, and the orbit stage whatever orbit
// says.
func gradeBy(orbit func() (string, error)) func(context.Context, *FoundryTools, string, string) (string, error) {
	return func(_ context.Context, _ *FoundryTools, stage, _ string) (string, error) {
		if stage == "orbit" {
			return orbit()
		}
		return cleanVector, nil
	}
}

// fileContents is the contents argument of the record file's chain. The SDK
// serialises arguments in map order, so the chain itself is not comparable.
var fileContents = regexp.MustCompile(`contents:"((?:[^"\\]|\\.)*)"`)

// gateFileRun runs GateFile as the gate Job does, with a record token, and
// answers the record file's contents as they went to the engine: the bytes the
// Job exports.
func gateFileRun(t *testing.T, m *FoundryTools, ride string, rideToken *dagger.Secret) string {
	t.Helper()
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	f, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "",
		dag.SetSecret("record-token", "gate-tok"), false, nil, nil, ride, rideToken)
	if err != nil {
		t.Fatalf("gate-file: %v", err)
	}
	_, _ = f.Contents(context.Background())
	chain := engine.chain("withNewFile", RecordFileName)
	got := fileContents.FindStringSubmatch(chain)
	if got == nil {
		t.Fatalf("gate-file wrote no record file: %q", chain)
	}
	return got[1]
}

// stageOf is the lane a posted record names.
func stageOf(t *testing.T, body string) StageResult {
	t.Helper()
	var got StageResult
	if err := json.Unmarshal([]byte(strings.TrimPrefix(body, "ourea-run-record/1 ")), &got); err != nil {
		t.Fatalf("not a record: %v: %q", err, body)
	}
	return got
}

// baseline is a run with no ride: the posts, the lines and the file the gate
// has always produced.
func baseline(t *testing.T) ([]post, string, string) {
	t.Helper()
	m := gateOn(t, cleanVector)
	door := rideDoor(t, m)
	out := rideLines(t)
	gateVector = gradeBy(func() (string, error) {
		t.Error("orbit was graded with no ride asked")
		return cleanVector, nil
	})
	file := gateFileRun(t, m, "", nil)
	return door.all(), door.lines(out), file
}

// WITH NO RIDE NOTHING MOVES: one post, under the gate's token, labelled gate;
// one line; and orbit is never graded. Two runs answer the same bytes.
func TestNoRideIsTheGateAsItWas(t *testing.T) {
	posts, lines, file := baseline(t)
	if len(posts) != 1 || posts[0].auth != "Bearer gate-tok" || stageOf(t, posts[0].body).Stage != "gate" {
		t.Fatalf("posts = %+v", posts)
	}
	if !strings.HasPrefix(lines, "record post: POST ") || strings.Count(lines, "\n") != 1 {
		t.Fatalf("lines = %q", lines)
	}
	again, againLines, againFile := baseline(t)
	if again[0] != posts[0] || againLines != lines || againFile != file {
		t.Fatal("two ride-less runs disagree")
	}
}

// ORBIT RIDES: two posts, the gate's first under its token and unchanged, then
// orbit's under its own, labelled orbit; the gate's file is the ride-less one.
func TestOrbitRidesUnderItsOwnToken(t *testing.T) {
	wantPosts, wantLines, wantFile := baseline(t)

	m := gateOn(t, cleanVector)
	door := rideDoor(t, m)
	out := rideLines(t)
	gateVector = gradeBy(func() (string, error) { return redVector, nil })
	file := gateFileRun(t, m, "orbit", dag.SetSecret("ride-token", "orbit-tok"))

	posts := door.all()
	if len(posts) != 2 {
		t.Fatalf("want 2 posts, got %+v", posts)
	}
	if posts[0] != wantPosts[0] {
		t.Errorf("the gate's post moved:\n want %+v\n  got %+v", wantPosts[0], posts[0])
	}
	if posts[1].auth != "Bearer orbit-tok" {
		t.Errorf("the rider posted under %q", posts[1].auth)
	}
	if got := stageOf(t, posts[1].body); got.Stage != "orbit" || got.State != 1 {
		t.Errorf("the rider's record = %s state %d", got.Stage, got.State)
	}
	if file != wantFile {
		t.Error("the gate's record file moved under a ride")
	}
	if lines := door.lines(out); !strings.HasPrefix(lines, wantLines) || !strings.HasSuffix(lines, "ride orbit: record post: POST http://door/ci/record → 204\n") {
		t.Errorf("lines = %q, want the gate's %q first", lines, wantLines)
	}
}

// A RIDER THAT CANNOT RUN POSTS COULD-NOT-RUN — an error and a panic alike —
// and the gate is unaffected.
func TestARiderThatCannotRunPostsStateTwo(t *testing.T) {
	_, _, wantFile := baseline(t)
	for name, orbit := range map[string]func() (string, error){
		"error": func() (string, error) { return "", errors.New("orbit went away") },
		"panic": func() (string, error) { panic("orbit blew up") },
	} {
		t.Run(name, func(t *testing.T) {
			m := gateOn(t, cleanVector)
			door := rideDoor(t, m)
			rideLines(t)
			gateVector = gradeBy(orbit)
			file := gateFileRun(t, m, "orbit", dag.SetSecret("ride-token", "orbit-tok"))
			posts := door.all()
			if len(posts) != 2 {
				t.Fatalf("want 2 posts, got %d", len(posts))
			}
			if got := stageOf(t, posts[1].body); got.Stage != "orbit" || got.State != 2 {
				t.Fatalf("the rider's record = %s state %d", got.Stage, got.State)
			}
			if stageOf(t, posts[0].body).State != 0 || file != wantFile {
				t.Fatal("the gate moved under a rider that could not run")
			}
		})
	}
}

// A RIDER SLOWER THAN THE GRACE IS ABANDONED UNPOSTED, and the gate returns.
func TestASlowRiderIsNotPosted(t *testing.T) {
	prev := rideGrace
	rideGrace = 50 * time.Millisecond
	t.Cleanup(func() { rideGrace = prev })
	release, returned := make(chan struct{}), make(chan struct{})

	m := gateOn(t, cleanVector)
	// AFTER gateOn, so it runs BEFORE gateOn restores gateVector: the abandoned
	// rider is let go and has returned before the variable it read is written.
	t.Cleanup(func() { close(release); <-returned })
	door := rideDoor(t, m)
	out := rideLines(t)
	gateVector = gradeBy(func() (string, error) {
		defer close(returned)
		<-release
		return cleanVector, nil
	})
	start := time.Now()
	gateFileRun(t, m, "orbit", dag.SetSecret("ride-token", "orbit-tok"))
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the gate waited %v on a hung rider", elapsed)
	}
	if posts := door.all(); len(posts) != 1 || posts[0].auth != "Bearer gate-tok" {
		t.Fatalf("posts = %+v", posts)
	}
	if !strings.Contains(out.String(), "ride orbit: not posted - no answer within the 50ms grace") {
		t.Fatalf("lines = %q", out.String())
	}
}

// REFUSED RIDES RIDE NOTHING: the gate itself, mutation, a stage that grades as
// the gate, and orbit without a token each cost one line and nothing else.
func TestRefusedRidesRideNothing(t *testing.T) {
	wantPosts, wantLines, wantFile := baseline(t)
	for _, c := range []struct {
		ride  string
		token bool
		why   string
	}{
		{"gate", true, "does not ride itself"},
		{"prepush", true, "does not ride itself"},
		{"mutation", true, "mutation lane does not ride"},
		{"mutation-bg", true, "mutation-bg lane does not ride"},
		{"visual", true, "visual lane does not ride"},
		{"orbit", false, "no --ride-token"},
	} {
		t.Run(c.ride, func(t *testing.T) {
			m := gateOn(t, cleanVector)
			door := rideDoor(t, m)
			out := rideLines(t)
			gateVector = gradeBy(func() (string, error) {
				t.Error("a refused ride was graded")
				return cleanVector, nil
			})
			var tok *dagger.Secret
			if c.token {
				tok = dag.SetSecret("ride-token", "orbit-tok")
			}
			file := gateFileRun(t, m, c.ride, tok)
			lines := door.lines(out)
			refusal, rest, _ := strings.Cut(lines, "\n")
			if !strings.HasPrefix(refusal, "ride "+c.ride+": refused - ") || !strings.Contains(refusal, c.why) {
				t.Errorf("refusal line = %q", refusal)
			}
			if rest != wantLines {
				t.Errorf("the gate's lines moved: %q, want %q", rest, wantLines)
			}
			if posts := door.all(); len(posts) != 1 || posts[0] != wantPosts[0] || file != wantFile {
				t.Errorf("the gate moved under a refused ride: %+v", posts)
			}
		})
	}
}

// ONLY THE GATE JOB CARRIES A RIDER: orbit asked of the mutation Job is refused.
func TestOnlyTheGateJobCarriesARider(t *testing.T) {
	if why := rideRefusal("mutation", "orbit", &dagger.Secret{}); !strings.Contains(why, "only the gate Job") {
		t.Fatalf("refusal = %q", why)
	}
	if why := rideRefusal("", "orbit", &dagger.Secret{}); why != "" {
		t.Fatalf("the gate refused orbit: %q", why)
	}
}
