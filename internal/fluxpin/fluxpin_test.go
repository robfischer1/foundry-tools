package fluxpin

import (
	"strings"
	"testing"
)

const img = "registry.notusmi.com/rob/narcissus"
const dig = "sha256:0774e298540cdf764d5ed20e57241db84ea1af9c09f8aac16219bed017a05921"

// The real file's shape: a Component, setter comments on each value.
const real = `# header
apiVersion: kustomize.config.k8s.io/v1alpha1
kind: Component
images:
  - name: registry.notusmi.com/rob/mnemosyne
    newTag: 1791061772-06c5e2a # {"$imagepolicy": "flux-system:mnemosyne:tag"}
    digest: sha256:64d2a1ebcab622e8defe3e4a8a316205912a85d5d16306e6ad587ee38854d489 # {"$imagepolicy": "flux-system:mnemosyne:digest"}
  - name: registry.notusmi.com/rob/narcissus
    newTag: 1791061288-a2c1393 # {"$imagepolicy": "flux-system:narcissus:tag"}
    digest: ` + dig + ` # {"$imagepolicy": "flux-system:narcissus:digest"}
`

func TestRefPinsByTagAndDigest(t *testing.T) {
	got, err := Ref([]byte(real), img)
	if err != nil {
		t.Fatal(err)
	}
	if want := img + ":1791061288-a2c1393@" + dig; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRefRefusesByNamingFileAndReason(t *testing.T) {
	for name, tc := range map[string]struct{ body, reason string }{
		"entry missing":    {strings.Replace(real, "rob/narcissus", "rob/other", 1), "has no entry for " + img},
		"digest malformed": {strings.Replace(real, dig, "sha256:abc", 1), "malformed digest"},
		"digest empty":     {strings.Replace(real, "digest: "+dig, "digest:", 1), "malformed digest"},
		"tag missing":      {strings.Replace(real, "newTag: 1791061288-a2c1393", "newTag:", 1), "no newTag"},
		"not yaml":         {"images: [\n", "does not parse"},
		"empty":            {"", "has no entry for"},
	} {
		got, err := Ref([]byte(tc.body), img)
		if err == nil || got != "" {
			t.Errorf("%s: want refusal, got %q, %v", name, got, err)
			continue
		}
		if !strings.Contains(err.Error(), File) || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: error %q must name %s and %q", name, err, File, tc.reason)
		}
	}
}

func TestRefDoesNotMatchAPrefixOfAName(t *testing.T) {
	if _, err := Ref([]byte(real), img+"-extra"); err == nil {
		t.Error("a name that merely extends an entry matched it")
	}
}
