package orbitkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

var row = checks.AtomDef{ID: "orbit:x", Stage: checks.StageOrbit, Lane: checks.LaneAny}

func TestTheOrbitVerdicts(t *testing.T) {
	atomtest.Expect(t, OrbitAbsent(row, "no seam"), atomtest.StateOf(0), atomtest.Absent, "orbit:x: ABSENT - no seam")
	atomtest.Expect(t, OrbitCannot(row, errors.New("gone")), atomtest.StateOf(2), atomtest.Cannot, "orbit:x: CANNOT RUN - gone")
	v := OrbitVerdict(row, []checks.Finding{{Verdict: checks.VerdictHolds, Subject: "s", Cause: "c"}})
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:x: 1 holds")
	if len(v.Findings) != 1 {
		t.Errorf("%+v", v.Findings)
	}
}

func TestFleetContractsReadsTheDiesAndNamesWhatItCannot(t *testing.T) {
	in := atoms.Input{Dies: atomtest.DiesAt(t, nil)}
	cs, bad, err := FleetContracts(in)
	if err != nil || len(cs) != 1 || len(bad) != 0 {
		t.Fatalf("%v %v %v", cs, bad, err)
	}
	if _, _, prefixes, err := FleetContractsAndRoster(in); err != nil || prefixes["a"] != "a" {
		t.Errorf("%v %v", prefixes, err)
	}
	for name, tc := range map[string]struct {
		prepare func(t *testing.T) atoms.Input
		want    string
	}{
		"no checkout": {func(*testing.T) atoms.Input { return atoms.Input{} }, "(-dies)"},
		"contracts that will not read": {func(t *testing.T) atoms.Input {
			d := atomtest.DiesAt(t, nil)
			atomtest.BrokenLink(t, d, "orbits/z.toml")
			return atoms.Input{Dies: d}
		}, "foundry-dies: orbits/*.toml could not be read"},
		"a roster that will not read": {func(t *testing.T) atoms.Input {
			d := atomtest.DiesAt(t, nil)
			atomtest.BrokenLink(t, d, "fleet/repos/z.json")
			return atoms.Input{Dies: d}
		}, "foundry-dies: fleet/repos/z.json could not be read"},
		"orbits that do not compose": {func(t *testing.T) atoms.Input {
			d := atomtest.DiesAt(t, nil)
			if err := os.Remove(filepath.Join(d, "orbits", "a-b.toml")); err != nil {
				t.Fatal(err)
			}
			return atoms.Input{Dies: d}
		}, "foundry-dies/orbits does not compose"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := FleetContracts(tc.prepare(t)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOrbitComposedAnswersEveryStateItHas(t *testing.T) {
	composed := checks.AtomDef{ID: "ops:orbit-composed", Stage: checks.StagePrepush, Lane: checks.LaneAny}
	atomtest.Expect(t, OrbitComposed(composed, atomtest.ComposedTree(t, nil, nil)), atomtest.StateOf(0), atomtest.Pass, "the composed set of 1 contract(s)")
	absent := atomtest.TreeIn(t, map[string]string{"a": "x"})
	atomtest.Expect(t, OrbitComposed(composed, absent), atomtest.StateOf(0), atomtest.Absent, "renders no orbit sidecars")
	noDies := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	atomtest.Expect(t, OrbitComposed(composed, noDies), atomtest.StateOf(2), atomtest.Cannot, "foundry-dies' contracts could not be read")
	broken := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	broken.Dies = atomtest.DiesAt(t, nil)
	atomtest.BrokenLink(t, broken.Dies, "orbits/z.toml")
	atomtest.Expect(t, OrbitComposed(composed, broken), atomtest.StateOf(2), atomtest.Cannot, "foundry-dies' contracts could not be read")
	noRoster := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	noRoster.Dies = atomtest.DiesAt(t, nil)
	atomtest.BrokenLink(t, noRoster.Dies, "fleet/repos/z.json")
	atomtest.Expect(t, OrbitComposed(composed, noRoster), atomtest.StateOf(2), atomtest.Cannot, "foundry-dies' roster could not be read")
	unread := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	atomtest.BrokenLink(t, unread.Root, "prime/orbits/z.orbit.toml")
	atomtest.Expect(t, OrbitComposed(composed, unread), atomtest.StateOf(2), atomtest.Cannot, "the rendered sidecars could not be read")
	drift := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	drift.Dies = atomtest.DiesAt(t, nil)
	atomtest.Expect(t, OrbitComposed(composed, drift), atomtest.StateOf(1), atomtest.Findings, "prime/orbits against foundry-dies main")
}

func TestOrbitSidecarsAnswersEveryStateItHas(t *testing.T) {
	sidecars := checks.AtomDef{ID: "ops:orbit-sidecars", Stage: checks.StagePrepush, Lane: checks.LaneAny}
	ctx := context.Background()
	tree := map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"}
	for _, tc := range []struct {
		name  string
		files map[string]string
		code  int
		state int
		res   string
		want  string
	}{
		{"every sidecar parses", tree, 0, 0, atomtest.Pass, "parsed"},
		{"a sidecar is refused", tree, 1, 1, atomtest.Findings, "parsed"},
		{"a reader above 2 is a 2", tree, 7, 2, atomtest.Cannot, "parsed"},
		{"a reader that would not start never ran", tree, -1, 2, atomtest.Cannot, "the atom never ran"},
		{"no sidecar is absent", map[string]string{"flux/k.yaml": "a: 1\n"}, 0, 0, atomtest.Absent, "tracks no composed orbit sidecar"},
		{"no ops shape is absent", map[string]string{"main.go": "x"}, 0, 0, atomtest.Absent, "has no ops shape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := atomtest.ToolTree(t, tc.files, func(atoms.Cmd) (string, int) { return "parsed", tc.code })
			atomtest.Expect(t, OrbitSidecars(ctx, sidecars, in), atomtest.StateOf(tc.state), tc.res, tc.want)
		})
	}
}
