package bundlelane

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-policy-agent/opa/v1/bundle"

	"dagger/foundry-tools/internal/bundleresign"
)

// THESE NEVER SKIP. They verify through OPA's own bundle reader
// (github.com/open-policy-agent/opa/v1/bundle, pinned to the OPA the fleet
// runs, checks.OpaVersion), which is the verifier `opa run` and ouranos call:
// the same VerificationConfig, the same ES256 check, the same per-file hash
// comparison. The binary-based twins in opa_resign_test.go skip where the lane
// ships no opa; these are the proof that holds in every lane.

// libBuilt is what the lane builds before the KMS is asked, built with the
// library instead of the binary: the policy directory read the way `opa build`
// reads it, signed ES256 by a throwaway key under the key id `default`.
func libBuilt(t *testing.T) []byte {
	t.Helper()
	src := t.TempDir()
	for name, body := range map[string]string{"data.json": `{"a":1}`, "x.rego": "package x\n\nallow := true\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loader, err := bundle.NewFSLoader(os.DirFS(src))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.NewCustomReader(loader).WithRegoVersion(1).Read()
	if err != nil {
		t.Fatal(err)
	}
	b.Manifest.Revision = "r"
	throwaway, err := EphemeralKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.GenerateSignature(bundle.NewSigningConfig(throwaway, "ES256", ""), "default", false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := bundle.NewWriter(&out).Write(b); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// libVerify is the lane's gate through the library: the KMS key's public half
// as the verification key under the key id ouranos pins.
func libVerify(tgz []byte, pub string) error {
	cfg := bundle.NewVerificationConfig(map[string]*bundle.KeyConfig{
		OPAKeyID: {Key: pub, Algorithm: "ES256"},
	}, OPAKeyID, "", nil)
	_, err := bundle.NewReader(bytes.NewReader(tgz)).WithBundleVerificationConfig(cfg).Read()
	return err
}

func libResign(t *testing.T, tgz []byte, kms fakeKMS) []byte {
	t.Helper()
	in, err := bundleresign.SigningInput(tgz, OPAKeyID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := bundleresign.Apply(tgz, OPAKeyID, kms.sign(t, in))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLibraryAResignedBundleVerifies(t *testing.T) {
	built := libBuilt(t)
	kms := newFakeKMS(t)
	if err := libVerify(built, kms.public(t)); err == nil {
		t.Fatal("the throwaway-signed bundle verified under the KMS key before it was re-signed")
	}
	if err := libVerify(libResign(t, built, kms), kms.public(t)); err != nil {
		t.Fatalf("a re-signed bundle did not verify: %v", err)
	}
}

func TestLibraryAResignedBundleStillCarriesItsContent(t *testing.T) {
	kms := newFakeKMS(t)
	cfg := bundle.NewVerificationConfig(map[string]*bundle.KeyConfig{OPAKeyID: {Key: kms.public(t), Algorithm: "ES256"}}, OPAKeyID, "", nil)
	b, err := bundle.NewReader(bytes.NewReader(libResign(t, libBuilt(t), kms))).WithBundleVerificationConfig(cfg).Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Modules) != 1 || b.Data["a"] == nil || b.Manifest.Revision != "r" {
		t.Errorf("modules %d, data %v, revision %q", len(b.Modules), b.Data, b.Manifest.Revision)
	}
}

func TestLibraryATamperedFileIsRejected(t *testing.T) {
	kms := newFakeKMS(t)
	signed := libResign(t, libBuilt(t), kms)
	for _, tc := range []struct{ suffix, body string }{
		{"x.rego", "package x\n\nallow := false\n"},
		{"data.json", `{"a":2}`},
	} {
		tampered, err := bundleresign.ReplaceMember(signed, memberEnding(t, signed, tc.suffix), []byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if err := libVerify(tampered, kms.public(t)); err == nil {
			t.Errorf("a bundle with an altered %s verified", tc.suffix)
		}
	}
}

func TestLibraryABundleSignedByAnotherKeyIsRejected(t *testing.T) {
	signed := libResign(t, libBuilt(t), newFakeKMS(t))
	if err := libVerify(signed, newFakeKMS(t).public(t)); err == nil {
		t.Fatal("a bundle signed by one key verified under another")
	}
}
