package bundlelane

import (
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
