package atomkit

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func lane(extra string) string {
	return "schema = \"lane@1\"\nlane = \"gate\"\nstages = [\"precommit\"]\n" + extra
}

func TestUT_600_ParseLane(t *testing.T) {
	got, err := ParseLane([]byte("schema = \"lane@1\"\nlane = \"gate\"\nstages = [\"precommit\",\"prepush\"]\nrider = \"visual\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := LaneDef{Schema: "lane@1", Lane: "gate", Stages: []string{"precommit", "prepush"}, Rider: "visual"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseLaneAtomLanes(t *testing.T) {
	got, err := ParseLane([]byte(lane("atom_lanes = [\"go\", \"ts\"]\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AtomLanes, []string{"go", "ts"}) {
		t.Fatalf("got %+v", got.AtomLanes)
	}
	for _, l := range []string{"go", "python", "rust", "ts"} {
		if _, err := ParseLane([]byte(lane("atom_lanes = [\"" + l + "\"]\n"))); err != nil {
			t.Errorf("atom_lanes %s: %v", l, err)
		}
	}
	if plain, err := ParseLane([]byte(lane(""))); err != nil || plain.AtomLanes != nil {
		t.Errorf("no atom_lanes narrows nothing: %+v %v", plain.AtomLanes, err)
	}
}

func TestLaneErrors(t *testing.T) {
	ph := func(body string) string { return lane("\n[[phase]]\n" + body) }
	for _, c := range []struct{ row, src, want string }{
		{"UT-601", lane("x = 1\n"), "unknown key x"},
		{"UT-601", strings.Replace(lane(""), "lane@1", "lane@2", 1), "schema is lane@2, want lane@1"},
		{"FN-107 · 1", "lane = \"gate\"\n", "schema is missing, want lane@1"},
		{"UT-602", strings.Replace(lane(""), `"gate"`, `"gat"`, 1), "lane gat is unknown"},
		{"atom_lanes", lane("atom_lanes = [\"perl\"]\n"), "atom_lanes: lane perl is unknown"},
		{"atom_lanes", lane("atom_lanes = [\"go\", \"\"]\n"), "atom_lanes: lane  is unknown"},
		{"UT-603/IT-173", strings.Replace(lane(""), `["precommit"]`, `["precommmit"]`, 1), "stage precommmit is unknown"},
		{"UT-604", ph("name = \"x\"\nkind = \"parallel\"\natoms = [\"stage:precommit\"]\n"), "phase x: kind parallel is not fanout or sequence"},
		{"UT-605/IT-172", ph("name = \"x\"\nkind = \"fanout\"\ncapability = \"sbom\"\n"), "capability sbom is unknown"},
		{"UT-606", ph("name = \"x\"\nkind = \"fanout\"\natoms = [\"go:release\"]\ncapability = \"scan\"\n"), "phase x: names atoms and a capability"},
		{"UT-606", ph("name = \"x\"\nkind = \"fanout\"\n"), "phase x: names no atoms and no capability"},
		{"UT-607", ph("name = \"a\"\nkind = \"fanout\"\natoms = [\"stage:precommit\"]\nafter = [\"b\"]\n[[phase]]\nname = \"b\"\nkind = \"fanout\"\natoms = [\"stage:precommit\"]\n"), "phase a: after b, which is not an earlier phase"},
		{"UT-608", ph("name = \"x\"\nkind = \"fanout\"\natoms = [\"stage:precommit\"]\n[[phase]]\nname = \"x\"\nkind = \"fanout\"\natoms = [\"stage:precommit\"]\n"), "phase x is named twice"},
		{"UT-609", ph("name = \"sign\"\nkind = \"sequence\"\ncapability = \"scan\"\nsecrets = [\"infisical-kms\"]\n[[phase]]\nname = \"push\"\nkind = \"fanout\"\ncapability = \"push-bundle\"\nsecrets = [\"infisical-kms\"]\n"), "secret infisical-kms is named by phases sign and push"},
	} {
		_, err := ParseLane([]byte(c.src))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: want %q, got %v", c.row, c.want, err)
		}
	}
}

func TestUT_603_AllStagesAccepted(t *testing.T) {
	src := strings.Replace(lane(""), `["precommit"]`, `["govern","dev","atoms"]`, 1)
	src = strings.Replace(src, `"gate"`, `"govern"`, 1)
	if _, err := ParseLane([]byte(src)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"precommit", "prepush", "mutation", "visual", "govern", "dev", "atoms"} {
		if _, err := ParseLane([]byte(strings.Replace(lane(""), `["precommit"]`, `["`+s+`"]`, 1))); err != nil {
			t.Errorf("stage %s: %v", s, err)
		}
	}
}

// mutation-bg and bases are retired (no lane by either name is asked): neither
// may parse. A repository of bases builds on the build lane (DEF-B6-035).
func TestRetiredLanesRefused(t *testing.T) {
	for _, l := range []string{"mutation-bg", "bases"} {
		if slices.Contains(KnownLanes, l) {
			t.Fatalf("%s is still a known lane", l)
		}
		if _, err := ParseLane([]byte(strings.Replace(lane(""), `"gate"`, `"`+l+`"`, 1))); err == nil {
			t.Errorf("a lane file named %s parsed", l)
		}
	}
}

func TestEveryKnownLaneAccepted(t *testing.T) {
	if len(KnownLanes) != 14 {
		t.Fatalf("KnownLanes has %d, want 14", len(KnownLanes))
	}
	for _, l := range KnownLanes {
		if _, err := ParseLane([]byte(strings.Replace(lane(""), `"gate"`, `"`+l+`"`, 1))); err != nil {
			t.Errorf("lane %s: %v", l, err)
		}
	}
}

func TestEveryCapabilityAccepted(t *testing.T) {
	for _, c := range []string{"release", "assemble", "scan", "publish", "push-bundle", "layer-cast", "build-atoms", "dev-output", "push-visual"} {
		src := "schema = \"lane@1\"\nlane = \"build\"\n\n[[phase]]\nname = \"p\"\nkind = \"fanout\"\ncapability = \"" + c + "\"\n"
		if _, err := ParseLane([]byte(src)); err != nil {
			t.Errorf("capability %s: %v", c, err)
		}
	}
}

func TestUT_610_ExpandPhasesFromStages(t *testing.T) {
	got := ExpandPhases(LaneDef{Lane: "gate", Stages: []string{"precommit", "prepush"}})
	want := []PhaseDef{{Name: "gate", Kind: "fanout", Atoms: []string{"stage:precommit", "stage:prepush"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestUT_611_ExpandPhasesAsGiven(t *testing.T) {
	ps := []PhaseDef{{Name: "a", Kind: "fanout", Atoms: []string{"x:y"}}, {Name: "b", Kind: "sequence", Capability: "scan", After: []string{"a"}}}
	if got := ExpandPhases(LaneDef{Lane: "build", Phases: ps}); !reflect.DeepEqual(got, ps) {
		t.Fatalf("got %+v", got)
	}
}

func TestIT_171_BundlePhases(t *testing.T) {
	src := "schema = \"lane@1\"\nlane = \"bundle\"\n" +
		"[[phase]]\nname = \"build\"\nkind = \"fanout\"\natoms = [\"stage:prepush\"]\n" +
		"[[phase]]\nname = \"sign\"\nkind = \"sequence\"\ncapability = \"scan\"\nafter = [\"build\"]\nsecrets = [\"infisical-kms\"]\n" +
		"[[phase]]\nname = \"push\"\nkind = \"fanout\"\ncapability = \"push-bundle\"\nafter = [\"sign\"]\n"
	d, err := ParseLane([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Phases) != 3 || d.Phases[1].Capability != "scan" || d.Phases[2].After[0] != "sign" || d.Phases[1].Secrets[0] != "infisical-kms" {
		t.Fatalf("%+v", d.Phases)
	}
}

// TestUT_615_LaneGoldenDecodes decodes testdata/lane.json; foundry-tools'
// copy decodes the same file (the sha256 sameness is IT-221's).
func TestUT_615_LaneGoldenDecodes(t *testing.T) {
	var d LaneDef
	if err := json.Unmarshal(readGolden(t, "lane"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Lane != "bundle" || len(d.Phases) != 3 || d.Phases[1].Capability != "scan" {
		t.Fatalf("%+v", d)
	}
	if err := roundTrip[LaneDef](readGolden(t, "lane")); err != nil {
		t.Fatal(err)
	}
}
