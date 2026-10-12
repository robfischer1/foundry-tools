package bundlelane

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundleresign"
)

// THESE RUN THE REAL opa. The claim under test is that OPA 1.18 loads a bundle
// whose signature OPA did not make, so a stand-in for opa would test nothing.
// Where there is no opa on PATH they skip, loudly; OPA_REQUIRED=1 (set it in
// any lane that ships the binary) turns the skip into a failure, so a lane that
// lost its opa cannot go green on these.
func opaBinary(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("opa")
	if err != nil {
		if os.Getenv("OPA_REQUIRED") != "" {
			t.Fatalf("OPA_REQUIRED is set and there is no opa on PATH: %v", err)
		}
		t.Skip("no opa on PATH (set OPA_REQUIRED=1 to make this a failure)")
	}
	return p
}

func runIn(t *testing.T, stdin string, argv ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// fakeKMS is a P-256 key held in memory that signs a message the way the KMS
// does with isDigest:false: ECDSA over sha256(message), answered as base64 DER.
type fakeKMS struct{ key *ecdsa.PrivateKey }

func newFakeKMS(t *testing.T) fakeKMS {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return fakeKMS{k}
}

func (f fakeKMS) sign(t *testing.T, message string) string {
	t.Helper()
	h := sha256.Sum256([]byte(message))
	d, err := ecdsa.SignASN1(rand.Reader, f.key, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(d)
}

// public is the PUBLIC KEY block the KMS serves (cosign public-key).
func (f fakeKMS) public(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// builtBundle is what the lane builds before the KMS is asked: the real
// `opa build`, signed with the throwaway key EphemeralKey mints.
func builtBundle(t *testing.T, opa string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"data.json": `{"a":1}`, "x.rego": "package x\nallow := true\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	throwaway, err := EphemeralKey()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "throwaway.key")
	if err := os.WriteFile(keyPath, []byte(throwaway), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "signed.tar.gz")
	if o, err := runIn(t, "", opa, "build", "-b", src, "-o", out, "--revision", "r", "--signing-key", keyPath, "--signing-alg", "ES256"); err != nil {
		t.Fatalf("opa build: %v\n%s", err, o)
	}
	return out
}

func resigned(t *testing.T, path string, kms fakeKMS) {
	t.Helper()
	tgz, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	in, err := bundleresign.SigningInput(tgz, OPAKeyID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := bundleresign.Apply(tgz, OPAKeyID, kms.sign(t, in))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// loads is the lane's gate: opa run with the verification key, the same argv.
func loads(t *testing.T, bundle string, pub string) (string, error) {
	t.Helper()
	pubPath := filepath.Join(filepath.Dir(bundle), "verify.pub")
	if err := os.WriteFile(pubPath, []byte(pub), 0o644); err != nil {
		t.Fatal(err)
	}
	return runIn(t, "data.x.allow\n", VerifyArgs(pubPath, bundle)...)
}

func TestAResignedBundleVerifiesAndEvaluates(t *testing.T) {
	opa := opaBinary(t)
	bundle := builtBundle(t, opa)
	kms := newFakeKMS(t)
	if out, err := loads(t, bundle, kms.public(t)); err == nil {
		t.Fatalf("the throwaway-signed bundle verified under the KMS key before it was re-signed:\n%s", out)
	}
	resigned(t, bundle, kms)
	out, err := loads(t, bundle, kms.public(t))
	if err != nil {
		t.Fatalf("a re-signed bundle did not load: %v\n%s", err, out)
	}
	if !strings.Contains(out, "true") {
		t.Errorf("the bundle loaded but data.x.allow did not evaluate true:\n%s", out)
	}
}

func TestATamperedFileIsRejected(t *testing.T) {
	opa := opaBinary(t)
	bundle := builtBundle(t, opa)
	kms := newFakeKMS(t)
	resigned(t, bundle, kms)
	tgz, _ := os.ReadFile(bundle)
	// The policy file changes after the signature was made.
	tampered, err := bundleresign.ReplaceMember(tgz, memberEnding(t, tgz, "x.rego"), []byte("package x\nallow := false\n"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(bundle, tampered, 0o600)
	if out, err := loads(t, bundle, kms.public(t)); err == nil {
		t.Fatalf("a bundle with an altered file loaded:\n%s", out)
	}
}

func TestABundleSignedByAnotherKeyIsRejected(t *testing.T) {
	opa := opaBinary(t)
	bundle := builtBundle(t, opa)
	resigned(t, bundle, newFakeKMS(t))
	if out, err := loads(t, bundle, newFakeKMS(t).public(t)); err == nil {
		t.Fatalf("a bundle signed by one key loaded under another:\n%s", out)
	}
}

// memberEnding is the bundle member whose name ends in suffix: OPA names a
// policy file by the path it was built from.
func memberEnding(t *testing.T, tgz []byte, suffix string) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("no member ends in %s", suffix)
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(h.Name, suffix) {
			return h.Name
		}
	}
}
