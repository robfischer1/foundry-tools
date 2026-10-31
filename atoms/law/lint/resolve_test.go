package lawlint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// absorb-april-soap: the door answers /tree for the sources dies.toml names in
// other repositories. The fake holds one repository, rob/x, with refs main and
// v1, and answers what the real door answers (404 for an unknown repo or ref, an
// empty manifest for a prefix with no file).
type fakeDoor struct {
	checks.Door
	asked []string
}

func newFakeDoor(t *testing.T, status int, files map[string][]string) *fakeDoor {
	t.Helper()
	f := &fakeDoor{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.asked = append(f.asked, q.Get("repo")+"@"+q.Get("ref")+":"+q.Get("prefix"))
		if status != 200 {
			http.Error(w, "down", status)
			return
		}
		paths, ok := files[q.Get("repo")+"@"+q.Get("ref")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		out := `{"entries":[`
		sep := ""
		for _, p := range paths {
			if strings.HasPrefix(p, q.Get("prefix")) {
				out += sep + `{"path":"` + p + `"}`
				sep = ","
			}
		}
		_, _ = w.Write([]byte(out + `],"truncated":false}`))
	}))
	t.Cleanup(srv.Close)
	f.Door = checks.Door{Base: srv.URL, NoRetry: true}
	return f
}

func resolve(f *fakeDoor, source, ref, p string) (govlaw.Presence, error) {
	return resolver(context.Background(), f.Door)(source, ref, p)
}

func TestResolverFindsAFileDirectoryAndRoot(t *testing.T) {
	f := newFakeDoor(t, 200, map[string][]string{"rob/x@main": {"policy/a.toml", "policy2/b.toml", "top.toml"}})
	for _, p := range []string{"policy", "policy/a.toml", ".", "top.toml"} {
		if got, err := resolve(f, f.Base+"/rob/x.git", "main", p); err != nil || got != govlaw.Found {
			t.Errorf("%s: %v %v", p, got, err)
		}
	}
}

func TestResolverTellsAMissingRefFromAMissingPath(t *testing.T) {
	f := newFakeDoor(t, 200, map[string][]string{"rob/x@main": {"policy2/b.toml"}})
	if got, err := resolve(f, f.Base+"/rob/x.git", "gone", "policy"); err != nil || got != govlaw.NoRef {
		t.Errorf("ref: %v %v", got, err)
	}
	// policy2/ is not policy/: a prefix match alone would call it found.
	if got, err := resolve(f, f.Base+"/rob/x.git", "main", "policy"); err != nil || got != govlaw.NoPath {
		t.Errorf("path: %v %v", got, err)
	}
}

func TestResolverLeavesAnotherHostAlone(t *testing.T) {
	f := newFakeDoor(t, 200, nil)
	if _, err := resolve(f, "https://github.com/o/r.git", "main", "."); err != govlaw.ErrForeign || len(f.asked) != 0 {
		t.Errorf("err %v asked %v", err, f.asked)
	}
}

func TestResolverADoorThatIsDownIsAnErrorNotAnAbsence(t *testing.T) {
	f := newFakeDoor(t, http.StatusServiceUnavailable, nil)
	if got, err := resolve(f, f.Base+"/rob/x.git", "main", "."); err == nil {
		t.Errorf("a 503 answered %v", got)
	}
	dead := atomtest.DeadDoor(t)
	if _, err := resolver(context.Background(), dead)(dead.Base+"/rob/x.git", "main", "."); err == nil || err == govlaw.ErrForeign {
		t.Errorf("a dead door answered: %v", err)
	}
}

func lawTree(source, p string) map[string]string {
	return atomtest.StocksTree(map[string]string{
		"dies.toml": "[dies.d]\nsource = \"" + source + "\"\npath = \"" + p + "\"\n",
	})
}

func TestTheAtomGradesASourceInAnotherRepository(t *testing.T) {
	f := newFakeDoor(t, 200, map[string][]string{"rob/x@main": {"policy/a.toml"}})
	run := func(files map[string]string) checks.Verdict {
		in := atomtest.TreeIn(t, files)
		in.Door = f.Door
		return check(context.Background(), in)
	}
	atomtest.Expect(t, run(lawTree(f.Base+"/rob/x.git@main", "policy")), checks.StatePass, atomtest.Pass, "1 more found at their ref")
	atomtest.Expect(t, run(lawTree(f.Base+"/rob/x.git@nope", "policy")), checks.StateFindings, atomtest.Findings, `ref "nope" does not exist`)
	atomtest.Expect(t, run(lawTree(f.Base+"/rob/x.git@main", "absent")), checks.StateFindings, atomtest.Findings, `path "absent" is not in`)
	down := newFakeDoor(t, http.StatusBadGateway, nil)
	in := atomtest.TreeIn(t, lawTree(down.Base+"/rob/x.git@main", "policy"))
	in.Door = down.Door
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "could not be read")
}
