// Package bundleresign re-signs an OPA bundle's .signatures.json with a
// signature made elsewhere, so the private key can stay in a KMS.
//
// WHY THIS EXISTS. OPA signs only with a key it can read (`opa build
// --signing-key` takes a PEM or an HMAC secret), and the bundle key lived in a
// CI secret as a result. A KMS key never leaves the KMS, so the lane lets OPA
// build the bundle with a throwaway key, which makes OPA compute the file
// hashes and the canonical payload itself, then replaces the one thing the
// throwaway key contributed: the signature over `header.payload`. This package
// is the byte work around that exchange; the KMS call is the lane's.
//
//	SigningInput   the `header.payload` to send to the KMS, with the key id set
//	Apply          the tarball with the KMS's signature written back in
//	DERToRS        ES256 wants r||s; a KMS answers ASN.1 DER
//
// Standard library only: the helper binary built from this must build with the
// network refused.
package bundleresign

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// SignaturesMember is the tar member OPA keeps the JWS in. OPA writes member
// names with a leading slash; both spellings are recognised.
const SignaturesMember = ".signatures.json"

func norm(name string) string { return strings.TrimPrefix(strings.TrimPrefix(name, "/"), "./") }

func isSignatures(name string) bool { return norm(name) == SignaturesMember }

// signatureFile is OPA's .signatures.json: a list of compact JWS tokens.
type signatureFile struct {
	Signatures []string `json:"signatures"`
}

// jws is the three parts of the one token a bundle carries.
type jws struct{ header, payload string }

// ReadSignatures answers the bytes of .signatures.json inside a bundle tarball.
func ReadSignatures(tgz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("the bundle is not gzip: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the bundle carries no " + SignaturesMember)
		}
		if err != nil {
			return nil, fmt.Errorf("the bundle is not a tar: %w", err)
		}
		if isSignatures(h.Name) {
			return io.ReadAll(tr)
		}
	}
}

// parse reads the single token out of .signatures.json. A bundle with none, or
// with several, is not one this lane built.
func parse(sigs []byte) (jws, error) {
	var f signatureFile
	if err := json.Unmarshal(sigs, &f); err != nil {
		return jws{}, fmt.Errorf("%s is not JSON: %w", SignaturesMember, err)
	}
	if len(f.Signatures) != 1 {
		return jws{}, fmt.Errorf("%s carries %d signatures, want exactly 1", SignaturesMember, len(f.Signatures))
	}
	parts := strings.Split(f.Signatures[0], ".")
	if len(parts) != 3 {
		return jws{}, fmt.Errorf("the signature is not a compact JWS: %d parts", len(parts))
	}
	return jws{header: parts[0], payload: parts[1]}, nil
}

// compactJSON marshals without HTML escaping, which Go does by default and
// which would change bytes nothing asked it to.
// Its inputs are decoded JSON, which always encodes, so it has no error.
func compactJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

var b64 = base64.RawURLEncoding

// SigningInput answers the `header.payload` the KMS signs. The header's kid is
// set to keyID, and so is the payload's keyid claim when OPA wrote one, so the
// two never disagree about which key signed. Everything else in both is what
// OPA computed: the file list, hashes and algorithm names are untouched.
func SigningInput(tgz []byte, keyID string) (string, error) {
	if keyID == "" {
		return "", errors.New("a key id is required")
	}
	sigs, err := ReadSignatures(tgz)
	if err != nil {
		return "", err
	}
	tok, err := parse(sigs)
	if err != nil {
		return "", err
	}
	hdrRaw, err := b64.DecodeString(tok.header)
	if err != nil {
		return "", fmt.Errorf("the JWS header is not base64url: %w", err)
	}
	hdr := map[string]any{}
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
		return "", fmt.Errorf("the JWS header is not JSON: %w", err)
	}
	if alg, _ := hdr["alg"].(string); alg != "ES256" {
		return "", fmt.Errorf("the JWS header says alg %q, this re-signs ES256 only", alg)
	}
	hdr["kid"] = keyID
	payRaw, err := b64.DecodeString(tok.payload)
	if err != nil {
		return "", fmt.Errorf("the JWS payload is not base64url: %w", err)
	}
	pay := map[string]json.RawMessage{}
	if err := json.Unmarshal(payRaw, &pay); err != nil {
		return "", fmt.Errorf("the JWS payload is not a JSON object: %w", err)
	}
	if _, ok := pay["files"]; !ok {
		return "", errors.New("the JWS payload lists no files")
	}
	if _, ok := pay["keyid"]; ok {
		pay["keyid"] = compactJSON(keyID)
	}
	return b64.EncodeToString(compactJSON(hdr)) + "." + b64.EncodeToString(compactJSON(pay)), nil
}

// DERToRS converts an ASN.1 DER ECDSA signature (SEQUENCE { r, s }) to the
// fixed-width r||s of JWS ES256: each integer left-padded with zeros to 32
// bytes. DER drops leading zeros (a short integer) and adds one in front of a
// high bit (33 bytes), so neither is 32 and a byte slice cannot be trusted to
// be. Anything that is not exactly one well-formed pair of P-256-sized positive
// integers is refused.
func DERToRS(der []byte) ([]byte, error) {
	var sig struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(der, &sig)
	if err != nil {
		return nil, fmt.Errorf("the signature is not DER: %w", err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("the signature has %d trailing bytes after its DER", len(rest))
	}
	for name, n := range map[string]*big.Int{"r": sig.R, "s": sig.S} {
		if n.Sign() <= 0 {
			return nil, fmt.Errorf("the signature's %s is not positive", name)
		}
		if n.BitLen() > 256 {
			return nil, fmt.Errorf("the signature's %s is %d bits, wider than P-256", name, n.BitLen())
		}
	}
	out := make([]byte, 64)
	sig.R.FillBytes(out[:32])
	sig.S.FillBytes(out[32:])
	return out, nil
}

// SignaturesJSON answers a .signatures.json carrying the one token, in the
// layout OPA writes.
func SignaturesJSON(token string) []byte {
	out, _ := json.MarshalIndent(signatureFile{Signatures: []string{token}}, "", " ") // a string list always encodes
	return out
}

// Apply answers the bundle with its .signatures.json replaced by one signed by
// derB64 (the base64 DER a KMS answered) over SigningInput(tgz, keyID). Every
// other member keeps its place, header and bytes.
func Apply(tgz []byte, keyID, derB64 string) ([]byte, error) {
	input, err := SigningInput(tgz, keyID)
	if err != nil {
		return nil, err
	}
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(derB64))
	if err != nil {
		return nil, fmt.Errorf("the signature is not base64: %w", err)
	}
	rs, err := DERToRS(der)
	if err != nil {
		return nil, err
	}
	return ReplaceMember(tgz, SignaturesMember, SignaturesJSON(input+"."+b64.EncodeToString(rs)))
}

// entry is one tar member: its header, as read, and its body.
type entry struct {
	h    *tar.Header
	body []byte
}

// ReplaceMember answers tgz with the named member's body replaced. Order and
// every other header field (mode, owner, times) are preserved; only the size
// follows the new body. A bundle without the member is an error.
func ReplaceMember(tgz []byte, name string, body []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("the bundle is not gzip: %w", err)
	}
	tr := tar.NewReader(zr)
	var entries []entry
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the bundle is not a tar: %w", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("the bundle is cut off inside %s: %w", h.Name, err)
		}
		if norm(h.Name) == norm(name) {
			found = true
			content = body
			h.Size = int64(len(body))
		}
		entries = append(entries, entry{h, content})
	}
	if !found {
		return nil, errors.New("the bundle carries no " + name)
	}
	return encode(entries)
}

// encode writes the members as a gzip tar, in order. The first write error is
// the one answered.
func encode(entries []entry) ([]byte, error) {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, e := range entries {
		keep(tw.WriteHeader(e.h))
		_, err := tw.Write(e.body)
		keep(err)
	}
	keep(tw.Close())
	keep(zw.Close())
	return out.Bytes(), first
}
