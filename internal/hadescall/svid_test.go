package hadescall

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
)

// HADESCALL_SVID_FIELD writes the call's own SVID into the arguments, so it
// needs a JSON object, and it refuses anything else before dialing.
func TestTheSVIDFieldNeedsAnObjectAndIsRefusedBeforeDialing(t *testing.T) {
	for _, body := range []string{`["a"]`, `"a string"`, `null`, `42`} {
		env := func(k string) string {
			return map[string]string{"HADESCALL_SVID_FIELD": "svid", "HADESCALL_SOCKET": "unix:///nonexistent.sock", "HADESCALL_IDENTITY_WAIT": "100ms"}[k]
		}
		code, stdout, stderr := runWith(env, "tartarus_attest_emit", body)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "HADESCALL_SVID_FIELD=svid needs a JSON object") {
			t.Errorf("%s: code %d stdout %q stderr %q", body, code, stdout, stderr)
		}
		if strings.Contains(stderr, "no identity") {
			t.Errorf("%s: dialed the socket before refusing", body)
		}
	}
}

// The receipt hades receives carries the SVID the call presented, under the
// field named, beside every field it was given.
func TestTheSVIDIsWrittenIntoTheNamedField(t *testing.T) {
	p := newPKI(t)
	h := newHades(t, p, "spiffe://notusmi.com/star/hades", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/ci/default/ca-gate"), bundle: x509bundle.FromX509Authorities(p.td, []*x509.Certificate{p.ca})}
	opened := Open
	Open = func(context.Context, string) (Identity, error) { return id, nil }
	t.Cleanup(func() { Open = opened })
	env := func(k string) string {
		return map[string]string{"HADESCALL_HADES": h.URL, "HADESCALL_SVID_FIELD": "svid"}[k]
	}
	code, stdout, stderr := runWith(env, "tartarus_attest_emit", `{"tree":"t","verdict":[1]}`)
	if code != 0 || stdout != "HTTP 200\n{\"ok\":true}" {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var got map[string]any
	if err := json.Unmarshal([]byte(h.body), &got); err != nil {
		t.Fatalf("hades received %q: %v", h.body, err)
	}
	if got["svid"] != "spiffe://notusmi.com/ci/default/ca-gate" || got["tree"] != "t" || len(got["verdict"].([]any)) != 1 {
		t.Fatalf("hades received %s", h.body)
	}
}
