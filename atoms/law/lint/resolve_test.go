package lawlint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// absorb-april-soap: the atom hands law:lint a lookup over the door's /tree.

func lawTree(source, p string) map[string]string {
	return atomtest.StocksTree(map[string]string{
		"dies.toml": "[dies.d]\nsource = \"" + source + "\"\npath = \"" + p + "\"\n",
	})
}

func TestTheAtomGradesASourceInAnotherRepository(t *testing.T) {
	door := treeDoor(t, http.StatusOK, map[string][]string{"rob/x@main": {"policy/a.toml"}})
	run := func(d checks.Door, source, p string) checks.Verdict {
		in := atomtest.TreeIn(t, lawTree(d.Base+source, p))
		in.Door = d
		return check(context.Background(), in)
	}
	atomtest.Expect(t, run(door, "/rob/x.git@main", "policy"), checks.StatePass, atomtest.Pass, "1 more found at their ref")
	atomtest.Expect(t, run(door, "/rob/x.git@nope", "policy"), checks.StateFindings, atomtest.Findings, `ref "nope" does not exist`)
	atomtest.Expect(t, run(door, "/rob/x.git@main", "absent"), checks.StateFindings, atomtest.Findings, `path "absent" is not in`)
	down := treeDoor(t, http.StatusBadGateway, nil)
	atomtest.Expect(t, run(down, "/rob/x.git@main", "policy"), checks.StateCannotRun, atomtest.Cannot, "could not be read")
}

// treeDoor answers the door's /tree manifest as the real door does: files is
// "<repo>@<ref>" to the paths that ref holds, an unknown repo or ref is a 404, a
// prefix with no file is an empty manifest, and a status other than 200 is every
// answer.
func treeDoor(t *testing.T, status int, files map[string][]string) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if status != http.StatusOK {
			http.Error(w, "down", status)
			return
		}
		paths, ok := files[q.Get("repo")+"@"+q.Get("ref")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		out, sep := `{"entries":[`, ""
		for _, p := range paths {
			if strings.HasPrefix(p, q.Get("prefix")) {
				out += sep + `{"path":"` + p + `"}`
				sep = ","
			}
		}
		_, _ = w.Write([]byte(out + `],"truncated":false}`))
	}))
	t.Cleanup(srv.Close)
	return checks.Door{Base: srv.URL, NoRetry: true}
}
