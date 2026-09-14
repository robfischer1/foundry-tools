package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func noEnv(string) string { return "" }

func runWith(env func(string) string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, env, &out, &errOut)
	return code, out.String(), errOut.String()
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
		return map[string]string{"HADESCALL_HADES": "https://h:1/", "HADESCALL_IDENTITY_WAIT": "5s"}[k]
	})
	if err != nil || c.hades != "https://h:1" || c.wait != 5*time.Second {
		t.Fatalf("overrides: %+v %v", c, err)
	}
}
