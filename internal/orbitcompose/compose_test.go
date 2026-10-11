package orbitcompose

import (
	"strings"
	"testing"
)

func contract(producer, consumer string, verbs ...string) Contract {
	return Contract{
		Name: producer + "-" + consumer, Producer: producer, Consumer: consumer,
		Version: "1", Status: "generated", Verbs: verbs,
	}
}

func names(cs []Contract) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return strings.Join(out, ",")
}

func TestComposeGivesEachStarItsTwoSidesSorted(t *testing.T) {
	got := Compose([]Contract{
		contract("urania", "themis", "shape_for"),
		contract("chaos", "urania", "shapes"),
		contract("chaos", "themis", "capture"),
		contract("urania", "athena", "get_litigant"),
	})
	want := []struct{ star, produces, consumes string }{
		{"athena", "", "urania-athena"},
		{"chaos", "chaos-themis,chaos-urania", ""},
		{"themis", "", "chaos-themis,urania-themis"},
		{"urania", "urania-athena,urania-themis", "chaos-urania"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sidecars, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Star != w.star || names(got[i].Produces) != w.produces || names(got[i].Consumes) != w.consumes {
			t.Errorf("sidecar %d: got %s produces=%s consumes=%s, want %+v",
				i, got[i].Star, names(got[i].Produces), names(got[i].Consumes), w)
		}
	}
}

const uraniaSidecar = `# orbit.toml — urania's seam contracts, composed by foundry-tools orbitcompose
# from foundry-dies/orbits. [[produces]] is the inbound ACL the endpoint PEP
# reads; [[consumes]] references the producer's contract. Generated — do not
# hand-edit: change the contract and re-run the composer.

[[produces]]
to = "athena"
contract = "urania-athena"
version = "1"
status = "generated"
verbs = ["get_litigant"]

[[produces]]
to = "themis"
contract = "urania-themis"
version = "1"
status = "generated"
verbs = ["neighbors", "shape_for"]

[[consumes]]
from = "chaos"
contract = "chaos-urania"
version = "2"
status = "approved"
verbs = ["shapes"]
`

// A CI lane's edge is on its producer's sidecar alone: a job has no sidecar.
func TestComposeGivesAJobNoSidecar(t *testing.T) {
	got := Compose([]Contract{contract("narcissus", "job.gate", "witness")})
	if len(got) != 1 || got[0].Star != "narcissus" || names(got[0].Produces) != "narcissus-job.gate" || len(got[0].Consumes) != 0 {
		t.Fatalf("%+v", got)
	}
	if r := string(Render(got[0])); !strings.Contains(r, "[[produces]]\nto = \"job.gate\"\ncontract = \"narcissus-job.gate\"") {
		t.Errorf("%s", r)
	}
}

func TestRenderIsTheComposedSidecarDialect(t *testing.T) {
	approved := contract("chaos", "urania", "shapes")
	approved.Version, approved.Status = "2", "approved"
	got := string(Render(Sidecar{
		Star:     "urania",
		Produces: []Contract{contract("urania", "athena", "get_litigant"), contract("urania", "themis", "neighbors", "shape_for")},
		Consumes: []Contract{approved},
	}))
	if got != uraniaSidecar {
		t.Errorf("got:\n%s\nwant:\n%s", got, uraniaSidecar)
	}
}
