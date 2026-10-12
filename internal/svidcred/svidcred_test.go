package svidcred

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
