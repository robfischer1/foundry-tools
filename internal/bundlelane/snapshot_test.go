package bundlelane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// THE REAL FLUX TREE, NOT A HAND-CUT ONE. testdata/flux is foundry/flux's
// prime/star-*.yaml and data/*.yaml at the commit in testdata/flux/SOURCE,
// with full-line comments, owner emails and kube-linter ignore annotations
// dropped (this repo's fleet:stop-justifications would read those as its own
// suppressions) — every document and every other field kept. A hand-cut
// fixture only holds the shapes its author thought of; flux 6a559a4 put a
// World (spec.ports a map) in prime/star-narcissus.yaml and the hand-cut
// fixtures passed while the lane would have refused every fleet landing.
// Refresh it from a flux checkout, T being this testdata/flux:
//
//	for p in $(git ls-tree --name-only origin/main prime/ | grep 'prime/star-.*\.yaml$') \
//	         $(git ls-tree --name-only origin/main data/ | grep '\.yaml$'); do
//	  git show origin/main:$p | grep -v -e '^\s*#' -e 'ignore-check\.kube-linter\.io' |
//	    sed -E 's/^(\s*email:) .*/\1 owner@example.invalid/' > $T/$p
//	done; git rev-parse origin/main > $T/SOURCE
func snapshot(t *testing.T) Inputs {
	t.Helper()
	read := func(glob string) map[string]string {
		paths, err := filepath.Glob(filepath.Join("testdata", "flux", glob))
		if err != nil || len(paths) == 0 {
			t.Fatalf("testdata/flux/%s: %v (%d files)", glob, err, len(paths))
		}
		out := map[string]string{}
		for _, p := range paths {
			body, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			out[strings.TrimPrefix(filepath.ToSlash(p), "testdata/flux/")] = string(body)
		}
		return out
	}
	in := Inputs{FluxSHA: testFluxSHA, Tier: `{"map":{}}`, StarManifests: read(StarManifestGlob), ClusterManifest: read(ClusterGlob)}
	for p := range in.StarManifests {
		star := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "star-"), ".yaml")
		in.Shards = append(in.Shards, "fleet/stars/"+star+"/slag.json")
	}
	return in
}

func TestInjectReadsTheRealFluxTree(t *testing.T) {
	inj, err := Inject(snapshot(t))
	if err != nil {
		t.Fatalf("Inject over the real flux tree: %v", err)
	}
	var flux map[string]FluxFact
	if err := json.Unmarshal([]byte(inj.Flux), &flux); err != nil {
		t.Fatal(err)
	}
	if inj.FluxRows < 35 {
		t.Fatalf("only %d of %d stars got a flux row", inj.FluxRows, inj.Known)
	}
	for star, want := range map[string]FluxFact{
		"hades":     {Ports: &Ports{Listen: 8101, MTLS: 8102}},
		"chaos":     {Ports: &Ports{Listen: 8206, MTLS: 8207}, DB: []string{"chaos-db"}},
		"ourea":     {Ports: &Ports{Listen: 8214, MTLS: 8216}, DB: []string{"erebus-db", "ourea-db"}},
		"ouranos":   {Ports: &Ports{Listen: 8181}},
		"narcissus": {Ports: &Ports{Listen: 8200, MTLS: 8201}, DB: []string{"narcissus-db"}},
	} {
		if got := flux[star]; !reflect.DeepEqual(got, want) {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			t.Errorf("%s = %s, want %s", star, a, b)
		}
	}
}

// The narcissus World, as flux carries it: its spec.ports is a map of peers.
const worldDoc = `apiVersion: notusmi.com/v1
kind: World
metadata:
  name: narcissus
spec:
  world: notusmi:narcissus/narcissus-star@0.2.0
  ports:
    clones-of: narcissus-db-rw:5432
    nearest: narcissus-db-rw:5432
  template:
    spec:
      containers:
        - name: narcissus
          env:
            - {name: NARCISSUS_DATABASE_URL, value: "postgresql://narcissus_svid@narcissus-db-rw:5432/narcissus?sslmode=verify-full&sslrootcert=/etc/stellar/narcissus-db-ca/ca.crt"}
---
kind: Service
metadata: {name: narcissus}
spec:
  ports:
    - {name: mcp, port: 8200}
`

func TestAKindWhoseSpecIsNotAServicesIsNeverDecodedAsOne(t *testing.T) {
	got, err := FluxFacts(map[string]string{"prime/star-narcissus.yaml": worldDoc + "---\nkind: SomeCR\nmetadata: {name: x}\nspec: {ports: 7, template: oops}\n"},
		map[string]bool{"narcissus": true}, map[string]bool{"narcissus-db": true})
	if err != nil {
		t.Fatalf("a World's map-shaped ports or a CR's spec failed the read: %v", err)
	}
	want := FluxFact{Ports: &Ports{Listen: 8200}, DB: []string{"narcissus-db"}}
	if !reflect.DeepEqual(got["narcissus"], want) {
		t.Fatalf("narcissus = %+v", got["narcissus"])
	}
}

func TestAServiceOrWorkloadOfTheWrongShapeIsNamed(t *testing.T) {
	for name, c := range map[string]struct{ doc, want string }{
		"a Service whose ports are a map":     {"kind: Service\nmetadata: {name: chaos}\nspec: {ports: {mcp: 1}}\n", "Service chaos"},
		"a Deployment whose template is text": {"kind: Deployment\nmetadata: {name: chaos}\nspec: {template: text}\n", "Deployment chaos"},
		"a head that is not an object":        {"kind: Service\nmetadata: [chaos]\n", "prime/star-chaos.yaml"},
	} {
		_, err := FluxFacts(map[string]string{"prime/star-chaos.yaml": c.doc}, map[string]bool{"chaos": true}, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
