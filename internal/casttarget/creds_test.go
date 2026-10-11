package casttarget

import (
	"bytes"
	"context"
	"dagger/foundry-tools/internal/castcache"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testJWT    = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJjYS1jYXN0In0.c2ln"
	testSVID   = "spiffe://notusmi.com/ci/prime/ca-cast"
	stsSuccess = `<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleWithWebIdentityResult><Credentials>` +
		`<AccessKeyId>ASIAEXAMPLE</AccessKeyId><SecretAccessKey>secretEXAMPLE</SecretAccessKey><SessionToken>tokenEXAMPLE</SessionToken>` +
		`<Expiration>2026-10-12T00:00:00Z</Expiration></Credentials></AssumeRoleWithWebIdentityResult></AssumeRoleWithWebIdentityResponse>`
)

// withJWT stands in for the SPIRE agent.
func withJWT(t *testing.T, token string, err error) *[]string {
	t.Helper()
	var audiences []string
	old := FetchJWT
	FetchJWT = func(_ context.Context, _, audience string) (string, string, error) {
		audiences = append(audiences, audience)
		return token, testSVID, err
	}
	t.Cleanup(func() { FetchJWT = old })
	return &audiences
}

// sts is a SeaweedFS STS endpoint that answers status and body, recording the
// form it was sent.
func sts(t *testing.T, status int, body string) (*httptest.Server, *url.Values) {
	t.Helper()
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &form
}

func TestFetchCredsAssumesTheCastCacheRoleWithAJWTSVID(t *testing.T) {
	audiences := withJWT(t, testJWT, nil)
	srv, form := sts(t, 200, stsSuccess)
	creds, id, err := FetchCreds(context.Background(), CredsConfig{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if creds != (castcache.Creds{AccessKeyID: "ASIAEXAMPLE", SecretAccessKey: "secretEXAMPLE", SessionToken: "tokenEXAMPLE"}) || id != testSVID {
		t.Errorf("creds %+v for %s", creds, id)
	}
	if len(*audiences) != 1 || (*audiences)[0] != "seaweedfs" {
		t.Errorf("JWT-SVID audiences %v, want [seaweedfs]", *audiences)
	}
	for k, want := range map[string]string{
		"Action": "AssumeRoleWithWebIdentity", "RoleArn": "arn:aws:iam::role/CastCache",
		"WebIdentityToken": testJWT, "Version": "2011-06-15", "DurationSeconds": "3600",
	} {
		if got := form.Get(k); got != want {
			t.Errorf("STS form %s = %q, want %q", k, got, want)
		}
	}
}

func TestFetchCredsFailuresNameTheCauseAndLeakNothing(t *testing.T) {
	denied := `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>Not authorized to perform sts:AssumeRoleWithWebIdentity</Message></Error></ErrorResponse>`
	cases := map[string]struct {
		jwtErr  error
		status  int
		body    string
		wantErr string
	}{
		"the role is not loaded": {nil, 403, denied, "STS AssumeRoleWithWebIdentity on arn:aws:iam::role/CastCache refused: AccessDenied: Not authorized"},
		"STS is down":            {nil, 503, "upstream", "STS AssumeRoleWithWebIdentity on arn:aws:iam::role/CastCache failed"},
		"not an STS answer":      {nil, 200, "<x/>", "STS AssumeRoleWithWebIdentity on arn:aws:iam::role/CastCache failed"},
		"no credentials in 200":  {nil, 200, `<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleWithWebIdentityResult/></AssumeRoleWithWebIdentityResponse>`, "STS answered with no credentials"},
		"no identity":            {errors.New("no identity issued"), 200, stsSuccess, "no JWT-SVID for audience seaweedfs within 30s: no identity issued"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			withJWT(t, testJWT, c.jwtErr)
			srv, _ := sts(t, c.status, c.body)
			_, _, err := FetchCreds(context.Background(), CredsConfig{Endpoint: srv.URL})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error %v, want %q", err, c.wantErr)
			}
			if strings.Contains(err.Error(), testJWT) || strings.Contains(err.Error(), "secretEXAMPLE") {
				t.Errorf("a token or credential reached the error: %v", err)
			}
		})
	}
	withJWT(t, testJWT, nil)
	_, _, err := FetchCreds(context.Background(), CredsConfig{Endpoint: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "STS AssumeRoleWithWebIdentity on arn:aws:iam::role/CastCache failed") || strings.Contains(err.Error(), testJWT) {
		t.Errorf("unreachable STS: %v", err)
	}
	_, _, err = FetchCreds(context.Background(), CredsConfig{Socket: "unix:///nonexistent/agent.sock"})
	if err == nil || !strings.Contains(err.Error(), "the SPIRE socket cannot be opened") {
		t.Errorf("unreachable socket: %v", err)
	}
}

func TestCredsFileRoundTripsAndIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.json")
	want := castcache.Creds{AccessKeyID: "A", SecretAccessKey: "S", SessionToken: "T"}
	if err := WriteCreds(p, want); err != nil {
		t.Fatal(err)
	}
	var got castcache.Creds
	raw, _ := os.ReadFile(p)
	if err := json.Unmarshal(raw, &got); err != nil || got != want {
		t.Errorf("round trip %+v, %v", got, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if !strings.Contains(string(raw), `"access_key_id"`) {
		t.Errorf("the module reads these names: %s", raw)
	}
	if WriteCreds(filepath.Join(t.TempDir(), "no", "dir.json"), want) == nil {
		t.Error("a write to a missing directory succeeded")
	}
}

// Main's creds verb is exercised through a stub agent and a stub STS; its
// answer never holds a credential.
func TestMainCredsReportsWithoutPrintingCredentials(t *testing.T) {
	withJWT(t, testJWT, errors.New("no identity"))
	var out bytes.Buffer
	code := Main(context.Background(), []string{"creds"}, func(k string) string {
		return map[string]string{"CASTCACHE_ENDPOINT": "http://127.0.0.1:1", "CASTCACHE_SOCKET": "x"}[k]
	}, &out)
	if code != 1 || !strings.HasPrefix(out.String(), "credentials unavailable: no JWT-SVID for audience seaweedfs") {
		t.Errorf("exit %d, %q", code, out.String())
	}

	withJWT(t, testJWT, nil)
	srv, _ := sts(t, 200, stsSuccess)
	out.Reset()
	code = Main(context.Background(), []string{"creds"}, func(k string) string {
		return map[string]string{"CASTCACHE_ENDPOINT": srv.URL, "CASTCACHE_STAMP": "9", "CASTCACHE_SOCKET": "x"}[k]
	}, &out)
	// The credentials file lands at its fixed path; where /tmp is not
	// writable the fetch degrades, which is also an answer.
	switch code {
	case 0:
		defer os.Remove(castcache.CredsOut)
		if want := "credentials issued to " + testSVID + " by role arn:aws:iam::role/CastCache\n"; out.String() != want {
			t.Errorf("answer %q", out.String())
		}
	case 1:
		if !strings.HasPrefix(out.String(), "credentials unavailable:") {
			t.Errorf("answer %q", out.String())
		}
	default:
		t.Errorf("exit %d", code)
	}
	if strings.Contains(out.String(), "secretEXAMPLE") || strings.Contains(out.String(), testJWT) {
		t.Errorf("a credential reached stdout: %q", out.String())
	}
}

// restore and save exit 0 whatever happens, and say why they did nothing; a
// malformed command is the one exit 2.
func TestMainArchiveVerbsExitZeroAndSayWhy(t *testing.T) {
	dir := t.TempDir()
	for _, verb := range []string{"restore", "save"} {
		var out bytes.Buffer
		// No secrets are mounted where these tests run.
		code := Main(context.Background(), []string{verb, "--repo", "http://x/a.git", "--toolchain", "t", "--dir", dir}, func(string) string { return "" }, &out)
		if code != 0 || !strings.HasPrefix(out.String(), "cast-cache: "+verb+" skipped: credentials: ") {
			t.Errorf("%s: exit %d, %q", verb, code, out.String())
		}
	}
	for _, args := range [][]string{nil, {"bogus"}, {"restore"}, {"restore", "--nope", "--dir", dir}} {
		var out bytes.Buffer
		if code := Main(context.Background(), args, func(string) string { return "" }, &out); code != 2 || !strings.Contains(out.String(), "usage: casttarget") {
			t.Errorf("%v: exit %d, %q", args, code, out.String())
		}
	}
}

func TestOneLineCapsAndCollapses(t *testing.T) {
	if got := oneLine("a\n  b\t c", 20); got != "a b c" {
		t.Errorf("%q", got)
	}
	if got := oneLine(strings.Repeat("x", 300), 10); got != strings.Repeat("x", 10)+"…" {
		t.Errorf("%q", got)
	}
}
