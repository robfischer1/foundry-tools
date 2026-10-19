package bundlekit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/bundleresign"
)

// built runs the policy and fleet atoms, then plays the sign capability: the
// KMS signs each signing input.
func built(t *testing.T) (*world, kms) {
	t.Helper()
	w := newWorld(t, nil)
	if f := Policy(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	if f := Fleet(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	k := newKMS(t)
	os.WriteFile(filepath.Join(w.out, OpaPubFile), []byte(k.pub(t)), 0o644)
	for _, twin := range []string{PolicySigned, FleetSigned} {
		input := w.read(twin + SigningInputSuffix)
		os.WriteFile(filepath.Join(w.out, twin+SigSuffix), []byte(k.sign(t, input)+"\n"), 0o644)
	}
	return w, k
}

func TestResignWritesTheKMSSignatureIntoBothTwinsAndVerifiesThem(t *testing.T) {
	w, _ := built(t)
	before := w.read(PolicySigned)
	if f := Resign(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	if w.read(PolicySigned) == before || w.read(FleetSigned) == "" {
		t.Error("the twin was not rewritten")
	}
	// The signature is the KMS's, over the signing input with the key id ouranos pins.
	tgz := []byte(w.read(PolicySigned))
	sigs, err := bundleresign.ReadSignatures(tgz)
	if err != nil || !strings.Contains(string(sigs), `"signatures"`) {
		t.Fatalf("%s %v", sigs, err)
	}
	if n := w.ran("run --bundle " + filepath.Join(w.out, PolicySigned) + " --verification-key " + filepath.Join(w.out, OpaPubFile) + " --verification-key-id " + bundlelane.OPAKeyID); n != 1 {
		t.Errorf("verifications %d: %v", n, w.calls)
	}
	// Stdin of the verification is the one query.
	if w.ran("run --bundle") != 2 {
		t.Errorf("runs %v", w.calls)
	}
}

func TestResignRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*world)
		needles []string
	}{
		{"the KMS key is not P-256", func(w *world) { os.WriteFile(filepath.Join(w.out, OpaPubFile), []byte("not a key"), 0o644) }, []string{"the OPA KMS key"}},
		{"no public key", func(w *world) { os.Remove(filepath.Join(w.out, OpaPubFile)) }, []string{OpaPubFile}},
		{"a die was not built", func(w *world) { os.Remove(filepath.Join(w.out, "fleet"+MetaSuffix)) }, []string{"the fleet die was not built"}},
		{"no signature", func(w *world) { os.Remove(filepath.Join(w.out, PolicySigned+SigSuffix)) }, []string{PolicySigned + SigSuffix}},
		{"the signature is not DER", func(w *world) {
			os.WriteFile(filepath.Join(w.out, PolicySigned+SigSuffix), []byte("AAAA"), 0o644)
		}, []string{"bundleresign apply bundle-signed.tar.gz failed"}},
		{"the twin is gone", func(w *world) { os.Remove(filepath.Join(w.out, PolicySigned)) }, []string{PolicySigned}},
		{"the twin does not verify", func(w *world) { w.runCode = 1 }, []string{"opa run verification of bundle-signed.tar.gz", "bundle did not load"}},
		{"the plain bundle is gone", func(w *world) { os.Remove(filepath.Join(w.out, PolicyPlain)) }, []string{PolicyPlain}},
	} {
		w, _ := built(t)
		tc.edit(w)
		expect(t, Resign(context.Background(), w.run()), false, tc.needles...)
	}
}

func TestResignCannotWriteTheTwinBack(t *testing.T) {
	w, _ := built(t)
	failWrite(t, PolicySigned)
	expect(t, Resign(context.Background(), w.run()), false, "bundleresign apply bundle-signed.tar.gz failed")
}

func TestResignOpaThatNeverRuns(t *testing.T) {
	w, _ := built(t)
	inner := w.in.Exec
	w.in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) {
		if c.Name == "opa" && c.Args[0] == "run" {
			return "opa: gone", -1
		}
		return inner(ctx, c)
	}
	expect(t, Resign(context.Background(), w.run()), false, "opa run verification of bundle-signed.tar.gz never ran")
}

func TestTwinGatesTheSignatureAndTheData(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, files map[string]string, signed bool) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, bundleOf(t, files, signed), 0o644)
		return p
	}
	plain := write("plain.tar.gz", map[string]string{"/data.json": `{"a":1}`}, false)
	good := write("good.tar.gz", map[string]string{"/data.json": `{"a":1}`}, true)
	if f := Twin(plain, good, "the twin"); f != nil {
		t.Fatal(f)
	}
	expect(t, Twin(plain, write("unsigned.tar.gz", map[string]string{"/data.json": `{"a":1}`}, false), "the twin"), true, "carries no .signatures.json")
	expect(t, Twin(plain, write("drift.tar.gz", map[string]string{"/data.json": `{"a":2}`}, true), "the twin"), true, "carry different data.json")
	expect(t, Twin(plain, write("nodata.tar.gz", map[string]string{"/x": "y"}, true), "the twin"), true, "carries no /data.json")
	expect(t, Twin(write("nodata2.tar.gz", map[string]string{"/x": "y"}, false), good, "the twin"), true, "carries no /data.json")
	os.WriteFile(filepath.Join(dir, "junk.tar.gz"), []byte("junk"), 0o644)
	expect(t, Twin(plain, filepath.Join(dir, "junk.tar.gz"), "the twin"), true, "carries no .signatures.json")
	expect(t, Twin(plain, filepath.Join(dir, "absent"), "the twin"), false, "absent")
	expect(t, Twin(filepath.Join(dir, "absent"), good, "the twin"), false, "absent")
}

func TestMembersAndMemberReadATarball(t *testing.T) {
	tgz := bundleOf(t, map[string]string{"/data.json": "d", "/b/x.rego": "r"}, false)
	names, err := Members(tgz)
	if err != nil || strings.Join(names, ",") != "/b/x.rego,/data.json" {
		t.Errorf("%v %v", names, err)
	}
	if body, ok, err := Member(tgz, "data.json"); err != nil || !ok || body != "d" {
		t.Errorf("%q %v %v", body, ok, err)
	}
	if _, ok, _ := Member(tgz, "/nothing"); ok {
		t.Error("found a member that is not there")
	}
	if _, err := Members([]byte("junk")); err == nil {
		t.Error("junk is a tarball")
	}
	if _, _, err := Member([]byte("junk"), "x"); err == nil {
		t.Error("junk is a tarball")
	}
}
