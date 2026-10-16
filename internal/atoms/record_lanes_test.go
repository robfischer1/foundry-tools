package atoms

import (
	"os"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

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
	const id = "ops:record-lanes"
	jobs, ourea := checks.RecordLanesJobsFile, checks.RecordLanesOureaFile
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a tree with neither file is absent", map[string]string{"main.go": "x"}, 0, absent,
			[]string{"not the fleet's flux tree"}},
		{"equal lists holding atoms, dev and the executor lanes pass",
			map[string]string{jobs: lanesJobs(`["gate","check","atoms","dev"]`, `["check"]`), ourea: lanesOurea(`["dev","atoms","check","gate"]`)},
			0, pass, []string{"agree"}},
		{"UT-680: an executor lane missing from ourea-config is a finding naming both",
			map[string]string{jobs: lanesJobs(`["gate","check","atoms","dev"]`, `["check"]`), ourea: lanesOurea(`["gate","atoms","dev"]`)},
			1, findings, []string{"check", "missing from record_lanes in prime/ourea-config.yaml"}},
		{"unequal lists are findings",
			map[string]string{jobs: lanesJobs(`["gate","atoms","dev","orbit"]`, ""), ourea: lanesOurea(`["gate","atoms","dev"]`)},
			1, findings, []string{"not equal", "orbit"}},
		{"atoms missing is a finding",
			map[string]string{jobs: lanesJobs(`["gate","dev"]`, ""), ourea: lanesOurea(`["gate","dev"]`)},
			1, findings, []string{"atoms"}},
		{"dev missing is a finding",
			map[string]string{jobs: lanesJobs(`["gate","atoms"]`, ""), ourea: lanesOurea(`["gate","atoms"]`)},
			1, findings, []string{"dev"}},
		{"one file moved is not absence",
			map[string]string{jobs: lanesJobs(`["atoms","dev"]`, "")},
			2, cannot, []string{"CANNOT RUN", checks.RecordLanesOureaFile}},
		{"a file that does not parse is a file not judged",
			map[string]string{jobs: "a: [", ourea: lanesOurea(`["atoms","dev"]`)},
			2, cannot, []string{"CANNOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), checks.State(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a listed file that will not read is a file not judged", func(t *testing.T) {
		in := treeIn(t, map[string]string{jobs: lanesJobs(`["atoms","dev"]`, ""), ourea: lanesOurea(`["atoms","dev"]`)})
		if err := os.Remove(in.Root + "/" + jobs); err != nil {
			t.Fatal(err)
		}
		expect(t, runAtom(t, id, in), checks.StateCannotRun, cannot, "would not read")
	})
	t.Run("a file list that would not enumerate is not a pass", func(t *testing.T) {
		in := treeIn(t, map[string]string{jobs: lanesJobs(`["atoms","dev"]`, ""), ourea: lanesOurea(`["atoms","dev"]`)})
		in.FilesErr = os.ErrPermission
		expect(t, runAtom(t, id, in), checks.StateCannotRun, cannot, "would not enumerate")
	})
}
