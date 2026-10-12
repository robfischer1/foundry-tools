package bundlelane

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
)

// KMS is the second signer of the dual-sign window (plan "Trust Roots to KMS",
// T1): a non-exportable key in an Infisical KMS project, reached by cosign as
// `--key infisical://<name>` through the sigstore-kms-infisical plugin that the
// signing-tools carrier ships.
//
// WHY A SECOND SIGNATURE AND NOT A SWAP. `cosign verify --key` trusts exactly
// one key, so a verifier cannot hold the old key and the new key at once.
// Signing every digest with both keys is how verifiers move one at a time:
// any verifier, on either key, finds a signature it trusts. Once the last
// verifier trusts the KMS key, the file key stops signing. That step is a
// separate, gated landing.
type KMS struct {
	// KeyRef is what cosign takes: infisical://<key name>.
	KeyRef string
	// Project is the KMS project the key lives in (INFISICAL_PROJECT_ID).
	Project string
	// Identity is the machine identity the lane logs in as by kubernetes-auth
	// (INFISICAL_MACHINE_IDENTITY_ID).
	Identity string
	// Site is the Infisical API base (INFISICAL_SITE_URL).
	Site string
}

// DefaultKMSSite is Calypso, the fleet's Infisical.
const DefaultKMSSite = "https://calypso.notusmi.com"

// ParseKMS reads --kms: infisical://<key>?project=<id>&identity=<id>[&site=<url>].
// An empty spec is "no KMS signer" (ok=false, no error). Everything in it is an
// id or a name, never a credential: the credential is the pod's own
// ServiceAccount token, which arrives separately as a Secret.
func ParseKMS(spec string) (kms KMS, ok bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return KMS{}, false, nil
	}
	u, err := url.Parse(spec)
	if err != nil {
		return KMS{}, false, fmt.Errorf("--kms %q does not parse: %w", spec, err)
	}
	if u.Scheme != "infisical" || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return KMS{}, false, fmt.Errorf("--kms %q: want infisical://<key>?project=<id>&identity=<id>", spec)
	}
	q := u.Query()
	kms = KMS{
		KeyRef:   "infisical://" + u.Host,
		Project:  q.Get("project"),
		Identity: q.Get("identity"),
		Site:     q.Get("site"),
	}
	if kms.Project == "" || kms.Identity == "" {
		return KMS{}, false, fmt.Errorf("--kms %q: project and identity are both required", spec)
	}
	if kms.Site == "" {
		kms.Site = DefaultKMSSite
	}
	if !strings.HasPrefix(kms.Site, "https://") {
		return KMS{}, false, fmt.Errorf("--kms %q: site must be https, got %q", spec, kms.Site)
	}
	return kms, true, nil
}

// KMSTokenPath is where the lane mounts the ServiceAccount token the plugin
// logs in with.
const KMSTokenPath = "/run/kms/token"

// Env is the plugin's environment for kubernetes-auth with the token mounted at
// KMSTokenPath. No value in it is a secret.
func (k KMS) Env() [][2]string {
	return [][2]string{
		{"INFISICAL_SITE_URL", k.Site},
		{"INFISICAL_AUTH_METHOD", "kubernetes"},
		{"INFISICAL_MACHINE_IDENTITY_ID", k.Identity},
		{"INFISICAL_KUBERNETES_SERVICE_ACCOUNT_TOKEN_PATH", KMSTokenPath},
		{"INFISICAL_PROJECT_ID", k.Project},
	}
}

// OPAKeyID is the keyid the bundle's signature carries and ouranos-opa pins its
// verification key under (flux prime/star-ouranos.yaml, `keys.ouranos_bundle`).
const OPAKeyID = "ouranos_bundle"

// ParseOpaKMS reads --opa-kms: <project>/<key>[?identity=<id>][&site=<url>],
// the non-exportable P-256 key the OPA bundle twins are signed with
// (Infisical project and key name; the project is what the plugin calls
// INFISICAL_PROJECT_ID, so it is the project's id). The machine identity is
// the one --kms already logs in as unless the spec names another: both keys
// are reached by the lane's ServiceAccount token. An empty spec is "no OPA KMS
// signer" (ok=false, no error).
func ParseOpaKMS(spec string, fallbackIdentity string) (kms KMS, ok bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return KMS{}, false, nil
	}
	path, query, _ := strings.Cut(spec, "?")
	project, key, found := strings.Cut(path, "/")
	if !found || project == "" || key == "" || strings.Contains(key, "/") {
		return KMS{}, false, fmt.Errorf("--opa-kms %q: want <project>/<key>[?identity=<id>]", spec)
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return KMS{}, false, fmt.Errorf("--opa-kms %q does not parse: %w", spec, err)
	}
	kms = KMS{KeyRef: "infisical://" + key, Project: project, Identity: q.Get("identity"), Site: q.Get("site")}
	if kms.Identity == "" {
		kms.Identity = fallbackIdentity
	}
	if kms.Identity == "" {
		return KMS{}, false, fmt.Errorf("--opa-kms %q: no machine identity — name one with ?identity=<id> or give --kms, whose identity it shares", spec)
	}
	if kms.Site == "" {
		kms.Site = DefaultKMSSite
	}
	if !strings.HasPrefix(kms.Site, "https://") {
		return KMS{}, false, fmt.Errorf("--opa-kms %q: site must be https, got %q", spec, kms.Site)
	}
	return kms, true, nil
}

// VerifyArgs is the argv that proves a re-signed bundle loads the way ouranos
// loads it: `opa run` with the KMS key's public half as the verification key
// under OPAKeyID. A bundle that does not verify fails to load, and `opa run`
// exits 1. The query goes on stdin; reaching it at all means the load held.
func VerifyArgs(pubPath, bundle string) []string {
	return []string{"opa", "run", "--bundle", bundle,
		"--verification-key", pubPath, "--verification-key-id", OPAKeyID, "--signing-alg", "ES256"}
}

// CheckOpaPublicKey says whether pemText is the public half of a P-256 key, the
// one curve ES256 means. It is read from the KMS, so a key of another type
// (someone created the wrong one) is caught before anything is signed with it.
func CheckOpaPublicKey(pemText string) error {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemText)))
	if block == nil || block.Type != "PUBLIC KEY" {
		return fmt.Errorf("the KMS answered no PUBLIC KEY block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("the KMS public key does not parse: %w", err)
	}
	k, ok := pub.(*ecdsa.PublicKey)
	if !ok || k.Curve != elliptic.P256() {
		return fmt.Errorf("the KMS key is %T, want an ECDSA P-256 key", pub)
	}
	return nil
}
