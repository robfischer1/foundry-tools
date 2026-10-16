package main

import (
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func chainJobs(record, executor string) string {
	s := "kind: ConfigMap\ndata:\n  RECORD_LANES: '" + record + "'\n"
	if executor != "" {
		s += "  EXECUTOR_LANES: '" + executor + "'\n"
	}
	return s
}

func chainOurea(list string) string {
	return "kind: ConfigMap\ndata:\n  config.toml: |\n    record_lanes = " + list + "\n"
}

// ---- ops:record-lanes ----

func TestOpsRecordLanesTable(t *testing.T) {
	jobs, ourea := checks.RecordLanesJobsFile, checks.RecordLanesOureaFile
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		needles []string
	}{
		{"neither file is absent", nil, 0, []string{"ABSENT", "not the fleet's flux tree"}},
		{"equal lists holding atoms, dev and the executor lanes pass",
			map[string]string{jobs: chainJobs(`["gate","check","atoms","dev"]`, `["check"]`), ourea: chainOurea(`["dev","atoms","check","gate"]`)},
			0, []string{"agree"}},
		{"UT-680: an executor lane missing from ourea-config",
			map[string]string{jobs: chainJobs(`["gate","check","atoms","dev"]`, `["check"]`), ourea: chainOurea(`["gate","atoms","dev"]`)},
			1, []string{"check", "missing from record_lanes in prime/ourea-config.yaml"}},
		{"unequal lists",
			map[string]string{jobs: chainJobs(`["gate","atoms","dev","orbit"]`, ""), ourea: chainOurea(`["gate","atoms","dev"]`)},
			1, []string{"not equal", "orbit"}},
		{"atoms missing",
			map[string]string{jobs: chainJobs(`["gate","dev"]`, ""), ourea: chainOurea(`["gate","dev"]`)},
			1, []string{"atoms"}},
		{"dev missing",
			map[string]string{jobs: chainJobs(`["gate","atoms"]`, ""), ourea: chainOurea(`["gate","atoms"]`)},
			1, []string{"dev"}},
		{"one file moved is not absence",
			map[string]string{jobs: chainJobs(`["atoms","dev"]`, "")}, 2, []string{"CANNOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(fleetTree(tc.files))
			wantReport(t, runAtom(t, "ops:record-lanes", ""), tc.state, tc.needles...)
		})
	}
}

func TestOpsRecordLanesNeverRunsOnAnUnreadableTree(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{checks.RecordLanesJobsFile: chainJobs(`["atoms","dev"]`, ""), checks.RecordLanesOureaFile: chainOurea(`["atoms","dev"]`)}))
	engine.failLeaf(`"`+checks.RecordLanesJobsFile+`"`, "contents", "the file would not read")
	wantReport(t, runAtom(t, "ops:record-lanes", ""), 2, "CANNOT RUN - prime/daedalus-jobs.yaml would not read")

	engine.reset()
	engine.withTree(fleetTree(nil))
	engine.fail(`glob(pattern:"`+checks.RecordLanesJobsFile+`")`, "the tree went away")
	wantReport(t, runAtom(t, "ops:record-lanes", ""), 2, "the tree would not enumerate")
}
