package orbitlane

import (
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// report is a narcissus JSON report holding the findings given as
// verdict|subject|cause|detail rows.
func report(rows ...string) []byte {
	var parts []string
	for _, r := range rows {
		f := strings.SplitN(r, "|", 4)
		parts = append(parts, `{"verdict":"`+f[0]+`","subject":"`+f[1]+`","cause":"`+f[2]+`","detail":"`+f[3]+`"}`)
	}
	return []byte(`{"version":1,"findings":[` + strings.Join(parts, ",") + `]}`)
}

func surfaceOf(t *testing.T, star string, surface, orbits []byte) []checks.Finding {
	t.Helper()
	found, err := Surface(SurfaceInput{
		Star: star, Contracts: contractsFor(t),
		Prefixes: map[string]string{"chaos": "graph", "urania": "urania", "themis": "themis"},
		Surface:  surface, Orbits: orbits,
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// themis serves close_node to athena and dials urania's and chaos's verbs.
func TestSurfaceHoldsWhenCodeAndContractsAgree(t *testing.T) {
	found := surfaceOf(t, "themis",
		report("inert|close_node|one-exposer|registered via literal", "inert|admit|one-exposer|registered via literal"),
		report("holds|cgo|mechanism|searched",
			"unanalyzable|/src/a.go:1|loose-verb|neighbors — the receiver traces to no dial",
			"unanalyzable|/src/a.go:2|loose-verb|neighbors — again, deduplicated",
			"unanalyzable|/src/b.go:3|loose-verb|graph_quads_to — via the gateway",
			"unanalyzable|/src/c.go:4|loose-verb|admit — its own verb",
			"unanalyzable|/src/main.go:9|unresolved-peer|mcpclient.Dial(url, peer, ident)"))
	got := verdicts(found)
	want := []string{"holds served orbits/themis-athena.toml", "holds dials-contracted themis"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if found[0].Detail != "themis registers all 1 verb(s) athena may call" || found[1].Detail != "2 dialled verb(s) are on themis's contracts, 1 are its own" {
		t.Errorf("details %q / %q", found[0].Detail, found[1].Detail)
	}
}

func TestSurfaceFindsARemovedVerbAndAnUncontractedDial(t *testing.T) {
	found := surfaceOf(t, "themis",
		report("inert|admit|one-exposer|registered via literal"),
		report("unanalyzable|/src/e.go:8|loose-verb|close_node — athena's? no: themis is the producer"))
	// close_node is themis's own contracted verb it no longer registers, and
	// dialling it is a call to a verb only themis's contracts name (with
	// athena as consumer), so it attributes to themis and is uncontracted.
	got := verdicts(found)
	want := []string{
		"violated contracted-verb-unregistered themis close_node",
		"violated dial-uncontracted /src/e.go:8",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if found[0].Detail != "orbits/themis-athena.toml says athena may call close_node, and themis registers no close_node — if this pull renamed or removed it, edit orbits/themis-athena.toml in a foundry-dies pull to match" {
		t.Errorf("producer detail %q", found[0].Detail)
	}
	if found[1].Detail != `themis dials close_node, which themis serves, and orbits/themis-themis.toml does not name it — add "close_node" to its verbs in a foundry-dies pull (create the file if the pair has none)` {
		t.Errorf("consumer detail %q", found[1].Detail)
	}
}

func TestADialAttributesThroughTheProducersOwnPrefixOnly(t *testing.T) {
	// athena dials graph_quads_to: chaos serves quads_to (on chaos-themis), and
	// chaos's prefix is graph, so the dial is chaos's and athena has no
	// contract with chaos for it. urania_quads_to strips only urania's prefix,
	// and urania serves no quads_to, so it is nobody's.
	found := surfaceOf(t, "athena",
		report(),
		report("unanalyzable|/src/x.go:1|loose-verb|graph_quads_to — gateway",
			"unanalyzable|/src/y.go:2|loose-verb|urania_quads_to — wrong prefix",
			"unanalyzable|/src/z.go:3|loose-verb|close_node — on themis-athena"))
	got := verdicts(found)
	want := []string{
		"violated dial-uncontracted /src/x.go:1",
		"unanalyzable dial-unattributed /src/y.go:2",
		"holds dials-contracted athena",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if !strings.Contains(found[0].Detail, `which chaos serves, and orbits/chaos-athena.toml does not name it — add "quads_to"`) {
		t.Errorf("prefix detail %q", found[0].Detail)
	}
	if found[1].Detail != "athena dials urania_quads_to and no single producer's contracts name it (0 do) — if it is a new seam, its contract is orbits/<producer>-athena.toml, where <producer> is the star that registers urania_quads_to" {
		t.Errorf("unattributed detail %q", found[1].Detail)
	}
}

func TestAVerbSeveralProducersServeIsUnattributed(t *testing.T) {
	in := SurfaceInput{Star: "athena", Contracts: append(contractsFor(t), parse(t, "chaos-urania.toml", body)),
		Surface: report(), Orbits: report("unanalyzable|/src/x.go:1|loose-verb|neighbors — two producers")}
	found, err := Surface(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := verdicts(found); !slices.Equal(got, []string{"unanalyzable dial-unattributed /src/x.go:1"}) || !strings.Contains(found[0].Detail, "(2 do)") {
		t.Errorf("%v %q", got, found[0].Detail)
	}
}

func TestAnUnresolvedRegistrationMakesAMissingVerbUnanalyzable(t *testing.T) {
	found := surfaceOf(t, "urania", report("unanalyzable|verbs.go:9|unresolved|tools[i].Name"), report())
	got := verdicts(found)
	want := []string{"unanalyzable registration-unresolved urania neighbors", "unanalyzable registration-unresolved urania shape_for"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if found[0].Detail != "orbits/urania-themis.toml names neighbors, it is not among the verbs urania registers, and narcissus could not follow 1 registration site(s), so it may be one of them" {
		t.Errorf("detail %q", found[0].Detail)
	}
}

func TestSurfaceOfAStarWithNoSeamsIsInert(t *testing.T) {
	if got := verdicts(surfaceOf(t, "eros", report(), report())); !slices.Equal(got, []string{"inert no-seams eros"}) {
		t.Errorf("%v", got)
	}
}

func TestSurfaceRefusesAReportItCannotRead(t *testing.T) {
	for name, in := range map[string]SurfaceInput{
		"surface": {Star: "x", Surface: []byte("table"), Orbits: report()},
		"orbits":  {Star: "x", Surface: report(), Orbits: []byte("table")},
	} {
		if _, err := Surface(in); err == nil || !strings.Contains(err.Error(), "narc scan "+name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
