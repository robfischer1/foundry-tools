package bundleresign

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func der(t *testing.T, r, s *big.Int) []byte {
	t.Helper()
	b, err := asn1.Marshal(struct{ R, S *big.Int }{r, s})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// DER drops leading zero bytes (short integers) and prefixes a zero byte to a
// high-bit integer (33 bytes). Neither is the 32 bytes r||s wants.
func TestDERToRSEdgeCases(t *testing.T) {
	high := new(big.Int).SetBytes(bytes.Repeat([]byte{0xff}, 32)) // 33 bytes in DER
	one := big.NewInt(1)                                          // 1 byte in DER
	mid := new(big.Int).SetBytes(append([]byte{0, 0, 0x7f}, bytes.Repeat([]byte{0x11}, 29)...))
	for name, tc := range map[string]struct{ r, s *big.Int }{
		"both high-bit":      {high, high},
		"r short, s high":    {one, high},
		"r high, s short":    {high, one},
		"both one byte":      {one, big.NewInt(0x7f)},
		"leading zeros kept": {mid, mid},
	} {
		t.Run(name, func(t *testing.T) {
			d := der(t, tc.r, tc.s)
			rs, err := DERToRS(d)
			if err != nil {
				t.Fatal(err)
			}
			if len(rs) != 64 {
				t.Fatalf("len %d, want 64", len(rs))
			}
			if new(big.Int).SetBytes(rs[:32]).Cmp(tc.r) != 0 || new(big.Int).SetBytes(rs[32:]).Cmp(tc.s) != 0 {
				t.Errorf("r||s %x does not carry r=%x s=%x", rs, tc.r, tc.s)
			}
		})
	}
	// The wire shapes the edge cases come from, pinned.
	if d := der(t, high, one); len(d) != 2+2+33+2+1 {
		t.Errorf("a high-bit r and a short s should be 33 and 1 bytes in DER: %x", d)
	}
}

func TestDERToRSRefusesWhatIsNotOneP256Signature(t *testing.T) {
	good := der(t, big.NewInt(5), big.NewInt(6))
	wide := new(big.Int).Lsh(big.NewInt(1), 256) // 257 bits
	for name, d := range map[string][]byte{
		"empty":         nil,
		"not DER":       {0x01, 0x02, 0x03},
		"raw r||s":      bytes.Repeat([]byte{7}, 64),
		"trailing byte": append(append([]byte{}, good...), 0),
		"zero r":        der(t, big.NewInt(0), big.NewInt(6)),
		"negative s":    der(t, big.NewInt(5), big.NewInt(-6)),
		"r wider":       der(t, wide, big.NewInt(6)),
	} {
		t.Run(name, func(t *testing.T) {
			if rs, err := DERToRS(d); err == nil {
				t.Errorf("accepted, answered %x", rs)
			}
		})
	}
}

func TestDERToRSAgreesWithEveryRealSignature(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for i := 0; i < 500; i++ {
		h := sha256.Sum256([]byte{byte(i), byte(i >> 8)})
		d, err := ecdsa.SignASN1(rand.Reader, k, h[:])
		if err != nil {
			t.Fatal(err)
		}
		rs, err := DERToRS(d)
		if err != nil {
			t.Fatal(err)
		}
		if !ecdsa.Verify(&k.PublicKey, h[:], new(big.Int).SetBytes(rs[:32]), new(big.Int).SetBytes(rs[32:])) {
			t.Fatalf("r||s of signature %d does not verify", i)
		}
	}
}

// fakeBundle is a gzip tar shaped the way OPA writes one: leading-slash names,
// mode 0600, .signatures.json in the middle, signed ES256 by a throwaway key
// under kid "default".
func fakeBundle(t *testing.T, files map[string]string) (tgz []byte, order []string) {
	t.Helper()
	order = []string{"/data.json", "/b/x.rego", "/.signatures.json", "/.manifest"}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var listed []map[string]string
	for _, n := range order {
		if n == "/.signatures.json" {
			continue
		}
		h := sha256.Sum256([]byte(files[n]))
		listed = append(listed, map[string]string{"name": strings.TrimPrefix(n, "/"), "hash": hex.EncodeToString(h[:]), "algorithm": "SHA-256"})
	}
	hdr, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": "default", "typ": "JWT"})
	pay, _ := json.Marshal(map[string]any{"files": listed, "keyid": "default"})
	in := b64.EncodeToString(hdr) + "." + b64.EncodeToString(pay)
	h := sha256.Sum256([]byte(in))
	r, s, _ := ecdsa.Sign(rand.Reader, k, h[:])
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	files["/.signatures.json"] = `{"signatures":["` + in + "." + b64.EncodeToString(sig) + `"]}`
	return tarOf(t, order, files, 0o600), order
}

func tarOf(t *testing.T, order []string, files map[string]string, mode int64) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for _, n := range order {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: mode, Size: int64(len(files[n])), ModTime: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(files[n]))
	}
	tw.Close()
	zw.Close()
	return out.Bytes()
}

func members(t *testing.T, tgz []byte) (names []string, bodies map[string]string, modes map[string]int64) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	bodies, modes = map[string]string{}, map[string]int64{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		names = append(names, h.Name)
		bodies[h.Name], modes[h.Name] = string(b), h.Mode
	}
}

func sources() map[string]string {
	return map[string]string{"/data.json": `{"a":1}`, "/b/x.rego": "package x\nallow := true\n", "/.manifest": `{"revision":"r"}`}
}

func TestSigningInputSetsKidAndKeepsOPAsFiles(t *testing.T) {
	tgz, _ := fakeBundle(t, sources())
	in, err := SigningInput(tgz, "ouranos_bundle")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(in, ".")
	if len(parts) != 2 {
		t.Fatalf("%q is not header.payload", in)
	}
	hb, _ := b64.DecodeString(parts[0])
	var hdr map[string]string
	json.Unmarshal(hb, &hdr)
	if hdr["kid"] != "ouranos_bundle" || hdr["alg"] != "ES256" || hdr["typ"] != "JWT" {
		t.Errorf("header %v", hdr)
	}
	pb, _ := b64.DecodeString(parts[1])
	var pay struct {
		Files []map[string]string `json:"files"`
		KeyID string              `json:"keyid"`
	}
	if err := json.Unmarshal(pb, &pay); err != nil {
		t.Fatal(err)
	}
	if pay.KeyID != "ouranos_bundle" || len(pay.Files) != 3 || pay.Files[1]["name"] != "b/x.rego" {
		t.Errorf("payload %s", pb)
	}
}

func TestSigningInputRefusesWhatItCannotResign(t *testing.T) {
	good := sources()
	tgz, _ := fakeBundle(t, good)
	if _, err := SigningInput(tgz, ""); err == nil {
		t.Error("an empty key id was accepted")
	}
	if _, err := SigningInput([]byte("not gzip"), "k"); err == nil {
		t.Error("a non-gzip bundle was accepted")
	}
	unsigned := tarOf(t, []string{"/data.json"}, map[string]string{"/data.json": "{}"}, 0o600)
	if _, err := SigningInput(unsigned, "k"); err == nil || !strings.Contains(err.Error(), "no .signatures.json") {
		t.Errorf("an unsigned bundle: %v", err)
	}
	hs256 := b64.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + b64.EncodeToString([]byte(`{"files":[]}`)) + ".x"
	for name, sigs := range map[string]string{
		"two tokens": `{"signatures":["a.b.c","a.b.c"]}`,
		"not JWS":    `{"signatures":["abc"]}`,
		"not JSON":   `nope`,
		"HS256":      `{"signatures":["` + hs256 + `"]}`,
	} {
		files := sources()
		files["/.signatures.json"] = sigs
		b := tarOf(t, []string{"/data.json", "/.signatures.json"}, files, 0o600)
		if _, err := SigningInput(b, "k"); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// signES256 is the fake KMS: a local P-256 key that answers a base64 DER
// signature over sha256(message), as a KMS signing a message (isDigest false)
// does.
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

// verifyJWS checks a compact ES256 JWS with the standard library.
func verifyJWS(token string, pub *ecdsa.PublicKey) bool {
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return false
	}
	sig, err := b64.DecodeString(p[2])
	if err != nil || len(sig) != 64 {
		return false
	}
	h := sha256.Sum256([]byte(p[0] + "." + p[1]))
	return ecdsa.Verify(pub, h[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

func TestApplyWritesAVerifiableSignatureAndTouchesNothingElse(t *testing.T) {
	files := sources()
	tgz, order := fakeBundle(t, files)
	kms := newFakeKMS(t)
	in, err := SigningInput(tgz, "ouranos_bundle")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Apply(tgz, "ouranos_bundle", kms.sign(t, in))
	if err != nil {
		t.Fatal(err)
	}
	names, bodies, modes := members(t, out)
	if strings.Join(names, ",") != strings.Join(order, ",") {
		t.Errorf("member order %v, want %v", names, order)
	}
	for _, n := range order {
		if modes[n] != 0o600 {
			t.Errorf("%s mode %o", n, modes[n])
		}
		if n != "/.signatures.json" && bodies[n] != files[n] {
			t.Errorf("%s changed", n)
		}
	}
	var sf struct{ Signatures []string }
	if err := json.Unmarshal([]byte(bodies["/.signatures.json"]), &sf); err != nil || len(sf.Signatures) != 1 {
		t.Fatalf("signatures file %q: %v", bodies["/.signatures.json"], err)
	}
	if !verifyJWS(sf.Signatures[0], &kms.key.PublicKey) {
		t.Error("the written JWS does not verify under the KMS key")
	}
	other := newFakeKMS(t)
	if verifyJWS(sf.Signatures[0], &other.key.PublicKey) {
		t.Error("the written JWS verifies under a different key")
	}
	if !strings.HasPrefix(sf.Signatures[0], strings.SplitN(in, ".", 2)[0]+".") {
		t.Error("the JWS header is not the one that was signed")
	}
}

func TestApplyRefusesASignatureThatIsNotDER(t *testing.T) {
	tgz, _ := fakeBundle(t, sources())
	for name, sig := range map[string]string{
		"not base64": "***",
		"raw r||s":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 64)),
		"empty":      "",
	} {
		if _, err := Apply(tgz, "k", sig); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestReplaceMemberRefusesAMissingMember(t *testing.T) {
	tgz, _ := fakeBundle(t, sources())
	if _, err := ReplaceMember(tgz, "nope", nil); err == nil {
		t.Error("replaced a member that is not there")
	}
}

func TestMainInputAndApplyInPlace(t *testing.T) {
	tgz, _ := fakeBundle(t, sources())
	dir := t.TempDir()
	bundle := filepath.Join(dir, "b.tar.gz")
	os.WriteFile(bundle, tgz, 0o600)
	var out, errb bytes.Buffer
	if rc := Main([]string{"input", bundle, "ouranos_bundle"}, &out, &errb); rc != 0 {
		t.Fatalf("input rc %d: %s", rc, errb.String())
	}
	kms := newFakeKMS(t)
	sigFile := filepath.Join(dir, "sig")
	os.WriteFile(sigFile, []byte(kms.sign(t, out.String())+"\n"), 0o600)
	if rc := Main([]string{"apply", bundle, "ouranos_bundle", sigFile}, &out, &errb); rc != 0 {
		t.Fatalf("apply rc %d: %s", rc, errb.String())
	}
	written, _ := os.ReadFile(bundle)
	_, bodies, _ := members(t, written)
	var sf struct{ Signatures []string }
	json.Unmarshal([]byte(bodies["/.signatures.json"]), &sf)
	if len(sf.Signatures) != 1 || !verifyJWS(sf.Signatures[0], &kms.key.PublicKey) {
		t.Errorf("the bundle on disk does not carry the KMS signature: %q", bodies["/.signatures.json"])
	}
	if _, err := os.Stat(bundle + ".resigned"); err == nil {
		t.Error("the temp file was left behind")
	}
	for _, args := range [][]string{nil, {"input"}, {"sign", bundle, "k"}, {"apply", bundle, "k"}, {"input", filepath.Join(dir, "missing"), "k"}} {
		if rc := Main(args, io.Discard, io.Discard); rc == 0 {
			t.Errorf("%v exited 0", args)
		}
	}
}
