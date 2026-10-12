package bundlelane

import (
	"encoding/json"
	"os"
	"path/filepath"
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
metadata: {name: mnemosyne-sync}
spec:
  ports:
    - {name: metrics, port: 9100}
---
kind: Service
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
		"chaos":     {Ports: &Ports{Listen: 8206, MTLS: 8207}, Interface: []string{"mcp"}, DB: []string{"chaos-db"}},
		"ananke":    {Ports: &Ports{Listen: 8200, MTLS: 8201}, Interface: []string{"mcp"}, DB: []string{"aether-db"}},
		"ourea":     {Ports: &Ports{Listen: 8214, MTLS: 8216}, Interface: []string{"mcp"}, DB: []string{"erebus-db", "ourea-db"}},
		"ouranos":   {Ports: &Ports{Listen: 8181}, Interface: []string{"http"}},
		"mnemosyne": {Ports: &Ports{Listen: 8212, MTLS: 8213}, Interface: []string{"mcp"}},
		"kairos":    {Ports: &Ports{Listen: 8200, MTLS: 8201}, Interface: []string{"mcp"}},
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
		"fleet/repos/hades.json": `{"name":"hades","kind":"go"}`,
		"fleet/repos/chaos.json": `{"name":"chaos"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(got), []string{"chaos", "hades"}) {
		t.Fatalf("Declared keys = %q", sortedKeys(got))
	}
	for name, files := range map[string]map[string]string{
		"a name that is not the filename": {"fleet/repos/hades.json": `{"name":"nyx"}`},
		"no name":                         {"fleet/repos/hades.json": `{"kind":"go"}`},
		"a name that is not a string":     {"fleet/repos/hades.json": `{"name":7}`},
		"not JSON":                        {"fleet/repos/hades.json": `{`},
		"not an object":                   {"fleet/repos/hades.json": `["hades"]`},
	} {
		if _, err := Declared(files); err == nil || !strings.Contains(err.Error(), "fleet/repos/hades.json") {
			t.Errorf("%s: err = %v, want one naming the file", name, err)
		}
	}
}

func TestStageIsTheSameBytesWhateverOrderTheFactsArrivedIn(t *testing.T) {
	facts, err := FluxFacts(fluxTree(), knownStars(), clustersOrDie(t))
	if err != nil {
		t.Fatal(err)
	}
	first := Stage(facts)
	for range 20 {
		again, _ := FluxFacts(fluxTree(), knownStars(), clustersOrDie(t))
		if b := Stage(again); b != first {
			t.Fatalf("Stage moved between two runs over one input:\n%s\n---\n%s", first, b)
		}
	}
	if !strings.HasSuffix(first, "}\n") || !strings.HasPrefix(first, "{\n  \"ananke\": {") {
		t.Fatalf("Stage = %q, want sorted keys, two-space indent, one trailing LF", first)
	}
	// a declared document's own layout does not reach the die
	a, _ := Declared(map[string]string{"fleet/repos/hades.json": `{"name":"hades","kind":"go"}`})
	b, _ := Declared(map[string]string{"fleet/repos/hades.json": "{\n    \"kind\": \"go\",   \"name\":   \"hades\"\n}\n"})
	if Stage(a) != Stage(b) {
		t.Fatalf("two layouts of one document staged differently:\n%s\n%s", Stage(a), Stage(b))
	}
	if empty := Stage[FluxFact](nil); empty != "{}\n" {
		t.Fatalf("no rows stage as %q, want an empty object", empty)
	}
}

func TestCollidesNamesTheInjectedKeysTheTierAlreadySets(t *testing.T) {
	if hit := Collides(`{"map":{},"flux":{},"declared":{}}`); !reflect.DeepEqual(hit, []string{"declared", "flux"}) {
		t.Fatalf("Collides = %q", hit)
	}
	if hit := Collides(`{"map":{},"repos":{}}`); !reflect.DeepEqual(hit, []string{"repos"}) {
		t.Fatalf("the derived catalog's key is reserved too: Collides = %q", hit)
	}
	if hit := Collides(`{"map":{},"topics":[]}`); hit != nil {
		t.Fatalf("a tier without them collides with nothing, got %q", hit)
	}
	if hit := Collides(`{`); hit != nil {
		t.Fatalf("a tier that is not JSON is Orphans' refusal, not a collision: %q", hit)
	}
}

func TestInjectedWantsBothKeysWithTheStagedRows(t *testing.T) {
	built := `{"fleet":{"declared":{"hades":{}},"flux":{"hades":{},"chaos":{}},"flux_source":{"repo":"foundry/flux","sha":"abababababababababababababababababababab"}}}`
	if p := Injected(built, 1, 2, testFluxSHA); p != "" {
		t.Fatalf("a roster carrying what was staged: %q", p)
	}
	for name, c := range map[string]struct {
		data           string
		declared, flux int
		want           string
	}{
		"no declared key":    {`{"fleet":{"flux":{}}}`, 0, 0, "no data.fleet.declared"},
		"no flux key":        {`{"fleet":{"declared":{}}}`, 0, 0, "no data.fleet.flux"},
		"a row went missing": {built, 1, 3, "data.fleet.flux has 2 rows, the lane staged 3"},
		"not JSON":           {"{", 0, 0, "not JSON"},
		"no flux source":     {`{"fleet":{"declared":{},"flux":{}}}`, 0, 0, "does not name flux " + testFluxSHA},
		"another flux":       {strings.Replace(built, "abab", "cdcd", 1), 1, 2, "does not name flux " + testFluxSHA},
	} {
		if p := Injected(c.data, c.declared, c.flux, testFluxSHA); !strings.Contains(p, c.want) {
			t.Errorf("%s: problem = %q, want %q", name, p, c.want)
		}
	}
}

const testFluxSHA = "abababababababababababababababababababab"

func injectInputs() Inputs {
	return Inputs{
		FluxSHA: testFluxSHA,
		Tier:    `{"map":{}}`,
		Entries: map[string]string{
			"fleet/repos/chaos.json": `{"name":"chaos","kind":"go"}`,
			"fleet/repos/tron.json":  `{"name":"tron","kind":"go"}`,
			"fleet/repos/ourea.json": `{"name":"ourea","kind":"go"}`,
		},
		StarManifests:   fluxTree(),
		ClusterManifest: map[string]string{"data/dbs.yaml": clusterManifests},
	}
}

func TestInjectReadsFluxForTheStarsTheTreeKnows(t *testing.T) {
	inj, err := Inject(injectInputs())
	if err != nil {
		t.Fatal(err)
	}
	// chaos, tron and ourea by their catalog entries; tron states nothing.
	if inj.DeclaredRows != 3 || inj.FluxRows != 2 || inj.Known != 3 || inj.Clusters != 5 {
		t.Fatalf("Inject = %+v", inj)
	}
	var flux map[string]FluxFact
	if err := json.Unmarshal([]byte(inj.Flux), &flux); err != nil || flux["ourea"].Ports.Listen != 8214 || flux["chaos"].DB[0] != "chaos-db" {
		t.Fatalf("flux = %s (%v)", inj.Flux, err)
	}
	if !strings.Contains(inj.Declared, `"ourea": {`) {
		t.Fatalf("declared = %s", inj.Declared)
	}
	if want := "{\n  \"repo\": \"foundry/flux\",\n  \"sha\": \"" + testFluxSHA + "\"\n}\n"; inj.Source != want {
		t.Fatalf("source = %q, want %q", inj.Source, want)
	}
}

func TestInjectRefusesWhatWouldServeTheWrongFacts(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*Inputs)
		want string
	}{
		"the tier sets an injected key":          {func(in *Inputs) { in.Tier = `{"flux":{}}` }, "fleet/data.json already sets data.fleet.flux"},
		"the tier sets the flux source":          {func(in *Inputs) { in.Tier = `{"flux_source":{}}` }, "already sets data.fleet.flux_source"},
		"a flux commit that is not a sha":        {func(in *Inputs) { in.FluxSHA = "main" }, `the flux commit "main" is not a full sha`},
		"an entry named for another":             {func(in *Inputs) { in.Entries["fleet/repos/ourea.json"] = `{"name":"nyx"}` }, `fleet/repos/ourea.json: name must be "ourea"`},
		"no star manifest":                       {func(in *Inputs) { in.StarManifests = nil }, "foundry/flux answered no prime/star-*.yaml"},
		"a cluster manifest that does not parse": {func(in *Inputs) { in.ClusterManifest = map[string]string{"data/x.yaml": "kind: [unclosed"} }, "foundry/flux data/x.yaml"},
		"a star manifest that does not parse":    {func(in *Inputs) { in.StarManifests["prime/star-chaos.yaml"] = "kind: [unclosed" }, "foundry/flux prime/star-chaos.yaml"},
	} {
		in := injectInputs()
		c.edit(&in)
		if _, err := Inject(in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// aletheia as flux will carry it once its Service port is named for what it
// serves: plain http on the listen port, the mTLS door beside it.
const httpStarManifest = `kind: Service
metadata: {name: aletheia}
spec:
  ports:
    - {name: http, port: 8220}
    - {name: mtls, port: 8221}
`

func TestFluxFactsNameTheInterfaceThePortNamesImply(t *testing.T) {
	got, err := FluxFacts(map[string]string{
		"prime/star-aletheia.yaml": httpStarManifest,
		"prime/star-chaos.yaml":    chaosManifest,
		"prime/star-tron.yaml":     tronManifest,
	}, map[string]bool{"aletheia": true, "chaos": true, "tron": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FluxFact{
		"aletheia": {Ports: &Ports{Listen: 8220, MTLS: 8221}, Interface: []string{"http"}},
		"chaos":    {Ports: &Ports{Listen: 8206, MTLS: 8207}, Interface: []string{"mcp"}},
	}
	if !reflect.DeepEqual(got, want) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		t.Fatalf("FluxFacts =\n%s\nwant\n%s", a, b)
	}
}

// A star naming both an mcp and an http port lists both, sorted, once each; a
// port named for anything else (redis, git, metrics) implies nothing.
func TestInterfaceIsTheSortedSetOverTheStarsServices(t *testing.T) {
	doc := `kind: Service
metadata: {name: x}
spec:
  ports:
    - {name: mcp, port: 1}
    - {name: git, port: 2}
---
kind: Service
metadata: {name: x-web}
spec:
  ports:
    - {name: http, port: 3}
    - {name: mcp, port: 4}
---
kind: Service
metadata: {name: x-redis}
spec:
  ports:
    - {name: redis, port: 6379}
`
	got, err := FluxFacts(map[string]string{"prime/star-x.yaml": doc}, map[string]bool{"x": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"http", "mcp"}; !reflect.DeepEqual(got["x"].Interface, want) {
		t.Fatalf("Interface = %q, want %q", got["x"].Interface, want)
	}
	if got["x"].Ports.Listen != 1 {
		t.Fatalf("Listen = %d: the interface must not move the listen port", got["x"].Ports.Listen)
	}
}

// BACKWARD COMPATIBLE: the served document is today's plus the interface key.
// Drop that key and every row is byte-for-byte the row the lane served before.
func TestStageIsTodaysBytesPlusTheInterfaceKey(t *testing.T) {
	facts, err := FluxFacts(fluxTree(), knownStars(), clustersOrDie(t))
	if err != nil {
		t.Fatal(err)
	}
	var rows map[string]map[string]any
	if err := json.Unmarshal([]byte(Stage(facts)), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		delete(r, "interface")
	}
	old := map[string]map[string]any{}
	for star, f := range facts {
		row := map[string]any{}
		if f.Ports != nil {
			p := map[string]any{"listen": f.Ports.Listen}
			if f.Ports.MTLS != 0 {
				p["mtls"] = f.Ports.MTLS
			}
			if f.Ports.Listen == 0 {
				delete(p, "listen")
			}
			row["ports"] = p
		}
		if len(f.DB) > 0 {
			row["db"] = f.DB
		}
		old[star] = row
	}
	a, _ := json.Marshal(rows)
	b, _ := json.Marshal(old)
	if string(a) != string(b) {
		t.Fatalf("minus interface, Stage =\n%s\nwant\n%s", a, b)
	}
}

// F16: a repo with a Service in prime is a deployed star, whether the Service
// is a ClusterIP in a star manifest or an ExternalName alias to another namespace.
func TestServiceFactsGiveEveryPrimeServiceARow(t *testing.T) {
	// blades names its namespace in the Kustomization, not the Service (below, on
	// the real tree); explicit namespaces are what these hand-cut cases state.
	manifests := map[string]string{
		"prime/blade-runner-alias.yaml": "kind: Service\nmetadata: {name: blade-runner, namespace: prime}\nspec: {type: ExternalName, externalName: blade-runner.blades.svc.cluster.local}\n",
		"blades/blade-controller.yaml":  "kind: Service\nmetadata: {name: blade-runner, namespace: blades}\nspec:\n  ports:\n    - {name: mcp, port: 8217}\n    - {name: mtls, port: 8218}\n",
		"hemera/hemera-collect.yaml": `kind: Service
metadata: {name: hemera-otelcol, namespace: hemera-node}
spec:
  ports:
    - {name: otlp-http, port: 4318}
---
kind: Service
metadata: {name: hemera-otelcol, namespace: prime}
spec: {type: ExternalName, externalName: hemera-otelcol.hemera-node.svc.cluster.local}
---
kind: Service
metadata: {name: hemera, namespace: prime}
spec: {type: ExternalName, externalName: hemera-otelcol.hemera-node.svc.cluster.local}
`,
		"prime/other.yaml": "kind: Service\nmetadata: {name: plain, namespace: prime}\nspec:\n  ports:\n    - {name: http, port: 3000}\n---\nkind: Service\nmetadata: {name: lost, namespace: prime}\nspec: {type: ExternalName, externalName: lost.nowhere.svc.cluster.local}\n---\nkind: Service\nmetadata: {name: elsewhere, namespace: blades}\nspec:\n  ports:\n    - {name: mcp, port: 1}\n",
	}
	repos := map[string]bool{"blade-runner": true, "hemera": true, "plain": true, "lost": true, "elsewhere": true, "tron": true, "chaos": true}
	have := map[string]FluxFact{"chaos": {Ports: &Ports{Listen: 1}}}
	got, err := ServiceFacts(manifests, repos, have)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FluxFact{
		"blade-runner": {Ports: &Ports{Listen: 8217, MTLS: 8218}, Interface: []string{"mcp"}},
		"hemera":       {Ports: &Ports{Listen: 4318}, Interface: []string{"otlp"}},
		"plain":        {Ports: &Ports{Listen: 3000}, Interface: []string{"http"}},
		"lost":         {},
	}
	if a, b := Stage(got), Stage(want); a != b {
		t.Fatalf("ServiceFacts =\n%s\nwant\n%s", a, b)
	}
}

func TestServiceFactsNameTheManifestThatDoesNotParse(t *testing.T) {
	_, err := ServiceFacts(map[string]string{"hemera/x.yaml": "kind: [unclosed"}, map[string]bool{"x": true}, nil)
	if err == nil || !strings.Contains(err.Error(), "hemera/x.yaml") {
		t.Fatalf("err = %v, want one naming hemera/x.yaml", err)
	}
}

// Rows FluxFacts already states do not move when the Service manifests join.
func TestInjectKeepsTodaysFluxRowsByteIdentical(t *testing.T) {
	in := snapshot(t)
	before, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	in.ServiceManifests = map[string]string{
		"prime/blade-runner-alias.yaml": "kind: Service\nmetadata: {name: blade-runner, namespace: prime}\nspec: {type: ExternalName, externalName: blade-runner.blades.svc.cluster.local}\n",
		"blades/c.yaml":                 "kind: Service\nmetadata: {name: blade-runner, namespace: blades}\nspec:\n  ports:\n    - {name: mcp, port: 8217}\n    - {name: mtls, port: 8218}\n",
	}
	in.Entries["fleet/repos/blade-runner.json"] = `{"name":"blade-runner"}`
	after, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	var b, a map[string]json.RawMessage
	_ = json.Unmarshal([]byte(before.Flux), &b)
	_ = json.Unmarshal([]byte(after.Flux), &a)
	if len(a) != len(b)+1 {
		t.Fatalf("rows %d -> %d, want exactly one new", len(b), len(a))
	}
	for k, v := range b {
		if string(a[k]) != string(v) {
			t.Errorf("%s moved: %s -> %s", k, v, a[k])
		}
	}
	if _, ok := a["blade-runner"]; !ok {
		t.Error("blade-runner has no flux row")
	}
}

// An ExternalName that is not an in-cluster Service (a hostname outside the
// cluster) has no target flux could carry: the repo is still a star.
func TestServiceFactsAnExternalHostIsAnEmptyFact(t *testing.T) {
	got, err := ServiceFacts(map[string]string{
		"prime/x.yaml": "kind: Service\nmetadata: {name: out, namespace: prime}\nspec: {type: ExternalName, externalName: db.example.com}\n",
	}, map[string]bool{"out": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := got["out"]; !ok || f.Ports != nil || f.Interface != nil {
		t.Fatalf("got %+v, want an empty fact for out", got)
	}
}

// A Service manifest that does not parse fails the injection, naming the file.
func TestInjectNamesTheServiceManifestThatDoesNotParse(t *testing.T) {
	in := snapshot(t)
	in.ServiceManifests = map[string]string{"hemera/bad.yaml": "kind: [unclosed"}
	if _, err := Inject(in); err == nil || !strings.Contains(err.Error(), "foundry/flux hemera/bad.yaml") {
		t.Fatalf("err = %v", err)
	}
}

// THE REAL BLADES, HEMERA AND PRIME ALIAS MANIFESTS: testdata/flux/{blades,
// hemera,prime/blade-runner-alias.yaml,clusters/pantheon} are foundry/flux's
// files at testdata/flux/SERVICES_SOURCE, minus full-line comments, kube-linter
// ignore annotations and owner emails. blades/blade-controller.yaml's
// blade-runner Service names NO namespace (clusters/pantheon/blades.yaml sets
// targetNamespace), which the hand-cut cases above never saw.
func servicesSnapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, g := range []string{"blades/*.yaml", "hemera/*.yaml", "prime/blade-runner-alias.yaml", "clusters/pantheon/*.yaml"} {
		paths, err := filepath.Glob(filepath.Join("testdata", "flux", g))
		if err != nil || len(paths) == 0 {
			t.Fatalf("testdata/flux/%s: %v (%d files)", g, err, len(paths))
		}
		for _, p := range paths {
			body, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			out[strings.TrimPrefix(filepath.ToSlash(p), "testdata/flux/")] = string(body)
		}
	}
	return out
}

func TestServiceFactsReadTheRealBladesAndHemeraManifests(t *testing.T) {
	got, err := ServiceFacts(servicesSnapshot(t), map[string]bool{"blade-runner": true, "hemera": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FluxFact{
		"blade-runner": {Ports: &Ports{Listen: 8217, MTLS: 8218}, Interface: []string{"mcp"}},
		"hemera":       {Ports: &Ports{Listen: 4318}, Interface: []string{"otlp"}},
	}
	if a, b := Stage(got), Stage(want); a != b {
		t.Fatalf("ServiceFacts over the real tree =\n%s\nwant\n%s", a, b)
	}
}

// Over the real flux tree the injection adds exactly blade-runner and hemera,
// and moves no row it already had.
func TestInjectOverTheRealTreeAddsBladeRunnerAndHemera(t *testing.T) {
	in := snapshot(t)
	before, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	in.ServiceManifests = servicesSnapshot(t)
	in.Entries["fleet/repos/blade-runner.json"] = `{"name":"blade-runner"}`
	in.Entries["fleet/repos/hemera.json"] = `{"name":"hemera"}`
	after, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	var b, a map[string]json.RawMessage
	_ = json.Unmarshal([]byte(before.Flux), &b)
	_ = json.Unmarshal([]byte(after.Flux), &a)
	if len(a) != len(b)+2 {
		t.Fatalf("rows %d -> %d, want exactly two new", len(b), len(a))
	}
	for k, v := range b {
		if string(a[k]) != string(v) {
			t.Errorf("%s moved: %s -> %s", k, v, a[k])
		}
	}
	if got := strings.Join(strings.Fields(string(a["blade-runner"])), ""); got != `{"ports":{"listen":8217,"mtls":8218},"interface":["mcp"]}` {
		t.Errorf("blade-runner = %s", got)
	}
	if got := strings.Join(strings.Fields(string(a["hemera"])), ""); got != `{"ports":{"listen":4318},"interface":["otlp"]}` {
		t.Errorf("hemera = %s", got)
	}
}

// A Service that names no namespace, in a directory no Kustomization places,
// sits in the namespace named for the directory; a Kustomization whose spec
// does not read places nothing.
func TestServiceFactsFallBackToTheDirectoryName(t *testing.T) {
	got, err := ServiceFacts(map[string]string{
		"prime/bare.yaml":          "kind: Service\nmetadata: {name: bare}\nspec:\n  ports:\n    - {name: http, port: 7}\n",
		"clusters/pantheon/x.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: x}\nspec: oops\n",
	}, map[string]bool{"bare": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := got["bare"]; f.Ports == nil || f.Ports.Listen != 7 {
		t.Fatalf("got %+v, want bare on port 7 in prime", got)
	}
}

// WORKERS (b36-workload-stars): a repo whose Deployment or StatefulSet flux
// deploys in prime is a star with or without a Service. Its entry states no
// ports and no interface; db when its env names a CNPG cluster.
func TestWorkloadFactsGiveAServicelessWorkerARow(t *testing.T) {
	manifests := map[string]string{
		"prime/a.yaml": `kind: Deployment
metadata: {name: by-label, namespace: prime, labels: {pistis.star: lab}}
spec: {template: {spec: {containers: [{name: c}]}}}
---
kind: Deployment
metadata: {name: lab-redis, namespace: prime}
spec: {template: {metadata: {labels: {pistis.star: podlab}}, spec: {containers: [{name: c}]}}}
---
kind: StatefulSet
metadata: {name: ss, namespace: prime}
spec: {template: {spec: {containers: [{name: c}]}}}
---
kind: Deployment
metadata: {name: dbworker, namespace: prime}
spec: {template: {spec: {containers: [{name: c, env: [{name: D, value: "postgresql://u@chaos-db-rw:5432/x"}, {name: E, value: "postgresql://u@aether:5432/x"}]}]}}}
---
kind: Deployment
metadata: {name: tron-redis, namespace: prime}
spec: {template: {spec: {containers: [{name: c}]}}}
---
kind: Deployment
metadata: {name: elsewhere, namespace: blades}
spec: {template: {spec: {containers: [{name: c}]}}}
---
kind: Deployment
metadata: {name: haveit, namespace: prime}
spec: {template: {spec: {containers: [{name: c}]}}}
---
kind: CronJob
metadata: {name: cron, namespace: prime}
spec: {jobTemplate: {spec: {template: {spec: {containers: [{name: c}]}}}}}
`,
	}
	repos := map[string]bool{"lab": true, "podlab": true, "ss": true, "dbworker": true, "tron": true, "elsewhere": true, "haveit": true, "cron": true}
	have := map[string]FluxFact{"haveit": {Ports: &Ports{Listen: 1}}}
	got, err := WorkloadFacts(manifests, repos, have, map[string]bool{"chaos-db": true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FluxFact{"lab": {}, "podlab": {}, "ss": {}, "dbworker": {DB: []string{"chaos-db"}}}
	if a, b := Stage(got), Stage(want); a != b {
		t.Fatalf("WorkloadFacts =\n%s\nwant\n%s", a, b)
	}
}

func TestWorkloadFactsNameTheManifestThatDoesNotParse(t *testing.T) {
	_, err := WorkloadFacts(map[string]string{"prime/x.yaml": "kind: [unclosed"}, map[string]bool{"x": true}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "prime/x.yaml") {
		t.Fatalf("err = %v, want one naming prime/x.yaml", err)
	}
}

// THE REAL star-tron.yaml AND naiad.yaml: tron (a Deployment labelled
// pistis.star=tron, tron-redis beside it, no Service for tron) and naiad (a
// Deployment, no Service) are stars with an empty flux entry, and no other row
// moves. naiad is matched by its pistis.star label; the second case matches it
// by its Deployment name alone.
func TestInjectOverTheRealWorkersAddsTronAndNaiad(t *testing.T) {
	for name, strip := range map[string]bool{"label": false, "name only": true} {
		t.Run(name, func(t *testing.T) {
			in := snapshot(t)
			before, err := Inject(in)
			if err != nil {
				t.Fatal(err)
			}
			naiad, err := os.ReadFile(filepath.Join("testdata", "flux", "prime", "naiad.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			body := string(naiad)
			if strip {
				body = strings.ReplaceAll(body, ", pistis.star: naiad", "")
			}
			tron, err := os.ReadFile(filepath.Join("testdata", "flux", "prime", "star-tron.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			in.ServiceManifests = map[string]string{"prime/naiad.yaml": body, "prime/star-tron.yaml": string(tron)}
			in.Entries["fleet/repos/naiad.json"] = `{"name":"naiad"}`
			after, err := Inject(in)
			if err != nil {
				t.Fatal(err)
			}
			var b, a map[string]json.RawMessage
			_ = json.Unmarshal([]byte(before.Flux), &b)
			_ = json.Unmarshal([]byte(after.Flux), &a)
			if len(a) != len(b)+2 {
				t.Fatalf("rows %d -> %d, want exactly tron and naiad new", len(b), len(a))
			}
			for k, v := range b {
				if string(a[k]) != string(v) {
					t.Errorf("%s moved: %s -> %s", k, v, a[k])
				}
			}
			for _, k := range []string{"tron", "naiad"} {
				if got := strings.Join(strings.Fields(string(a[k])), ""); got != `{}` {
					t.Errorf("%s = %s, want an empty fact", k, got)
				}
			}
		})
	}
}

// A Kustomization's targetNamespace places the directory it builds; only a
// Flux Kustomization with one counts. An object with no namespace sits where
// its directory is placed, else in the directory's own name.
func TestParseAllAndNamespaceOfPlaceTheDirectory(t *testing.T) {
	_, ns, err := parseAll(map[string]string{
		"clusters/a.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: a}\nspec: {path: ./apps/, targetNamespace: prime}\n---\n" +
			"apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: b}\nspec: {path: ./bare}\n---\n" +
			"apiVersion: other.io/v1\nkind: Kustomization\nmetadata: {name: c}\nspec: {path: ./other, targetNamespace: elsewhere}\n---\n" +
			"apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Other\nmetadata: {name: d}\nspec: {path: ./kind, targetNamespace: elsewhere}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns["apps"] != "prime" {
		t.Fatalf("targetNS = %v, want only apps -> prime", ns)
	}
	for _, c := range []struct {
		d          manifest
		file, want string
	}{
		{manifest{Namespace: "x"}, "apps/f.yaml", "x"},
		{manifest{}, "apps/f.yaml", "prime"},
		{manifest{}, "bare/f.yaml", "bare"},
	} {
		if got := namespaceOf(c.d, c.file, ns); got != c.want {
			t.Errorf("namespaceOf(%+v, %s) = %q, want %q", c.d, c.file, got, c.want)
		}
	}
}

// A workload placed in prime by its Kustomization is in prime; one workload
// per repo may name a database, and a repo's two workloads state the union.
func TestWorkloadFactsPlaceByKustomizationAndMergeDatabases(t *testing.T) {
	dep := func(name, label, dsn string) string {
		return "kind: Deployment\nmetadata: {name: " + name + ", labels: {pistis.star: " + label + "}}\nspec: {template: {spec: {containers: [{name: c, env: [{name: D, value: \"" + dsn + "\"}]}]}}}\n"
	}
	manifests := map[string]string{
		"clusters/k.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: k}\nspec: {path: ./apps, targetNamespace: prime}\n",
		"apps/a.yaml":     dep("w1", "w", "postgresql://u@erebus-db-rw:5432/x") + "---\n" + dep("w", "", "postgresql://u@chaos-db-rw:5432/x"),
		"other/b.yaml":    dep("o", "o", ""),
	}
	got, err := WorkloadFacts(manifests, map[string]bool{"w": true, "o": true}, nil, map[string]bool{"chaos-db": true, "erebus-db": true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FluxFact{"w": {DB: []string{"chaos-db", "erebus-db"}}}
	if a, b := Stage(got), Stage(want); a != b {
		t.Fatalf("WorkloadFacts =\n%s\nwant\n%s", a, b)
	}
	if mergeSorted(nil, nil) != nil || len(mergeSorted([]string{"a"}, []string{"a"})) != 1 {
		t.Fatal("mergeSorted: nil for nothing, a set for something")
	}
}

// A pistis.star label that names no catalog repo is refused, not read as the
// Deployment's own name: here `tron` is a repo and the label says `tronn`.
func TestWorkloadFactsRefuseALabelThatNamesNoRepo(t *testing.T) {
	manifests := map[string]string{"prime/x.yaml": "kind: Deployment\nmetadata: {name: tron, namespace: prime, labels: {pistis.star: tronn}}\nspec: {template: {spec: {containers: [{name: c}]}}}\n"}
	_, err := WorkloadFacts(manifests, map[string]bool{"tron": true}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "prime/x.yaml") || !strings.Contains(err.Error(), "tronn") {
		t.Fatalf("err = %v, want one naming prime/x.yaml and tronn", err)
	}
	in := snapshot(t)
	in.ServiceManifests = manifests
	in.Entries["fleet/repos/tron.json"] = `{"name":"tron"}`
	if _, err := Inject(in); err == nil || !strings.Contains(err.Error(), "foundry/flux prime/x.yaml") {
		t.Fatalf("Inject err = %v, want the lane to refuse", err)
	}
	// Outside prime a label is not this rule's business.
	manifests["prime/x.yaml"] = strings.Replace(manifests["prime/x.yaml"], "namespace: prime", "namespace: blades", 1)
	if _, err := WorkloadFacts(manifests, map[string]bool{"tron": true}, nil, nil); err != nil {
		t.Fatalf("a label outside prime was refused: %v", err)
	}
}
