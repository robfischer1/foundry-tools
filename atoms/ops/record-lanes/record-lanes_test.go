package opsrecordlanes

import (
	"context"
	"os"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func lanesJobs(record, executor string) string {
	s := "kind: ConfigMap\ndata:\n  RECORD_LANES: '" + record + "'\n"
	if executor != "" {
		s += "  EXECUTOR_LANES: '" + executor + "'\n"
	}
	return s
}

func lanesOurea(list string) string {
	return "kind: ConfigMap\ndata:\n  config.toml: |\n    record_lanes = " + list + "\n"
}

// ops:record-lanes: UT-680 and IT-195. The atom reads the two flux files and
// stands down on any other tree.
func TestOpsRecordLanes(t *testing.T) {
	jobs, ourea := checks.RecordLanesJobsFile, checks.RecordLanesOureaFile
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a tree with neither file is absent", map[string]string{"main.go": "x"}, 0, atomtest.Absent,
			[]string{"not the fleet's flux tree"}},
		{"equal lists holding atoms, dev and the executor lanes pass",
			map[string]string{jobs: lanesJobs(`["gate","check","atoms","dev"]`, `["check"]`), ourea: lanesOurea(`["dev","atoms","check","gate"]`)},
			0, atomtest.Pass, []string{"agree"}},
		{"UT-680: an executor lane missing from ourea-config is a finding naming both",
			map[string]string{jobs: lanesJobs(`["gate","check","atoms","dev"]`, `["check"]`), ourea: lanesOurea(`["gate","atoms","dev"]`)},
			1, atomtest.Findings, []string{"check", "missing from record_lanes in prime/ourea-config.yaml"}},
		{"unequal lists are findings",
			map[string]string{jobs: lanesJobs(`["gate","atoms","dev","visual"]`, `["visual"]`), ourea: lanesOurea(`["gate","atoms","dev"]`)},
			1, atomtest.Findings, []string{"not equal", "visual"}},
		{"atoms missing is a finding",
			map[string]string{jobs: lanesJobs(`["gate","dev"]`, ""), ourea: lanesOurea(`["gate","dev"]`)},
			1, atomtest.Findings, []string{"atoms"}},
		{"dev missing is a finding",
			map[string]string{jobs: lanesJobs(`["gate","atoms"]`, ""), ourea: lanesOurea(`["gate","atoms"]`)},
			1, atomtest.Findings, []string{"dev"}},
		{"one file moved is not absence",
			map[string]string{jobs: lanesJobs(`["atoms","dev"]`, "")},
			2, atomtest.Cannot, []string{"CANNOT RUN", checks.RecordLanesOureaFile}},
		{"a file that does not parse is a file not judged",
			map[string]string{jobs: "a: [", ourea: lanesOurea(`["atoms","dev"]`)},
			2, atomtest.Cannot, []string{"CANNOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, run(atomtest.TreeIn(t, tc.files)), checks.State(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a listed file that will not read is a file not judged", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{jobs: lanesJobs(`["atoms","dev"]`, ""), ourea: lanesOurea(`["atoms","dev"]`)})
		if err := os.Remove(in.Root + "/" + jobs); err != nil {
			t.Fatal(err)
		}
		atomtest.Expect(t, run(in), checks.StateCannotRun, atomtest.Cannot, "would not read")
	})
	t.Run("a file list that would not enumerate is not a pass", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{jobs: lanesJobs(`["atoms","dev"]`, ""), ourea: lanesOurea(`["atoms","dev"]`)})
		in.FilesErr = os.ErrPermission
		atomtest.Expect(t, run(in), checks.StateCannotRun, atomtest.Cannot, "would not enumerate")
	})
}
