package bundlelane

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"reflect"
	"strings"
	"testing"
)

func TestChangedDropsBlankLines(t *testing.T) {
	got := Changed("fleet/data.json\n\n  policy/authz/data.json \n")
	want := []string{"fleet/data.json", "policy/authz/data.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Changed = %q, want %q", got, want)
	}
	if Changed("") != nil {
		t.Error("no output is no paths")
	}
}

// build.yml's paths-ignore and fleet-bundle.yml's paths, both directions.
func TestPublishesFollowsTheRetiredWorkflowsPathFilters(t *testing.T) {
	cases := []struct {
		name          string
		changed       []string
		policy, fleet bool
	}{
		{"cannot tell: both publish", nil, true, true},
		{"a policy change", []string{"policy/authz/visible.rego"}, true, false},
		{"docs and schema alone move nothing", []string{"README.md", "policy/authz/NOTES.md", ".forgejo/workflows/ci.yml", "schema/slag-v3.schema.json"}, false, false},
		{"a roster change", []string{"fleet/stars/athena/slag.json"}, true, true},
		{"the manifest moves both", []string{"policy/.manifest"}, true, true},
		{"the stubs move both", []string{"policy/admission/stubs.rego"}, true, true},
		{"the recipe moves the roster", []string{"ci/bundle.sh"}, true, true},
		{"a nested fleet/ is not the roster", []string{"docs/fleet/x.json"}, true, false},
	}
	for _, c := range cases {
		policy, fleet := Publishes(c.changed)
		if policy != c.policy || fleet != c.fleet {
			t.Errorf("%s: Publishes(%q) = %v, %v; want %v, %v", c.name, c.changed, policy, fleet, c.policy, c.fleet)
		}
	}
}

func TestPinIsGAndTheFirstSeven(t *testing.T) {
	if p, err := Pin("ceb0c1cc3adea1eac88f1b87385fb20a18b1e016"); err != nil || p != "gceb0c1c" {
		t.Fatalf("Pin = %q, %v; want gceb0c1c (the pin ceb0c1c's run pushed)", p, err)
	}
	if _, err := Pin("ceb0c1"); err == nil {
		t.Error("a six-character commit makes no pin")
	}
	if p, err := Pin("ceb0c1c"); err != nil || p != "gceb0c1c" {
		t.Errorf("Pin of exactly seven = %q, %v; seven is enough", p, err)
	}
}

func TestOrphansAreShardsTheMapLacks(t *testing.T) {
	shards := []string{"fleet/stars/zeus/slag.json", "fleet/stars/athena/slag.json", "fleet/stars/argus/slag.json"}
	orphans, mapped, err := Orphans(shards, `{"map":{"athena":{},"ares":{}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orphans, []string{"argus", "zeus"}) || mapped != 2 {
		t.Fatalf("Orphans = %q, %d; want [argus zeus], 2", orphans, mapped)
	}
	if o, _, _ := Orphans([]string{"fleet/stars/athena/slag.json"}, `{"map":{"athena":{}}}`); o != nil {
		t.Errorf("a mapped shard is no orphan: %q", o)
	}
	if _, _, err := Orphans(shards, "{not json"); err == nil {
		t.Error("an unreadable data.json must be an error, not an empty map")
	}
}

func TestSignedReadsTheListingWhole(t *testing.T) {
	// The listing the ceb0c1c run's opa build produced carries the member at
	// the root, with or without the leading slash tar keeps.
	for _, listing := range []string{
		"/data.json\n/policy/authz/visible.rego\n/.signatures.json\n/.manifest\n",
		"data.json\n.signatures.json\n",
	} {
		if !Signed(listing) {
			t.Errorf("Signed(%q) = false", listing)
		}
	}
	for _, listing := range []string{"", "/data.json\n/.manifest\n", "/policy/.signatures.json\n", "/.signatures.json.bak\n"} {
		if Signed(listing) {
			t.Errorf("Signed(%q) = true; only a root .signatures.json is a signature", listing)
		}
	}
}

func TestRootsAreFleetExactly(t *testing.T) {
	if !RootsAreFleet(FleetManifest) {
		t.Fatal("the manifest the stage writes must pass its own check")
	}
	for _, m := range []string{`{"roots":["fleet","authz"]}`, `{"roots":[]}`, `{"roots":["admission"]}`, `{}`, "not json"} {
		if RootsAreFleet(m) {
			t.Errorf("RootsAreFleet(%q) = true", m)
		}
	}
}

func TestRosterRefusesAnEmptyFleetOrALeakedCharter(t *testing.T) {
	sound := `{"fleet":{"map":{"athena":{},"ares":{}},"topics":["a._ops.calls"],"stars":{"athena":{"name":"athena"},"ares":{"name":"ares"},"chaos":{"name":"chaos"}}}}`
	mapped, stars, topics, problem, err := Roster(sound)
	if err != nil || problem != "" || mapped != 2 || stars != 3 || topics != 1 {
		t.Fatalf("Roster(sound) = %d, %d, %d, %q, %v", mapped, stars, topics, problem, err)
	}
	for name, doc := range map[string]string{
		"no map":    `{"fleet":{"map":{},"topics":["t"],"stars":{"a":{}}}}`,
		"no topics": `{"fleet":{"map":{"a":{}},"topics":[],"stars":{"a":{}}}}`,
		"no stars":  `{"fleet":{"map":{"a":{}},"topics":["t"],"stars":[]}}`,
		"no fleet":  `{}`,
	} {
		if _, _, _, problem, _ := Roster(doc); !strings.Contains(problem, "empty data.fleet") {
			t.Errorf("%s: problem = %q", name, problem)
		}
	}
	leaked := `{"fleet":{"map":{"a":{}},"topics":{"t":{}},"stars":[{"name":"a"},{"name":"b","charter":"the full declaration"}]}}`
	if _, _, _, problem, _ := Roster(leaked); !strings.Contains(problem, "leaked") {
		t.Errorf("a charter in the bundle: problem = %q", problem)
	}
	if _, _, _, _, err := Roster("{"); err == nil {
		t.Error("an unreadable data.json must be an error")
	}
}

func TestRosterDenialsCountOnlyTheRostersOwn(t *testing.T) {
	denies := []string{
		"seam to definitely-not-a-real-star is not in the fleet roster",
		"topic not-a-registered-topic is not in the fleet roster",
		"gate-probe has no interface",
	}
	if n := RosterDenials(denies); n != 2 {
		t.Fatalf("RosterDenials = %d, want 2", n)
	}
	if RosterDenials(nil) != 0 {
		t.Error("no denials count none")
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(PlantedSeams), &probe); err != nil {
		t.Fatalf("the planted probe must be JSON opa can read: %v", err)
	}
}

func TestFileDigestReadsSha256sum(t *testing.T) {
	hex := strings.Repeat("ab", 32)
	if d, err := FileDigest(hex + "  /work/bundle.tar.gz\n"); err != nil || d != "sha256:"+hex {
		t.Fatalf("FileDigest = %q, %v", d, err)
	}
	for _, out := range []string{"", "sha256sum: /work/bundle.tar.gz: No such file or directory", strings.Repeat("ab", 31) + "  f"} {
		if _, err := FileDigest(out); err == nil {
			t.Errorf("FileDigest(%q) answered a digest", out)
		}
	}
}

func TestPublishedLayerReadsTheFirstLayer(t *testing.T) {
	m := `{"schemaVersion":2,"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:c7e2"},{"digest":"sha256:ffff"}]}`
	if got := PublishedLayer(m); got != "sha256:c7e2" {
		t.Fatalf("PublishedLayer = %q", got)
	}
	for _, m := range []string{"", `{"layers":[]}`, "Error: not found"} {
		if got := PublishedLayer(m); got != "" {
			t.Errorf("PublishedLayer(%q) = %q, want none", m, got)
		}
	}
}

func TestDockerConfigLogsTheUserInToTheHost(t *testing.T) {
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal([]byte(DockerConfig(RegistryHost, RegistryUser, "tok")), &cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(cfg.Auths["foundry.notusmi.com"].Auth)
	if err != nil || string(raw) != "rob:tok" {
		t.Fatalf("auth for foundry.notusmi.com decodes to %q, %v", raw, err)
	}
}

func TestDecodeKeyWantsBase64PEMAndNeverEchoesIt(t *testing.T) {
	key, _ := EphemeralKey()
	got, err := DecodeKey("OPA_BUNDLE_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte(key)))
	if err != nil || got != key {
		t.Fatalf("DecodeKey round trip: %v", err)
	}
	for _, in := range []string{"not base64!", base64.StdEncoding.EncodeToString([]byte("plainly not a key"))} {
		_, err := DecodeKey("COSIGN_PRIVATE_KEY", in)
		if err == nil {
			t.Fatalf("DecodeKey(%q) accepted it", in)
		}
		if !strings.Contains(err.Error(), "COSIGN_PRIVATE_KEY") || strings.Contains(err.Error(), in) {
			t.Errorf("the error must name the key and never carry it: %v", err)
		}
	}
}

func TestEphemeralKeyIsAP256PEMOpaCanSignWith(t *testing.T) {
	k, err := EphemeralKey()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(k))
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("not an EC PRIVATE KEY PEM: %q", k)
	}
	parsed, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil || parsed.Curve.Params().Name != "P-256" {
		t.Fatalf("not a P-256 key: %v", err)
	}
	if other, _ := EphemeralKey(); other == k {
		t.Error("two ephemeral keys must differ")
	}
}
