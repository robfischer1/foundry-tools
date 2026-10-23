package fleetbgparity

import (
	"context"
	"os"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Toolchain != "none" ||
		Def.Surface != "any:prime/daedalus-jobs*.yaml" || Def.Group != checks.GroupOf(id) {
		t.Errorf("%+v", Def)
	}
}

func jobsMap(group string) string {
	return "kind: ConfigMap\ndata:\n  DAEDALUS_JOBS_GROUP: '" + group + "'\n  RECORD_LANES: '[\"atoms\",\"dev\"]'\n"
}

func TestFleetBGParity(t *testing.T) {
	blue, green := checks.BGParityBlueFile, checks.BGParityGreenFile
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a tree with no green map is one colour: absent", map[string]string{blue: jobsMap("daedalus-jobs")}, 0, atomtest.Absent,
			[]string{"one colour"}},
		{"a tree with neither is absent", map[string]string{"main.go": "x"}, 0, atomtest.Absent, nil},
		{"colours that agree pass", map[string]string{blue: jobsMap("daedalus-jobs"), green: jobsMap("daedalus-jobs")}, 0, atomtest.Pass, []string{"agree"}},
		{"a group typo is a finding", map[string]string{blue: jobsMap("daedalus-jobs"), green: jobsMap("daedalus-job")}, 1, atomtest.Findings,
			[]string{"DAEDALUS_JOBS_GROUP"}},
		{"green without blue is not absence", map[string]string{green: jobsMap("daedalus-jobs")}, 2, atomtest.Cannot,
			[]string{"CANNOT RUN", blue}},
		{"a map that does not parse is a map not judged", map[string]string{blue: "a: [", green: jobsMap("daedalus-jobs")}, 2, atomtest.Cannot,
			[]string{"CANNOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, run(atomtest.TreeIn(t, tc.files)), checks.State(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a listed file that will not read is a file not judged", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{blue: jobsMap("daedalus-jobs"), green: jobsMap("daedalus-jobs")})
		if err := os.Remove(in.Root + "/" + blue); err != nil {
			t.Fatal(err)
		}
		atomtest.Expect(t, run(in), checks.StateCannotRun, atomtest.Cannot, "would not read")
	})
	t.Run("a file list that would not enumerate is not a pass", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{blue: jobsMap("daedalus-jobs"), green: jobsMap("daedalus-jobs")})
		in.FilesErr = os.ErrPermission
		atomtest.Expect(t, run(in), checks.StateCannotRun, atomtest.Cannot, "would not enumerate")
	})
}
