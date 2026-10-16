package orbitsidecars

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "orbit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func findingsOf(v checks.Verdict) string {
	var out []string
	for _, f := range v.Findings {
		out = append(out, f.Verdict+" "+f.Cause+" "+f.Subject)
	}
	return strings.Join(out, "\n")
}

// opsTree is a tree an ops atom grades: one sidecar tracked and a flux file.
func opsTree() map[string]string {
	return map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"}
}

func TestOrbitSidecarsIsAbsentWhereNothingRendersSidecars(t *testing.T) {
	atomtest.Expect(t, run(atomtest.TreeIn(t, map[string]string{"a": "x"})), atomtest.StateOf(0), atomtest.Absent, "renders no orbit sidecars")
}

// WITHOUT foundry-dies main the first check cannot look, and the fold says which.
func TestOrbitSidecarsWithoutTheDiesIsATwo(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "ops:orbit-composed could not run")
}

func TestOrbitSidecarsFoldsTheTwoFluxChecks(t *testing.T) {
	in := atomtest.ComposedTree(t, nil, nil)
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:sidecars: 2 holds")
	if findingsOf(v) != "holds ops:orbit-composed prime/orbits\nholds ops:orbit-sidecars prime/orbits" {
		t.Errorf("findings %s", findingsOf(v))
	}
	in = atomtest.ComposedTree(t, func(r map[string][]byte) { r["themis.orbit.toml"] = []byte("stale") }, nil)
	v = run(in)
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "1 drifted", "[report-only: would be violated]", "stale against its contracts")
}

func TestOrbitSidecarsRecordsBothGateAtomsAsFindings(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	in.Dies = atomtest.DiesAt(t, nil)
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:sidecars: ")
	if len(v.Findings) != 2 || v.Findings[0].Cause != "ops:orbit-composed" || v.Findings[1].Cause != "ops:orbit-sidecars" {
		t.Fatalf("findings %+v: want one per gate atom, composed first", v.Findings)
	}
	if v.Findings[0].Verdict != checks.VerdictDrifted {
		t.Errorf("composed drift, report-only: %+v", v.Findings[0])
	}
	if v.Findings[1].Verdict != checks.VerdictHolds || !strings.Contains(v.Findings[1].Detail, "no ops shape") {
		t.Errorf("a tree that is not an ops tree holds ops:orbit-sidecars by absence: %+v", v.Findings[1])
	}
}

// What the reader says about the sidecars is the second finding.
func TestOrbitSidecarsFoldsTheReadersAnswer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		verdict string
		cannot  bool
	}{
		{"a sidecar the reader accepts holds", 0, "parsed 1", checks.VerdictHolds, false},
		{"a sidecar the reader refuses is recorded", 1, "a.orbit.toml: bad acl", checks.VerdictDrifted, false},
		{"a reader that would not start is a 2", -1, "orbitparse: not found", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := atomtest.TreeIn(t, opsTree())
			in.Dies = atomtest.DiesAt(t, nil)
			var called atoms.Cmd
			in.Exec = func(_ context.Context, c atoms.Cmd) (string, int) { called = c; return tc.out, tc.code }
			v := run(in)
			if tc.cannot {
				atomtest.Expect(t, v, atomtest.StateOf(2), atomtest.Cannot, "ops:orbit-sidecars could not run", "the atom never ran")
				return
			}
			// A pass's reason is "<id>: PASS"; only a finding carries the reader's words.
			got := v.Findings[1]
			if got.Verdict != tc.verdict || (tc.verdict == checks.VerdictDrifted && !strings.Contains(got.Detail, tc.out)) {
				t.Errorf("finding %+v, want %s carrying %q", got, tc.verdict, tc.out)
			}
			want := []string{in.Root + "/prime/orbits/a.orbit.toml"}
			if called.Name != "orbitparse" || !reflect.DeepEqual(called.Args, want) || !called.Both {
				t.Errorf("the reader was run as %+v, want orbitparse %v with both streams", called, want)
			}
		})
	}
}

// A check that cannot run makes the fold a 2 naming it.
func TestOrbitSidecarsCannotRunWhenACheckCannot(t *testing.T) {
	in := atomtest.ComposedTree(t, nil, nil)
	atomtest.BrokenLink(t, in.Dies, "orbits/z.toml")
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "ops:orbit-composed could not run")

	in = atomtest.ComposedTree(t, nil, map[string]string{"flux/k.yaml": "a: 1\n"})
	in.Exec = func(context.Context, atoms.Cmd) (string, int) { return "gone", -1 }
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "ops:orbit-sidecars could not run", "CANNOT RUN")
}
