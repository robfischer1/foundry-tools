package witnesscall

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

	"dagger/foundry-tools/internal/hadescall"
)

func noEnv(string) string { return "" }

func noFile(string) ([]byte, error) { return nil, errors.New("no such file") }

// THE FAST CHECKS COME FIRST (gremlins runs with -failfast in source order).

func TestTheDefaultsAreTheFleets(t *testing.T) {
	c, err := configOf(noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c.socket != "unix:///run/spire/agent.sock" || c.serverID.String() != "spiffe://notusmi.com/star/narcissus" || c.wait != 20*time.Second {
		t.Errorf("defaults %+v", c)
	}
	c, err = configOf(func(k string) string {
		return map[string]string{"WITNESSCALL_SOCKET": "unix:///s", "WITNESSCALL_SERVER_ID": "spiffe://x.org/star/y", "WITNESSCALL_IDENTITY_WAIT": "3s"}[k]
	})
	if err != nil || c.socket != "unix:///s" || c.serverID.String() != "spiffe://x.org/star/y" || c.wait != 3*time.Second {
		t.Errorf("overrides %+v %v", c, err)
	}
	// A refused value names its key, wraps the parser's error, and keeps what
	// was already read.
	for k, v := range map[string]string{"WITNESSCALL_SERVER_ID": "not an id", "WITNESSCALL_IDENTITY_WAIT": "soon"} {
		c, err := configOf(func(n string) string { return map[string]string{k: v}[n] })
		if err == nil || !strings.Contains(err.Error(), k) || errors.Unwrap(err) == nil || c.socket != "unix:///run/spire/agent.sock" {
			t.Errorf("%s=%s: %+v %v", k, v, c, err)
		}
	}
}

func TestOnlyTheTwoCallsAreWellFormed(t *testing.T) {
	for _, ok := range [][]string{{"whoami"}, {"post", "u", "f"}} {
		if !wellFormed(ok) {
			t.Errorf("%v refused", ok)
		}
	}
	for _, bad := range [][]string{nil, {"whoami", "x"}, {"post", "u"}, {"post", "u", "f", "g"}, {"get", "u", "f"}, {"x"}} {
		if wellFormed(bad) {
			t.Errorf("%v accepted", bad)
		}
	}
}

func run(id hadescall.Identity, openErr error, env func(string) string, readFile func(string) ([]byte, error), args ...string) (int, string, string) {
	opened := hadescall.Open
	hadescall.Open = func(context.Context, string) (hadescall.Identity, error) { return id, openErr }
	defer func() { hadescall.Open = opened }()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, env, readFile, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestWhatCannotBeAskedNeverWaitsForAnIdentity(t *testing.T) {
	code, _, stderr := run(nil, errors.New("must not open"), noEnv, noFile, "post", "u")
	if code != CouldNotAsk || !strings.Contains(stderr, "usage:") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
	code, _, stderr = run(nil, errors.New("must not open"), func(k string) string {
		return map[string]string{"WITNESSCALL_IDENTITY_WAIT": "soon"}[k]
	}, noFile, "whoami")
	if code != CouldNotAsk || !strings.Contains(stderr, "WITNESSCALL_IDENTITY_WAIT") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}

func TestNoIdentityIsItsOwnExit(t *testing.T) {
	code, _, stderr := run(nil, errors.New("deadline exceeded"), noEnv, noFile, "whoami")
	if code != NoIdentity || !strings.Contains(stderr, "no identity from unix:///run/spire/agent.sock within 20s: deadline exceeded") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
	code, _, stderr = run(&fakeIdentity{svidErr: errors.New("empty")}, nil, noEnv, noFile, "whoami")
	if code != NoIdentity || !strings.Contains(stderr, "holds no SVID: empty") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}

func TestWhoamiPrintsTheLanesID(t *testing.T) {
	p := newPKI(t)
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/job/gate/gate-x-1")}
	code, stdout, _ := run(id, nil, noEnv, noFile, "whoami")
	if code != 0 || stdout != "spiffe://notusmi.com/job/gate/gate-x-1\n" {
		t.Errorf("code %d stdout %q", code, stdout)
	}
	if !id.closed {
		t.Error("the identity was not closed")
	}
}

func TestARequestFileThatDoesNotReadIsCouldNotAsk(t *testing.T) {
	p := newPKI(t)
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/job/gate/g")}
	code, _, stderr := run(id, nil, noEnv, noFile, "post", "https://narcissus:8201/mcp", "/req.json")
	if code != CouldNotAsk || !strings.Contains(stderr, "no such file") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
}

// A post presents the lane's SVID, verifies narcissus by its SPIFFE id, and
// prints status, content type and body.
func TestAPostPresentsTheLanesSVIDAndPrintsTheAnswer(t *testing.T) {
	p := newPKI(t)
	n := newServer(t, p, "spiffe://notusmi.com/star/narcissus", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {}\n\n"))
	})
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/job/gate/gate-x-1"), bundle: x509bundle.FromX509Authorities(p.td, []*x509.Certificate{p.ca})}
	read := func(string) ([]byte, error) { return []byte(`{"jsonrpc":"2.0"}`), nil }
	code, stdout, stderr := run(id, nil, noEnv, read, "post", n.URL+"/mcp", "/req.json")
	if code != 0 || stdout != "HTTP 200\ntext/event-stream\ndata: {}\n\n" {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.path != "/mcp" || n.body != `{"jsonrpc":"2.0"}` || n.ctype != "application/json" || n.accept != "application/json, text/event-stream" {
		t.Errorf("asked %q %q %q %q", n.path, n.body, n.ctype, n.accept)
	}
	if len(n.peerURIs) != 1 || n.peerURIs[0] != "spiffe://notusmi.com/job/gate/gate-x-1" {
		t.Errorf("the witness saw the client as %v", n.peerURIs)
	}
}

// A server presenting any other id is not narcissus: nothing is sent.
func TestAServerThatIsNotNarcissusIsNeverAsked(t *testing.T) {
	p := newPKI(t)
	n := newServer(t, p, "spiffe://notusmi.com/star/impostor", func(http.ResponseWriter, *http.Request) {})
	id := &fakeIdentity{svid: p.svid(t, "spiffe://notusmi.com/job/gate/g"), bundle: x509bundle.FromX509Authorities(p.td, []*x509.Certificate{p.ca})}
	read := func(string) ([]byte, error) { return []byte(`{}`), nil }
	code, _, stderr := run(id, nil, noEnv, read, "post", n.URL+"/mcp", "/req.json")
	if code != CouldNotAsk || !strings.Contains(stderr, "did not answer") {
		t.Errorf("code %d stderr %q", code, stderr)
	}
	if n.asked {
		t.Error("an impostor was asked")
	}
}

func TestAnAddressThatIsNotAURLIsCouldNotAsk(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := post(context.Background(), http.DefaultClient, "::", nil, &out, &errOut); code != CouldNotAsk || out.Len() != 0 || !strings.Contains(errOut.String(), "missing protocol scheme") {
		t.Errorf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

type cutShort struct{}

func (cutShort) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(&failingReader{})}, nil
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errors.New("reset") }

func TestAnAnswerCutShortIsCouldNotAsk(t *testing.T) {
	var out, errOut bytes.Buffer
	code := post(context.Background(), &http.Client{Transport: cutShort{}}, "https://narcissus:8201/mcp", nil, &out, &errOut)
	if code != CouldNotAsk || out.Len() != 0 || !strings.Contains(errOut.String(), "cut short: reset") {
		t.Errorf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

func TestTheClientWaitsTwoMinutes(t *testing.T) {
	if c := clientFor(&fakeIdentity{}, spiffeid.RequireFromString("spiffe://notusmi.com/star/narcissus")); c.Timeout != 120*time.Second {
		t.Errorf("timeout %s", c.Timeout)
	}
}

// --- a test PKI and a fake narcissus (shapes of internal/hadescall's) ---

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
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
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

type fakeServer struct {
	*httptest.Server
	mu                        sync.Mutex
	asked                     bool
	path, body, ctype, accept string
	peerURIs                  []string
}

func newServer(t *testing.T, p *testPKI, serverID string, handler http.HandlerFunc) *fakeServer {
	t.Helper()
	s := &fakeServer{}
	srv := p.svid(t, serverID)
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.asked, s.path, s.body = true, r.URL.Path, string(b)
		s.ctype, s.accept = r.Header.Get("Content-Type"), r.Header.Get("Accept")
		for _, u := range r.TLS.PeerCertificates[0].URIs {
			s.peerURIs = append(s.peerURIs, u.String())
		}
		s.mu.Unlock()
		handler(w, r)
	}))
	s.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{srv.Certificates[0].Raw}, PrivateKey: srv.PrivateKey}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
