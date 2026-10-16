package visuallane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRegistry is the slice of the distribution API a push uses, recording
// what it was sent.
type fakeRegistry struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	auths     []string
	// have are digests the registry already holds (HEAD answers 200).
	have map[string]bool
	// fail maps "METHOD path-prefix" to the status that request gets.
	fail map[string]int
	// relative makes the upload Location a path, not an absolute URL.
	relative bool
	// challenge, when set, answers an unauthenticated request with a Bearer
	// challenge naming this realm, and issues token for a Basic request to it.
	realm, token string
}

func newFake() *fakeRegistry {
	return &fakeRegistry{blobs: map[string][]byte{}, manifests: map[string][]byte{}, have: map[string]bool{}, fail: map[string]int{}}
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	auth := r.Header.Get("Authorization")
	if r.URL.Path == "/token" {
		f.auths = append(f.auths, "token:"+auth)
		if !strings.HasPrefix(auth, "Basic ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": f.token})
		return
	}
	f.auths = append(f.auths, auth)
	if f.realm != "" && auth != "Bearer "+f.token {
		w.Header().Set("Www-Authenticate", `Bearer realm="`+f.realm+`",service="registry",scope="repository:foundry/visual/gijmo-ui:push,pull"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	for prefix, code := range f.fail {
		if strings.HasPrefix(r.Method+" "+r.URL.Path, prefix) {
			w.WriteHeader(code)
			return
		}
	}
	const repo = "/v2/foundry/visual/gijmo-ui"
	switch {
	case r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, repo+"/blobs/"):
		if f.have[strings.TrimPrefix(r.URL.Path, repo+"/blobs/")] {
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost && r.URL.Path == repo+"/blobs/uploads/":
		loc := repo + "/blobs/uploads/session-1"
		if !f.relative {
			loc = "http://" + r.Host + loc
		}
		w.Header().Set("Location", loc)
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodPut && r.URL.Path == repo+"/blobs/uploads/session-1":
		body, _ := io.ReadAll(r.Body)
		f.blobs[r.URL.Query().Get("digest")] = body
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, repo+"/manifests/"):
		body, _ := io.ReadAll(r.Body)
		f.manifests[strings.TrimPrefix(r.URL.Path, repo+"/manifests/")+"|"+r.Header.Get("Content-Type")] = body
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

func layerOf(t *testing.T, name, body string) Layer {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := TarLayer("results", root, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func pusher(srv *httptest.Server, cred Credential) Registry {
	return Registry{Base: srv.URL, HTTP: srv.Client(), Cred: cred}
}

// A push uploads the config and every layer, then the manifest under the tag,
// and the manifest names exactly those blobs.
func TestUT_VIS_PushUploadsBlobsThenTheManifest(t *testing.T) {
	f := newFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	a, b := layerOf(t, "a.png", "A"), layerOf(t, "b.png", "B")
	err := pusher(srv, Credential{Bearer: "jwt"}).Push(context.Background(), "foundry/visual/gijmo-ui", "sha1", []Layer{a, b})
	if err != nil {
		t.Fatal(err)
	}
	var key, raw string
	for k, v := range f.manifests {
		key, raw = k, string(v)
	}
	if key != "sha1|application/vnd.oci.image.manifest.v1+json" {
		t.Fatalf("manifest stored as %q", key)
	}
	var m struct {
		MediaType string
		Config    struct{ MediaType, Digest string }
		Layers    []struct{ MediaType, Digest string }
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Layers) != 2 || m.Layers[0].Digest != a.Digest || m.Layers[1].Digest != b.Digest ||
		m.Layers[0].MediaType != "application/vnd.oci.image.layer.v1.tar+gzip" || m.Config.MediaType != "application/vnd.oci.image.config.v1+json" {
		t.Errorf("%s", raw)
	}
	for _, d := range []string{a.Digest, b.Digest, m.Config.Digest} {
		if len(f.blobs[d]) == 0 {
			t.Errorf("blob %s was not uploaded", d)
		}
	}
	var cfg struct {
		Architecture, OS string
		RootFS           struct {
			Type    string
			DiffIDs []string `json:"diff_ids"`
		}
	}
	if err := json.Unmarshal(f.blobs[m.Config.Digest], &cfg); err != nil || cfg.RootFS.Type != "layers" ||
		len(cfg.RootFS.DiffIDs) != 2 || cfg.RootFS.DiffIDs[0] != a.DiffID || cfg.OS != "linux" || cfg.Architecture != "amd64" {
		t.Errorf("config %s: %v", f.blobs[m.Config.Digest], err)
	}
	for _, auth := range f.auths {
		if auth != "Bearer jwt" {
			t.Errorf("a request carried %q, not the SVID as a bearer token", auth)
		}
	}
}

// A blob the registry holds is not sent again; a relative upload Location is
// resolved against the registry.
func TestUT_VIS_PushSkipsHeldBlobsAndFollowsRelativeLocations(t *testing.T) {
	f := newFake()
	f.relative = true
	a := layerOf(t, "a.png", "A")
	f.have[a.Digest] = true
	srv := httptest.NewServer(f)
	defer srv.Close()
	if err := pusher(srv, Credential{Bearer: "jwt"}).Push(context.Background(), "foundry/visual/gijmo-ui", "t", []Layer{a}); err != nil {
		t.Fatal(err)
	}
	if _, sent := f.blobs[a.Digest]; sent {
		t.Error("a blob the registry already held was uploaded again")
	}
	if len(f.blobs) != 1 {
		t.Errorf("only the config was missing, got %d blobs", len(f.blobs))
	}
}

// Basic credentials answer a Bearer challenge by asking the realm for a token,
// then retry with it; the password never rides on the API calls.
func TestUT_VIS_PushAnswersABearerChallengeWithBasic(t *testing.T) {
	f := newFake()
	f.token = "tok"
	srv := httptest.NewServer(f)
	defer srv.Close()
	f.realm = srv.URL + "/token"
	err := pusher(srv, Credential{User: "publisher", Password: "pw"}).Push(context.Background(), "foundry/visual/gijmo-ui", "t", []Layer{layerOf(t, "a", "A")})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.manifests) != 1 {
		t.Fatalf("no manifest after the challenge: %v", f.auths)
	}
	var tokens int
	for _, a := range f.auths {
		if strings.HasPrefix(a, "token:Basic ") {
			tokens++
		} else if strings.HasPrefix(a, "Basic ") {
			t.Errorf("the password rode on an API call: %q", a)
		}
	}
	if tokens != 1 {
		t.Errorf("the realm was asked %d times, want once: %v", tokens, f.auths)
	}
	if last := f.auths[len(f.auths)-1]; last != "Bearer tok" {
		t.Errorf("the last call carried %q", last)
	}
}

// Every way a push fails says which request, and with what.
func TestUT_VIS_PushSaysWhichRequestFailed(t *testing.T) {
	for name, c := range map[string]struct {
		fail map[string]int
		want string
	}{
		"head":             {map[string]int{"HEAD ": 500}, "HEAD"},
		"upload start":     {map[string]int{"POST ": 403}, "403"},
		"upload finish":    {map[string]int{"PUT /v2/foundry/visual/gijmo-ui/blobs/uploads/": 400}, "400"},
		"manifest":         {map[string]int{"PUT /v2/foundry/visual/gijmo-ui/manifests/": 400}, "400"},
		"denied":           {map[string]int{"POST ": 401}, "401"},
		"manifest refused": {map[string]int{"PUT /v2/foundry/visual/gijmo-ui/manifests/": 403}, "403"},
	} {
		f := newFake()
		f.fail = c.fail
		srv := httptest.NewServer(f)
		err := pusher(srv, Credential{Bearer: "jwt"}).Push(context.Background(), "foundry/visual/gijmo-ui", "t", []Layer{layerOf(t, "a", "A")})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// A registry that is not there, a realm that refuses and a credential with
// nothing in it each end the push with their own cause.
func TestUT_VIS_PushRefusesWhatItCannotAuthenticateOrReach(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	base := gone.URL
	gone.Close()
	err := Registry{Base: base, HTTP: http.DefaultClient, Cred: Credential{Bearer: "jwt"}}.Push(context.Background(), "r", "t", []Layer{layerOf(t, "a", "A")})
	if err == nil {
		t.Error("an unreachable registry pushed")
	}

	f := newFake()
	f.token = "tok"
	srv := httptest.NewServer(f)
	defer srv.Close()
	f.realm = srv.URL + "/token"
	err = pusher(srv, Credential{Bearer: "wrong"}).Push(context.Background(), "foundry/visual/gijmo-ui", "t", []Layer{layerOf(t, "a", "A")})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a bearer the registry refuses: %v", err)
	}

	f = newFake()
	f.token = "tok"
	srv2 := httptest.NewServer(f)
	defer srv2.Close()
	f.realm = srv2.URL + "/token"
	err = pusher(srv2, Credential{User: "u", Password: "p"}).Push(context.Background(), "foundry/visual/gijmo-ui", "t", []Layer{layerOf(t, "a", "A")})
	if err != nil {
		t.Errorf("basic through the realm: %v", err)
	}
	f.token = ""
	err = pusher(srv2, Credential{User: "u", Password: "p"}).Push(context.Background(), "foundry/visual/gijmo-ui", "t", []Layer{layerOf(t, "a", "A")})
	if err == nil {
		t.Error("a realm that issued no token let the push through")
	}
}

func TestUT_VIS_ParseRefSplitsHostRepositoryAndTag(t *testing.T) {
	host, repo, tag, err := ParseRef("registry.notusmi.com/foundry/visual/gijmo-ui:" + strings.Repeat("a", 40))
	if err != nil || host != "registry.notusmi.com" || repo != "foundry/visual/gijmo-ui" || tag != strings.Repeat("a", 40) {
		t.Errorf("%s %s %s %v", host, repo, tag, err)
	}
	for _, bad := range []string{"noslash:tag", "host/repo", "host/repo:"} {
		if _, _, _, err := ParseRef(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestUT_VIS_PushRefPushesToTheReferencesRepositoryAndTag(t *testing.T) {
	f := newFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	r := pusher(srv, Credential{Bearer: "jwt"})
	if err := r.PushRef(context.Background(), "registry.notusmi.com/foundry/visual/gijmo-ui:abc", []Layer{layerOf(t, "a", "A")}); err != nil {
		t.Fatal(err)
	}
	if len(f.manifests) != 1 {
		t.Errorf("%v", f.manifests)
	}
	if err := r.PushRef(context.Background(), "no-tag", nil); err == nil {
		t.Error("a reference with no tag pushed")
	}
}
