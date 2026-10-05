package bundlelane

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const (
	kmsProject  = "e4815550-9c4f-4e9b-91a9-4b65d4acc9df"
	kmsIdentity = "739c79f7-9f0b-4ee3-b0de-bc244c4d3418"
)

func TestAnEmptyKMSSpecIsNoSecondSigner(t *testing.T) {
	for _, spec := range []string{"", "   "} {
		k, ok, err := ParseKMS(spec)
		if ok || err != nil || k != (KMS{}) {
			t.Errorf("ParseKMS(%q) = %+v, %v, %v; want no signer and no error", spec, k, ok, err)
		}
	}
}

func TestAKMSSpecNamesTheKeyItsProjectAndItsIdentity(t *testing.T) {
	k, ok, err := ParseKMS(" infisical://fleet-cosign-1?project=" + kmsProject + "&identity=" + kmsIdentity + " ")
	if err != nil || !ok {
		t.Fatalf("ParseKMS: %v, ok=%v", err, ok)
	}
	want := KMS{KeyRef: "infisical://fleet-cosign-1", Project: kmsProject, Identity: kmsIdentity, Site: DefaultKMSSite}
	if k != want {
		t.Fatalf("ParseKMS = %+v, want %+v", k, want)
	}
}

func TestAKMSSpecMayNameItsSite(t *testing.T) {
	k, _, err := ParseKMS("infisical://k/?project=p&identity=i&site=https://kms.example")
	if err != nil {
		t.Fatal(err)
	}
	if k.Site != "https://kms.example" || k.KeyRef != "infisical://k" {
		t.Fatalf("got %+v", k)
	}
}

func TestAKMSSpecThatCannotSignIsRefused(t *testing.T) {
	cases := []struct{ name, spec, why string }{
		{"not a url", "infisical://%zz", "does not parse"},
		{"wrong scheme", "awskms://k?project=p&identity=i", "want infisical://"},
		{"no key", "infisical://?project=p&identity=i", "want infisical://"},
		{"a path is not a key name", "infisical://k/extra?project=p&identity=i", "want infisical://"},
		{"no project", "infisical://k?identity=i", "both required"},
		{"no identity", "infisical://k?project=p", "both required"},
		{"a plaintext site", "infisical://k?project=p&identity=i&site=http://kms", "must be https"},
	}
	for _, c := range cases {
		k, ok, err := ParseKMS(c.spec)
		if err == nil || ok || k != (KMS{}) {
			t.Errorf("%s: ParseKMS(%q) = %+v, %v, %v; want a refusal", c.name, c.spec, k, ok, err)
			continue
		}
		if !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: error %q does not say %q", c.name, err, c.why)
		}
	}
	// The parse failure keeps url.Parse's own error, so a caller can ask it.
	_, _, err := ParseKMS("infisical://%zz")
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		t.Errorf("the parse error %v does not wrap url.Parse's", err)
	}
}

// The plugin logs in with the mounted ServiceAccount token, and nothing in its
// environment is a credential.
func TestTheKMSPluginEnvironmentIsKubernetesAuthOverTheMountedToken(t *testing.T) {
	k := KMS{KeyRef: "infisical://k", Project: "p", Identity: "i", Site: "https://s"}
	want := [][2]string{
		{"INFISICAL_SITE_URL", "https://s"},
		{"INFISICAL_AUTH_METHOD", "kubernetes"},
		{"INFISICAL_MACHINE_IDENTITY_ID", "i"},
		{"INFISICAL_KUBERNETES_SERVICE_ACCOUNT_TOKEN_PATH", "/run/kms/token"},
		{"INFISICAL_PROJECT_ID", "p"},
	}
	if got := k.Env(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Env = %q, want %q", got, want)
	}
}
