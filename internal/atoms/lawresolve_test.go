package atoms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	return LawResolver(context.Background(), f.Door)(source, ref, p)
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
	dead := deadDoor(t)
	if _, err := LawResolver(context.Background(), dead)(dead.Base+"/rob/x.git", "main", "."); err == nil || err == govlaw.ErrForeign {
		t.Errorf("a dead door answered: %v", err)
	}
}

func deadDoor(t *testing.T) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return checks.Door{Base: base, NoRetry: true}
}

func TestResolverABrokenOrCutManifestIsAnErrorNotAnAbsence(t *testing.T) {
	for name, body := range map[string]string{
		"not json":  "<html>",
		"truncated": `{"entries":[{"path":"other/x"}],"truncated":true}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		door := checks.Door{Base: srv.URL, NoRetry: true}
		got, err := LawResolver(context.Background(), door)(srv.URL+"/rob/x.git", "main", "policy")
		srv.Close()
		if err == nil || err == govlaw.ErrForeign {
			t.Errorf("%s: answered %v, %v", name, got, err)
		}
	}
}
