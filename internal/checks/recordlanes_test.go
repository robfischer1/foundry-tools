package checks

import (
	"strings"
	"testing"
)

// jobsYAML is flux's daedalus-jobs ConfigMap in its real shape: the lists are
// JSON inside string values, and a comment names both keys.
func jobsYAML(record, executor string) string {
	s := "# RECORD_LANES and EXECUTOR_LANES are explained here.\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: daedalus-jobs}\ndata:\n"
	if record != "" {
		s += "  RECORD_LANES: '" + record + "'\n"
	}
	if executor != "" {
		s += "  EXECUTOR_LANES: '" + executor + "'\n"
	}
	return s
}

// oureaYAML is ourea-config's ConfigMap: the TOML rides in config.toml.
func oureaYAML(toml string) string {
	s := "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: ourea-config}\ndata:\n  config.toml: |\n"
	for _, l := range strings.Split(toml, "\n") {
		s += "    " + l + "\n"
	}
	return s
}

func laneFiles(jobs, ourea string) map[string]string {
	return map[string]string{RecordLanesJobsFile: jobs, RecordLanesOureaFile: ourea}
}

func TestRecordLanesPass(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"equal, atoms and dev present, no executor lanes": laneFiles(
			jobsYAML(`["gate","atoms","dev"]`, ""), oureaYAML(`record_lanes = ["dev", "gate", "atoms"]`)),
		"equal and holding the executor lanes": laneFiles(
			jobsYAML(`["gate","check","atoms","dev"]`, `["check"]`), oureaYAML("door_url = \"x\"\nrecord_lanes = [\"atoms\",\"check\",\"dev\",\"gate\"]\n[[offsite_remotes]]\nname = \"a\"")),
		"a comment naming a lane is inert": laneFiles(
			jobsYAML(`["atoms","dev"]`, ""), oureaYAML("# record_lanes = [\"check\"]\nrecord_lanes = [\"atoms\",\"dev\"]")),
		"UT-681 canary entries contribute their lane": laneFiles(
			jobsYAML(`["gate","atoms","dev"]`, `["gate@stellar-core","gate@foundry/flux","gate@foundry-tools"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
		"UT-681 a bare lane beside a canary of it": laneFiles(
			jobsYAML(`["gate","check","atoms","dev"]`, `["check","gate@flux"]`), oureaYAML(`record_lanes = ["gate","check","atoms","dev"]`)),
		"EX-1 exclusions contribute nothing": laneFiles(
			jobsYAML(`["gate","check","atoms","dev"]`, `["atoms","check","gate","!gate@prometheus","!gate@foundry/nomos"]`), oureaYAML(`record_lanes = ["gate","check","atoms","dev"]`)),
	} {
		if state, report := RecordLanes(files); state != int(StatePass) {
			t.Errorf("%s: state %d, want pass:\n%s", name, state, report)
		}
	}
}

func TestRecordLanesFindings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		needles []string
	}{
		{ // UT-680
			"an executor lane missing from ourea-config",
			laneFiles(jobsYAML(`["gate","check","atoms","dev"]`, `["check"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{"check", "missing from record_lanes in prime/ourea-config.yaml"},
		},
		{
			"an executor lane missing from daedalus-jobs",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["check"]`), oureaYAML(`record_lanes = ["gate","check","atoms","dev"]`)),
			[]string{"check", "missing from RECORD_LANES in prime/daedalus-jobs.yaml"},
		},
		{
			"unequal lists",
			laneFiles(jobsYAML(`["gate","atoms","dev","mutation"]`, ""), oureaYAML(`record_lanes = ["gate","atoms","dev","visual"]`)),
			[]string{"mutation", "visual", "not equal"},
		},
		{
			"atoms missing from both",
			laneFiles(jobsYAML(`["gate","dev"]`, ""), oureaYAML(`record_lanes = ["gate","dev"]`)),
			[]string{"atoms", "missing from RECORD_LANES in prime/daedalus-jobs.yaml", "missing from record_lanes in prime/ourea-config.yaml"},
		},
		{
			"dev missing from one",
			laneFiles(jobsYAML(`["gate","atoms"]`, ""), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{"dev", "missing from RECORD_LANES in prime/daedalus-jobs.yaml"},
		},
		{ // UT-682
			"a canary lane missing from the record lists",
			laneFiles(jobsYAML(`["atoms","dev"]`, `["gate@flux"]`), oureaYAML(`record_lanes = ["atoms","dev"]`)),
			[]string{"gate: missing from RECORD_LANES in prime/daedalus-jobs.yaml", "gate: missing from record_lanes in prime/ourea-config.yaml"},
		},
		{
			"UT-683 malformed: no repo",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate@"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "gate@" is malformed (want lane, lane@repo or !lane@repo, the repo with at most one '/')`},
		},
		{
			"UT-683 malformed: no lane",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["@flux"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "@flux" is malformed`},
		},
		{
			"UT-683 malformed: two @",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate@a@b"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "gate@a@b" is malformed`},
		},
		{
			"UT-683 malformed: two slashes",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate@a/b/c"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "gate@a/b/c" is malformed`},
		},
		{
			"EX-1 malformed: no repo",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate","!gate"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "!gate" is malformed`},
		},
		{
			"EX-1 malformed: no lane",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate","!@x"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "!@x" is malformed`},
		},
		{
			"EX-1 malformed: double bang",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate","!!gate@x"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "!!gate@x" is malformed`},
		},
		{
			"EX-1 malformed: two slashes",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["gate","!gate@a/b/c"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`EXECUTOR_LANES entry "!gate@a/b/c" is malformed`},
		},
		{
			"EX-1 exclusion without its bare lane",
			laneFiles(jobsYAML(`["gate","atoms","dev"]`, `["!gate@x"]`), oureaYAML(`record_lanes = ["gate","atoms","dev"]`)),
			[]string{`exclusion "!gate@x" has no bare lane`},
		},
		{
			"EX-1 an exclusion does not owe its lane a record",
			laneFiles(jobsYAML(`["atoms","dev"]`, `["!check@x"]`), oureaYAML(`record_lanes = ["atoms","dev"]`)),
			[]string{`exclusion "!check@x" has no bare lane`},
		},
	} {
		state, report := RecordLanes(tc.files)
		if state != int(StateFindings) {
			t.Errorf("%s: state %d, want findings:\n%s", tc.name, state, report)
		}
		for _, n := range tc.needles {
			if !strings.Contains(report, n) {
				t.Errorf("%s: report lacks %q:\n%s", tc.name, n, report)
			}
		}
	}
}

// A list that cannot be read is a list not judged: could-not-run, never a pass.
func TestRecordLanesCannotRun(t *testing.T) {
	good := oureaYAML(`record_lanes = ["atoms","dev"]`)
	for name, files := range map[string]map[string]string{
		"the jobs file is not YAML":     laneFiles("a: [", good),
		"RECORD_LANES is not JSON":      laneFiles(jobsYAML(`gate,atoms`, ""), good),
		"EXECUTOR_LANES is not JSON":    laneFiles(jobsYAML(`["atoms","dev"]`, `nope`), good),
		"RECORD_LANES key moved":        laneFiles(jobsYAML("", ""), good),
		"the ourea TOML does not parse": laneFiles(jobsYAML(`["atoms","dev"]`, ""), oureaYAML(`record_lanes = [`)),
		"record_lanes key moved":        laneFiles(jobsYAML(`["atoms","dev"]`, ""), oureaYAML(`door_url = "x"`)),
		"config.toml key moved":         laneFiles(jobsYAML(`["atoms","dev"]`, ""), "kind: ConfigMap\ndata: {other: x}\n"),
		"the ourea file is absent":      {RecordLanesJobsFile: jobsYAML(`["atoms","dev"]`, "")},
		"the jobs file is absent":       {RecordLanesOureaFile: good},
		"the ourea file is not YAML":    laneFiles(jobsYAML(`["atoms","dev"]`, ""), "a: ["),
		"data holds other keys only":    laneFiles("kind: ConfigMap\ndata: {OTHER: x}\n", good),
		"no document carries data":      laneFiles("kind: ConfigMap\n", good),
	} {
		if state, report := RecordLanes(files); state != int(StateCannotRun) {
			t.Errorf("%s: state %d, want cannot-run:\n%s", name, state, report)
		}
	}
}

func TestIsRecordLanesFile(t *testing.T) {
	for path, want := range map[string]bool{
		RecordLanesJobsFile: true, RecordLanesOureaFile: true,
		"prime/star-a.yaml": false, "daedalus-jobs.yaml": false,
	} {
		if got := IsRecordLanesFile(path); got != want {
			t.Errorf("IsRecordLanesFile(%q) = %v, want %v", path, got, want)
		}
	}
}

// BG1: the green colour's ConfigMap is judged against the same ourea list when
// the tree has it, and its absence is a tree from before the second colour.
func greenFiles(blue, green, ourea string) map[string]string {
	f := laneFiles(blue, ourea)
	f[RecordLanesJobsGreenFile] = green
	return f
}

func TestBG1_RecordLanesJudgesTheGreenColourToo(t *testing.T) {
	ok := jobsYAML(`["gate","check","atoms","dev"]`, `["check"]`)
	ourea := oureaYAML(`record_lanes = ["gate","check","atoms","dev"]`)
	if state, report := RecordLanes(greenFiles(ok, ok, ourea)); state != int(StatePass) {
		t.Fatalf("equal colours: state %d:\n%s", state, report)
	}
	drift := jobsYAML(`["gate","atoms","dev"]`, `["check"]`)
	state, report := RecordLanes(greenFiles(ok, drift, ourea))
	if state != int(StateFindings) || !strings.Contains(report, "check: missing from RECORD_LANES in "+RecordLanesJobsGreenFile) {
		t.Fatalf("green missing a lane: state %d:\n%s", state, report)
	}
	if strings.Contains(report, "in "+RecordLanesJobsFile) {
		t.Fatalf("blue is fine and must not be named:\n%s", report)
	}
	if state, _ := RecordLanes(greenFiles(ok, "a: [", ourea)); state != int(StateCannotRun) {
		t.Fatalf("an unreadable green file must be could-not-run, got %d", state)
	}
	if state, _ := RecordLanes(laneFiles(ok, ourea)); state != int(StatePass) {
		t.Fatalf("a tree with no green file is judged as before, got %d", state)
	}
}

func TestBG1_IsRecordLanesFileNamesTheGreenFile(t *testing.T) {
	if !IsRecordLanesFile(RecordLanesJobsGreenFile) {
		t.Fatal("the green ConfigMap is one of the files this atom reads")
	}
}
