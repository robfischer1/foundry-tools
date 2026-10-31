package atomtest

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// TreeDoor answers the door's /tree manifest as the real door does: files is
// "<repo>@<ref>" to the paths that ref holds, a repo or ref it has no key for is
// a 404, a prefix with no file is an empty manifest, and a status other than 200
// is every answer.
func TreeDoor(t TB, status int, files map[string][]string) checks.Door {
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
	return checks.Door{Base: srv.URL, NoRetry: true}
}
