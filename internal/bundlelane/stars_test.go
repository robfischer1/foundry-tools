package bundlelane

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The manifests below are cut from foundry/flux prime/ as measured
// 2026-10-10, down to what the readers look at.
const (
	chaosManifest = `apiVersion: apps/v1
kind: Deployment
metadata: {name: chaos}
spec:
  template:
    spec:
      containers:
        - name: chaos
          env:
            - {name: CHAOS_PORT, value: "8206"}
            - {name: CHAOS_BIGINT_DATABASE_URL, value: "postgresql://chaos_svid@chaos-db-rw:5432/chaos?pool_max_conns=32&sslmode=verify-full&sslrootcert=/etc/stellar/chaos-db-ca/ca.crt"}
---
apiVersion: v1
kind: Service
metadata: {name: chaos}
spec:
  ports:
    - {name: mcp,  port: 8206, targetPort: 8206, protocol: TCP}
    - {name: mtls, port: 8207, targetPort: 8207, protocol: TCP}
`
	// ananke reaches aether-db through the alias host "aether": only the CA
	// directory names the cluster.
	anankeManifest = `kind: Deployment
metadata: {name: ananke}
spec:
  template:
    spec:
      initContainers:
        - name: migrate
          env:
            - {name: ANANKE_DATABASE_URL, value: "postgresql://ananke_svid@aether:5432/ananke?sslmode=verify-full&sslrootcert=/etc/stellar/aether-db-ca/ca.crt"}
---
kind: Service
metadata: {name: ananke}
spec:
  ports:
    - {name: mcp, port: 8200}
    - {name: mtls, port: 8201}
`
	// ourea names two clusters, one by an alias host; and a git port that is
	// not its address; and a template DSN that names no cluster.
	oureaManifest = `kind: Deployment
metadata: {name: ourea}
spec:
  template:
    spec:
      containers:
        - name: ourea
          env:
            - {name: OUREA_REALM_ROSTER, value: "{\"env\":{\"DATABASE_URL\":\"postgresql://forge@{aether}:5432/chaos\"}}"}
            - {name: OUREA_DB_DSN, value: "postgresql://ourea_svid@ourea-db:5432/ourea?sslmode=verify-full&sslrootcert=/etc/stellar/ourea-db-ca/ca.crt"}
            - {name: EREBUS_DB_DSN, value: "postgresql://ourea_svid@erebus-timescaledb:5432/erebus?sslmode=verify-full&sslrootcert=/etc/stellar/erebus-db-ca/ca.crt"}
            - {name: SECRET, valueFrom: {secretKeyRef: {name: x, key: y}}}
---
kind: Service
metadata: {name: ourea}
spec:
  ports:
    - {name: mcp, port: 8214}
    - {name: mtls, port: 8216}
    - {name: git, port: 8215}
`
	// ouranos's own Service has no ports; ouranos-opa serves its http.
	ouranosManifest = `kind: Service
metadata: {name: ouranos}
spec: {}
---
kind: Service
metadata: {name: ouranos-opa}
spec:
  ports:
    - {name: http, port: 8181}
`
	// mnemosyne's redis sorts before it by name and is not its address.
	mnemosyneManifest = `kind: Service
metadata: {name: mnemosyne-redis}
spec:
  ports:
    - {name: redis, port: 6379}
---
kind: Service
metadata: {name: mnemosyne}
spec:
  ports:
    - {name: mcp, port: 8212}
    - {name: mtls, port: 8213}
`
	// tron's only Service is its redis: no address, no database, no entry.
	tronManifest = `kind: Service
metadata: {name: tron-redis}
spec:
  ports:
    - {name: redis, port: 6379}
`
	// kairos's DSN rides inside a secret; a comment naming aether-db is not
	// a fact.
	kairosManifest = `kind: Deployment
metadata: {name: kairos}
spec:
  template:
    spec:
      containers:
        - name: kairos
          # postgres://kairos_session:<pw>@aether-db-rw.data.svc.cluster.local:5432/kairos_session
          envFrom:
            - secretRef: {name: kairos-calypso-env}
---
kind: Service
metadata: {name: kairos}
spec:
  ports:
    - {name: mcp, port: 8200}
    - {name: mtls, port: 8201}
`
	// a CronJob's DSN by a -rw host, no CA pinned.
	cronManifest = `kind: CronJob
metadata: {name: eros-sweep}
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - env:
                - {name: DSN, value: "postgres://eros@eros-db-rw.data.svc:5432/eros"}
`
	clusterManifests = `apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: aether-db}
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: chaos-db}
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: erebus-db}
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: ourea-db}
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: eros-db}
---
apiVersion: v1
kind: Secret
metadata: {name: not-a-cluster}
---
apiVersion: other.io/v1
kind: Cluster
metadata: {name: not-cnpg}
`
)

func fluxTree() map[string]string {
	return map[string]string{
		"prime/star-chaos.yaml":        chaosManifest,
		"prime/star-ananke.yaml":       anankeManifest,
		"prime/star-ourea.yaml":        oureaManifest,
		"prime/star-ouranos.yaml":      ouranosManifest,
		"prime/star-mnemosyne.yaml":    mnemosyneManifest,
		"prime/star-tron.yaml":         tronManifest,
		"prime/star-kairos.yaml":       kairosManifest,
		"prime/star-eros.yaml":         cronManifest,
		"prime/star-svid-sidecar.yaml": tronManifest,
		"prime/kustomization.yaml":     "kind: Kustomization\n",
	}
}

func knownStars() map[string]bool {
	return map[string]bool{"chaos": true, "ananke": true, "ourea": true, "ouranos": true,
		"mnemosyne": true, "tron": true, "kairos": true, "eros": true, "hades": true}
}

func clustersOrDie(t *testing.T) map[string]bool {
	t.Helper()
	c, err := Clusters(map[string]string{"data/dbs.yaml": clusterManifests})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClustersAreTheCNPGClusterObjectsOnly(t *testing.T) {
	got := sortedKeys(clustersOrDie(t))
	want := []string{"aether-db", "chaos-db", "erebus-db", "eros-db", "ourea-db"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Clusters = %q, want %q", got, want)
	}
	if _, err := Clusters(map[string]string{"data/x.yaml": "kind: [unclosed"}); err == nil || !strings.Contains(err.Error(), "data/x.yaml") {
		t.Fatalf("a manifest that does not parse must be named, got %v", err)
	}
}

func TestFluxFactsReadPortsAndDatabasesTheWayFluxStatesThem(t *testing.T) {
	got, err := FluxFacts(fluxTree(), knownStars(), clustersOrDie(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FluxFact{
		"chaos":     {Ports: &Ports{Listen: 8206, MTLS: 8207}, DB: []string{"chaos-db"}},
		"ananke":    {Ports: &Ports{Listen: 8200, MTLS: 8201}, DB: []string{"aether-db"}},
		"ourea":     {Ports: &Ports{Listen: 8214, MTLS: 8216}, DB: []string{"erebus-db", "ourea-db"}},
		"ouranos":   {Ports: &Ports{Listen: 8181}},
		"mnemosyne": {Ports: &Ports{Listen: 8212, MTLS: 8213}},
		"kairos":    {Ports: &Ports{Listen: 8200, MTLS: 8201}},
		"eros":      {DB: []string{"eros-db"}},
	}
	if !reflect.DeepEqual(got, want) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		t.Fatalf("FluxFacts =\n%s\nwant\n%s", a, b)
	}
	for _, absent := range []string{"tron", "svid-sidecar", "hades", "kustomization"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s has an entry; a star with no manifest, a manifest with no fact, or a manifest that is no star gets none", absent)
		}
	}
}

func TestFluxFactsNameTheManifestThatDoesNotParse(t *testing.T) {
	_, err := FluxFacts(map[string]string{"prime/star-chaos.yaml": "kind: [unclosed"}, knownStars(), nil)
	if err == nil || !strings.Contains(err.Error(), "prime/star-chaos.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestClusterReadsTheCAThenTheHost(t *testing.T) {
	for value, want := range map[string]string{
		"postgresql://u@aether:5432/x?sslrootcert=/etc/stellar/aether-db-ca/ca.crt": "aether-db",
		"postgres://u:pw@chaos-db-rw:5432/chaos":                                    "chaos-db",
		"postgresql://chaos-db-ro.data.svc.cluster.local/chaos":                     "chaos-db",
		"postgresql://u@narcissus-db-r:5432/n":                                      "narcissus-db",
		"postgresql://ourea-db/ourea":                                               "ourea-db",
		"hades:":                                                                    "",
		"8206":                                                                      "",
		"https://u@x-rw/":                                                           "",
	} {
		if got := cluster(value); got != want {
			t.Errorf("cluster(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestDeclaredKeysEachDocumentByItsFilename(t *testing.T) {
	got, err := Declared(map[string]string{
		"fleet/stars/hades.json": `{"name":"hades","kind":"go"}`,
		"fleet/stars/chaos.json": `{"name":"chaos"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(got), []string{"chaos", "hades"}) {
		t.Fatalf("Declared keys = %q", sortedKeys(got))
	}
	for name, files := range map[string]map[string]string{
		"a name that is not the filename": {"fleet/stars/hades.json": `{"name":"nyx"}`},
		"no name":                         {"fleet/stars/hades.json": `{"kind":"go"}`},
		"a name that is not a string":     {"fleet/stars/hades.json": `{"name":7}`},
		"not JSON":                        {"fleet/stars/hades.json": `{`},
		"not an object":                   {"fleet/stars/hades.json": `["hades"]`},
	} {
		if _, err := Declared(files); err == nil || !strings.Contains(err.Error(), "fleet/stars/hades.json") {
			t.Errorf("%s: err = %v, want one naming the file", name, err)
		}
	}
}

func TestStageIsTheSameBytesWhateverOrderTheFactsArrivedIn(t *testing.T) {
	facts, err := FluxFacts(fluxTree(), knownStars(), clustersOrDie(t))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Stage(facts)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, _ := FluxFacts(fluxTree(), knownStars(), clustersOrDie(t))
		if b, _ := Stage(again); b != first {
			t.Fatalf("Stage moved between two runs over one input:\n%s\n---\n%s", first, b)
		}
	}
	if !strings.HasSuffix(first, "}\n") || !strings.HasPrefix(first, "{\n  \"ananke\": {") {
		t.Fatalf("Stage = %q, want sorted keys, two-space indent, one trailing LF", first)
	}
	// a declared document's own layout does not reach the die
	a, _ := Stage(map[string]json.RawMessage{"hades": json.RawMessage(`{"name":"hades"}`)})
	b, _ := Stage(map[string]json.RawMessage{"hades": json.RawMessage("{\n    \"name\":   \"hades\"\n}\n")})
	if a != b {
		t.Fatalf("two layouts of one document staged differently:\n%s\n%s", a, b)
	}
	if empty, _ := Stage[FluxFact](nil); empty != "{}\n" {
		t.Fatalf("no rows stage as %q, want an empty object", empty)
	}
}

func TestCollidesNamesTheInjectedKeysTheTierAlreadySets(t *testing.T) {
	hit, err := Collides(`{"map":{},"flux":{},"declared":{}}`)
	if err != nil || !reflect.DeepEqual(hit, []string{"declared", "flux"}) {
		t.Fatalf("Collides = %q, %v", hit, err)
	}
	if hit, _ := Collides(`{"map":{},"topics":[]}`); hit != nil {
		t.Fatalf("a tier without them collides with nothing, got %q", hit)
	}
	if _, err := Collides(`{`); err == nil {
		t.Fatal("a tier that is not JSON must be an error")
	}
}

func TestInjectedWantsBothKeysWithTheStagedRows(t *testing.T) {
	built := `{"fleet":{"declared":{"hades":{}},"flux":{"hades":{},"chaos":{}}}}`
	if p, err := Injected(built, 1, 2); err != nil || p != "" {
		t.Fatalf("a roster carrying what was staged: %q %v", p, err)
	}
	for name, c := range map[string]struct {
		data           string
		declared, flux int
		want           string
	}{
		"no declared key":    {`{"fleet":{"flux":{}}}`, 0, 0, "no data.fleet.declared"},
		"no flux key":        {`{"fleet":{"declared":{}}}`, 0, 0, "no data.fleet.flux"},
		"a row went missing": {built, 1, 3, "data.fleet.flux has 2 rows, the lane staged 3"},
	} {
		if p, _ := Injected(c.data, c.declared, c.flux); !strings.Contains(p, c.want) {
			t.Errorf("%s: problem = %q, want %q", name, p, c.want)
		}
	}
	if _, err := Injected("{", 0, 0); err == nil {
		t.Fatal("a roster that is not JSON must be an error")
	}
}
