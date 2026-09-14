package hadescall

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
)

func noEnv(string) string { return "" }

func runWith(env func(string) string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, env, &out, &errOut)
	return code, out.String(), errOut.String()
}

// THE FAST CHECKS COME FIRST. gremlins runs a mutant's package with
// -failfast in source order, so a mutant that would stretch a later test's
// identity wait to its two-minute default is killed here in milliseconds
// instead of timing out there.

func TestTheDefaultsAreTheFleets(t *testing.T) {
	c, err := configOf(noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c.socket != "unix:///run/spire/agent.sock" || c.hades != "https://hades.default.svc.cluster.local:8102" ||
		c.hadesID.String() != "spiffe://notusmi.com/star/hades" || c.wait != 120*time.Second {
		t.Fatalf("defaults: %+v", c)
	}
	c, err = configOf(func(k string) string {
		return map[string]string{"HADESCALL_HADES": "https://h:1/", "HADESCALL_IDENTITY_WAIT": "5s", "HADESCALL_SOCKET": "unix:///s"}[k]
	})
	if err != nil || c.hades != "https://h:1" || c.wait != 5*time.Second || c.socket != "unix:///s" {
		t.Fatalf("overrides: %+v %v", c, err)
	}
}

// The client gives hades three minutes, and presents this identity.
func TestTheClientWaitsThreeMinutes(t *testing.T) {
	if c := clientFor(&fakeIdentity{}, spiffeid.RequireFromString("spiffe://notusmi.com/star/hades")); c.Timeout != 3*time.Minute {
		t.Fatalf("timeout %s", c.Timeout)
	}
}

// An identity the agent cannot hand over is a could-not-run that names the
// socket, before anything is dialed.
func TestAnIdentityThatCannotOpenIsCouldNotRun(t *testing.T) {
	opened := Open
	Open = func(context.Context, string) (Identity, error) { return nil, errors.New("connection refused") }
	t.Cleanup(func() { Open = opened })
	code, stdout, stderr := runWith(noEnv, "forge_mold", `{}`)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "no identity from unix:///run/spire/agent.sock within 2m0s: connection refused") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// What a call refuses before it dials anything.
func TestACallRefusesWhatItCannotAsk(t *testing.T) {
	for _, c := range []struct {
		env    map[string]string
		args   []string
		stderr string
	}{
		{nil, nil, "usage"},
		{nil, []string{"forge_mold"}, "usage"},
		{nil, []string{"forge_mold", "{}", "extra"}, "usage"},
		{nil, []string{"forge_mold", "{not json"}, "not JSON"},
		{map[string]string{"HADESCALL_HADES_ID": "not-a-spiffe-id"}, []string{"v", "{}"}, "HADESCALL_HADES_ID"},
		{map[string]string{"HADESCALL_IDENTITY_WAIT": "soon"}, []string{"v", "{}"}, "HADESCALL_IDENTITY_WAIT"},
	} {
		env := func(k string) string { return c.env[k] }
		code, stdout, stderr := runWith(env, c.args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, c.stderr) {
			t.Errorf("%v %v: code %d stdout %q stderr %q, want 2 containing %q", c.env, c.args, code, stdout, stderr, c.stderr)
		}
	}
}

// A socket with no agent behind it is a could-not-run inside the wait, and the
// answer names the socket.
func TestACallWithNoAgentIsCouldNotRun(t *testing.T) {
	sock := "unix://" + t.TempDir() + "/absent.sock"
	env := func(k string) string {
		return map[string]string{"HADESCALL_SOCKET": sock, "HADESCALL_IDENTITY_WAIT": "300ms"}[k]
	}
	start := time.Now()
	code, stdout, stderr := runWith(env, "forge_mold", `{"name":"x"}`)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "no identity from "+sock) {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the identity wait overran: %s", took)
	}
}

// testPKI is a trust domain in miniature: a CA, and SVIDs it signs.
type testPKI struct {
	td    spiffeid.TrustDomain
	ca    *x509.Certificate
	caKey *ecdsa.PrivateKey
	next  int64
}

func newPKI(t *testing.T) *testPKI {
	t.Helper()
	td := spiffeid.RequireTrustDomainFromString("notusmi.com")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		URIs: []*url.URL{td.ID().URL()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testPKI{td: td, ca: ca, caKey: key, next: 2}
}

// svid signs a leaf for id, usable as a client or a server certificate.
func (p *testPKI) svid(t *testing.T, id string) *x509svid.SVID {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sid := spiffeid.RequireFromString(id)
	p.next++
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(p.next), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:        []*url.URL{sid.URL()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &x509svid.SVID{ID: sid, Certificates: []*x509.Certificate{cert}, PrivateKey: key}
}

type fakeIdentity struct {
	svid    *x509svid.SVID
	svidErr error
	bundle  *x509bundle.Bundle
	closed  bool
}

func (f *fakeIdentity) GetX509SVID() (*x509svid.SVID, error) { return f.svid, f.svidErr }
func (f *fakeIdentity) GetX509BundleForTrustDomain(spiffeid.TrustDomain) (*x509bundle.Bundle, error) {
	return f.bundle, nil
}
func (f *fakeIdentity) Close() error { f.closed = true; return nil }

// hades is a TLS server that presents serverID's SVID and requires a client
// certificate from the same CA, recording what it was asked.
type fakeHades struct {
	*httptest.Server
	mu       sync.Mutex
	asked    bool
	path     string
	body     string
	ctype    string
	peerURIs []string
}

func newHades(t *testing.T, p *testPKI, serverID string, handler func(w http.ResponseWriter, r *http.Request)) *fakeHades {
	t.Helper()
	h := &fakeHades{}
	srv := p.svid(t, serverID)
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	h.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.asked, h.path, h.body, h.ctype = true, r.URL.Path, string(b), r.Header.Get("Content-Type")
		for _, c := range r.TLS.PeerCertificates[:1] {
			for _, u := range c.URIs {
				h.peerURIs = append(h.peerURIs, u.String())
			}
		}
		h.mu.Unlock()
		handler(w, r)
	}))
	h.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{srv.Certificates[0].Raw}, PrivateKey: srv.PrivateKey}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	h.StartTLS()
	t.Cleanup(h.Close)
	return h
}

// callAs runs a call with the identity handed over in place of the agent's.
func callAs(t *testing.T, id Identity, hades string, args ...string) (int, string, string) {
	t.Helper()
	opened := Open
	Open = func(context.Context, string) (Identity, error) { return id, nil }
	t.Cleanup(func() { Open = opened })
	return runWith(func(k string) string { return map[string]string{"HADESCALL_HADES": hades}[k] }, args...)
}

// A call presents its own SVID, verifies hades by its SPIFFE id, posts the
// arguments as JSON to /v1/call/<verb>, and prints hades's answer whatever it
// says.
func TestACallPresentsItsSVIDAndPrintsHadesAnswer(t *testing.T) {
	p := newPKI(t)
	h := newHades(t, p, "spiffe://notusmi.com/star/hades", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"not granted"}`))
	})
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/ci/default/ca-build"), bundle: x509bundle.FromX509Authorities(p.td, []*x509.Certificate{p.ca})}
	code, stdout, stderr := callAs(t, id, h.URL, "forge_mold", `{"name":"ares"}`)
	if code != 0 || stdout != "HTTP 403\n{\"detail\":\"not granted\"}" {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "acting as spiffe://notusmi.com/ci/default/ca-build") {
		t.Errorf("stderr %q", stderr)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.path != "/v1/call/forge_mold" || h.body != `{"name":"ares"}` || h.ctype != "application/json" {
		t.Errorf("hades was asked %q %q %q", h.path, h.body, h.ctype)
	}
	if len(h.peerURIs) != 1 || h.peerURIs[0] != "spiffe://notusmi.com/ci/default/ca-build" {
		t.Errorf("hades saw the client as %v", h.peerURIs)
	}
	if !id.closed {
		t.Error("the identity was not closed")
	}
}

// A server presenting any other SPIFFE id is not hades: nothing is sent to it.
func TestAServerThatIsNotHadesIsNeverAsked(t *testing.T) {
	p := newPKI(t)
	h := newHades(t, p, "spiffe://notusmi.com/star/impostor", func(w http.ResponseWriter, r *http.Request) {})
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/ci/default/ca-build"), bundle: x509bundle.FromX509Authorities(p.td, []*x509.Certificate{p.ca})}
	code, stdout, stderr := callAs(t, id, h.URL, "forge_mold", `{}`)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "hades did not answer") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.asked {
		t.Fatal("the impostor was asked")
	}
}

// An identity that holds no SVID cannot ask.
func TestAnIdentityWithoutAnSVIDIsCouldNotRun(t *testing.T) {
	code, stdout, stderr := callAs(t, &fakeIdentity{svidErr: errors.New("no SVID yet")}, "https://hades:1", "forge_mold", `{}`)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "holds no SVID: no SVID yet") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// An address that is not a URL fails before anything is sent.
func TestAnAddressThatIsNotAURLIsCouldNotRun(t *testing.T) {
	p := newPKI(t)
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/ci/default/ca-build")}
	code, stdout, stderr := callAs(t, id, "https://hades\x7f:1", "forge_mold", `{}`)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "invalid control character") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// An answer cut off mid-body is not an answer.
func TestAnAnswerCutShortIsCouldNotRun(t *testing.T) {
	p := newPKI(t)
	h := newHades(t, p, "spiffe://notusmi.com/star/hades", func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 64\r\n\r\ncut")
		_ = buf.Flush()
		_ = conn.Close()
	})
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/ci/default/ca-build"), bundle: x509bundle.FromX509Authorities(p.td, []*x509.Certificate{p.ca})}
	code, stdout, stderr := callAs(t, id, h.URL, "forge_mold", `{}`)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "the answer did not read") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
