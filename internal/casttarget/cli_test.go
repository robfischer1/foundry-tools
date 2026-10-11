package casttarget

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7/pkg/credentials"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// With no CASTCACHE_SOCKET the fetch looks at the agent's standard socket,
// and a socket it cannot open is the reason, not a hang.
func TestMainCredsDefaultsToTheAgentsStandardSocket(t *testing.T) {
	if defaultSocket != "unix:///run/spire/agent.sock" {
		t.Errorf("the default socket is %q", defaultSocket)
	}
	seams(t)
	// Point the default at a path nothing listens on, so a machine that does
	// run an agent gives the same answer.
	defaultSocket = "unix://" + filepath.Join(t.TempDir(), "agent.sock")
	var out bytes.Buffer
	code := Main(context.Background(), []string{"creds"}, envOf(nil), &out)
	if code != 1 || !strings.HasPrefix(out.String(), "credentials unavailable: the SPIRE socket cannot be opened") {
		t.Errorf("exit %d, %q", code, out.String())
	}
}

// Credentials that cannot be left for the lane are "unavailable": the lane
// reads the exit code and mounts nothing.
func TestMainCredsReportsCredentialsItCannotLeave(t *testing.T) {
	seams(t)
	withJWT(t, testJWT, nil)
	srv, _ := sts(t, 200, stsSuccess)
	credsOut = filepath.Join(t.TempDir(), "no", "such", "dir", "creds.json")
	var out bytes.Buffer
	code := Main(context.Background(), []string{"creds"}, envOf(map[string]string{"CASTCACHE_ENDPOINT": srv.URL, "CASTCACHE_SOCKET": "x"}), &out)
	if code != 1 || !strings.HasPrefix(out.String(), "credentials unavailable: they could not be left for the lane: ") {
		t.Errorf("exit %d, %q", code, out.String())
	}
	if strings.Contains(out.String(), "secretEXAMPLE") {
		t.Errorf("a credential reached stdout: %q", out.String())
	}
}

// restore and save, driven the way the lane drives them: credentials from the
// three mounted files, the endpoint from the environment, one line out.
func TestMainRestoreAndSaveRunAgainstTheStore(t *testing.T) {
	seams(t)
	mount := t.TempDir()
	for i, body := range []string{"AKFILE\n", "SKFILE\n", "STFILE\n"} {
		secretFiles[i] = filepath.Join(mount, []string{"id", "secret", "token"}[i])
		if err := os.WriteFile(secretFiles[i], []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, cfg := newS3(t)
	target(t, cfg.Dir, 30)
	env := envOf(map[string]string{"CASTCACHE_ENDPOINT": cfg.Endpoint})
	args := func(verb, dir string) []string {
		return []string{verb, "--repo", cfg.Repo, "--toolchain", cfg.Toolchain, "--dir", dir}
	}

	var out bytes.Buffer
	if code := Main(context.Background(), args("save", cfg.Dir), env, &out); code != 0 || !strings.HasPrefix(out.String(), "cast-cache: saved s3://cast-cache/tongs/rust-1.97.0/") {
		t.Fatalf("save: exit %d, %q", code, out.String())
	}
	out.Reset()
	fresh := t.TempDir()
	if code := Main(context.Background(), args("restore", fresh), env, &out); code != 0 || !strings.HasPrefix(out.String(), "cast-cache: restored s3://cast-cache/tongs/rust-1.97.0/") {
		t.Fatalf("restore: exit %d, %q", code, out.String())
	}
	if read(t, fresh, "release/deps/libaaa.rlib") != "artifact" {
		t.Error("the restored target is missing a file")
	}
	for _, a := range f.auths {
		if !strings.Contains(a, "Credential=AKFILE/") || !strings.HasSuffix(a, "|STFILE") {
			t.Errorf("a request was signed with something other than the mounted credentials: %q", a)
		}
	}
}

// The identity can come from a real socket path: a listener that is there is
// dialled and closed, and the exchange goes on to STS.
func TestFetchCredsOverAnOpenableSocket(t *testing.T) {
	withJWT(t, testJWT, nil)
	srv, _ := sts(t, 200, stsSuccess)
	sock := filepath.Join(t.TempDir(), "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	creds, id, err := FetchCreds(context.Background(), CredsConfig{Socket: "unix://" + sock, Endpoint: srv.URL})
	if err != nil || creds.AccessKeyID != "ASIAEXAMPLE" || id != testSVID {
		t.Errorf("creds %+v id %q error %v", creds, id, err)
	}
}

func TestFetchCredsReturnsTheProvidersConstructionError(t *testing.T) {
	seams(t)
	saved := newProvider
	t.Cleanup(func() { newProvider = saved })
	newProvider = func(string, func() (*credentials.WebIdentityToken, error), ...func(*credentials.STSWebIdentity)) (*credentials.Credentials, error) {
		return nil, errors.New("no token getter")
	}
	if _, _, err := FetchCreds(context.Background(), CredsConfig{}); err == nil || err.Error() != "no token getter" {
		t.Errorf("error %v", err)
	}
}

func TestReadCredsReadsThreeFilesAndNamesTheMissingOne(t *testing.T) {
	seams(t)
	dir := t.TempDir()
	for i, n := range []string{"id", "secret", "token"} {
		secretFiles[i] = filepath.Join(dir, n)
	}
	// Each file missing in turn, the others present.
	for missing := range secretFiles {
		for i, p := range secretFiles {
			os.Remove(p)
			if i != missing {
				if err := os.WriteFile(p, []byte(" v"+p[len(p)-2:]+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		_, err := ReadCreds()
		if err == nil || !strings.HasPrefix(err.Error(), "credentials: ") || !strings.Contains(err.Error(), secretFiles[missing]) {
			t.Errorf("missing %s: error %v", secretFiles[missing], err)
		}
	}
	for i, body := range []string{" id\n", "\tsecret ", "token\n\n"} {
		os.WriteFile(secretFiles[i], []byte(body), 0o600)
	}
	c, err := ReadCreds()
	if err != nil || c.AccessKeyID != "id" || c.SecretAccessKey != "secret" || c.SessionToken != "token" {
		t.Errorf("creds %+v, error %v", c, err)
	}
}
