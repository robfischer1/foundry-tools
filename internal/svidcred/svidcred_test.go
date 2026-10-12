package svidcred

import (
	"encoding/base64"
	"net"
	"path/filepath"
	"time"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"strings"
	"testing"
)

const fakeToken = "eyJhbGciOiJSUzI1NiJ9.fake.token"

func fake(t *testing.T, err error) {
	t.Helper()
	old := Fetch
	t.Cleanup(func() { Fetch = old })
	Fetch = func(_ context.Context, socket, audience string) (string, string, error) {
		if err != nil {
			return "", "", err
		}
		if socket != "tcp://agent:1" || audience != "registry.notusmi.com" {
			t.Errorf("asked %s for %s", socket, audience)
		}
		return "spiffe://notusmi.com/ci/x", fakeToken, nil
	}
}

func run(args []string, env map[string]string, stdin string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

var goodEnv = map[string]string{EnvSocket: "tcp://agent:1", EnvAudience: "registry.notusmi.com"}

func TestGetAnswersTheSVIDAsTheLogin(t *testing.T) {
	fake(t, nil)
	code, out, errs := run([]string{"get"}, goodEnv, "registry.notusmi.com\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	user, secret, err := Parse(out)
	if err != nil || user != "spiffe://notusmi.com/ci/x" || secret != fakeToken {
		t.Fatalf("login = %q %q %v", user, secret, err)
	}
	var c credential
	if json.Unmarshal([]byte(out), &c); c.ServerURL != "registry.notusmi.com" {
		t.Errorf("ServerURL = %q", c.ServerURL)
	}
	if strings.Contains(errs, fakeToken) {
		t.Error("the token reached stderr")
	}
}

func TestGetNeverLogsTheTokenOnFailure(t *testing.T) {
	fake(t, errors.New("no identity"))
	code, out, errs := run([]string{"get"}, goodEnv, "h")
	if code != 1 || out != "" || !strings.Contains(errs, "no JWT-SVID") {
		t.Errorf("code %d out %q err %q", code, out, errs)
	}
}

func TestGetNeedsAnAudience(t *testing.T) {
	fake(t, nil)
	code, _, errs := run([]string{"get"}, map[string]string{EnvSocket: "tcp://agent:1"}, "h")
	if code != 1 || !strings.Contains(errs, EnvAudience) {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestGetRefusesAnUnopenableSocketAtOnce(t *testing.T) {
	fake(t, nil)
	env := map[string]string{EnvSocket: "unix:///nonexistent/agent.sock", EnvAudience: "a"}
	code, _, errs := run([]string{"get"}, env, "h")
	if code != 1 || !strings.Contains(errs, "cannot connect") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestOtherVerbs(t *testing.T) {
	for verb, wantOut := range map[string]string{"list": "{}\n", "store": "", "erase": ""} {
		if code, out, _ := run([]string{verb}, nil, ""); code != 0 || out != wantOut {
			t.Errorf("%s: %d %q", verb, code, out)
		}
	}
	if code, _, _ := run([]string{"bogus"}, nil, ""); code != 2 {
		t.Errorf("unknown verb exit %d", code)
	}
	if code, _, _ := run(nil, nil, ""); code != 2 {
		t.Errorf("no verb exit %d", code)
	}
}

func TestConfigNamesTheHelperAndHoldsNoCredential(t *testing.T) {
	cfg := Config("registry.notusmi.com")
	var got struct {
		CredHelpers map[string]string `json:"credHelpers"`
		Auths       map[string]any    `json:"auths"`
	}
	if err := json.Unmarshal([]byte(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.CredHelpers["registry.notusmi.com"] != "svid" || len(got.Auths) != 0 {
		t.Errorf("config = %s", cfg)
	}
}

func TestParseRejectsAnEmptyLogin(t *testing.T) {
	for _, in := range []string{"x", `{"Username":"","Secret":"s"}`, `{"Username":"u"}`} {
		if _, _, err := Parse(in); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestAnswerNamesTheSubjectAndCarriesTheToken(t *testing.T) {
	// A JWT with a blank signature parses insecurely: enough to hold an SVID without an agent.
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	tok := seg(`{"alg":"ES256","typ":"JWT"}`) + "." + seg(`{"sub":"spiffe://notusmi.com/ci/x","aud":["registry.notusmi.com"],"exp":4102444800}`) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	svid, err := jwtsvid.ParseInsecure(tok, []string{"registry.notusmi.com"})
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := answer(svid, nil)
	if err != nil || id != "spiffe://notusmi.com/ci/x" || token != tok {
		t.Errorf("id %q token %q err %v", id, token, err)
	}
	if _, _, err := answer(nil, errors.New("no agent")); err == nil {
		t.Error("an agent error was swallowed")
	}
}

// The default Fetch, against a socket nobody listens on, fails inside its
// wait instead of hanging.
func TestDefaultFetchFailsWithinItsWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, _, err := Fetch(ctx, "unix:///nonexistent/agent.sock", "a"); err == nil {
		t.Error("fetched from nowhere")
	}
}

func TestTheSocketDefaultsAndTheWaitIsConfigurable(t *testing.T) {
	old := Fetch
	t.Cleanup(func() { Fetch = old })
	var got string
	Fetch = func(_ context.Context, socket, _ string) (string, string, error) {
		got = socket
		return "id", "tok", nil
	}
	// The default socket is not there to connect to; reach refuses it first.
	if code, _, errs := run([]string{"get"}, map[string]string{EnvAudience: "a"}, "h"); code != 1 || !strings.Contains(errs, "unix:///run/spire/agent.sock") {
		t.Errorf("code %d err %q", code, errs)
	}
	if got != "" {
		t.Errorf("fetched from %q", got)
	}
	env := map[string]string{EnvSocket: "tcp://agent:1", EnvAudience: "a", envWait: "5s"}
	if code, _, errs := run([]string{"get"}, env, "h"); code != 0 {
		t.Errorf("code %d err %q", code, errs)
	}
	env[envWait] = "soon"
	if code, _, errs := run([]string{"get"}, env, "h"); code != 1 || !strings.Contains(errs, envWait) {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestReachConnectsToALiveSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := reach("unix://" + path); err != nil {
		t.Error(err)
	}
}
