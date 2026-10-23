package checks

import (
	"strings"
	"testing"
)

// cm renders a daedalus-jobs ConfigMap from key/value pairs, values single-quoted.
func cm(kv ...string) string {
	s := "# RECORD_LANES is explained here.\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: daedalus-jobs}\ndata:\n"
	for i := 0; i < len(kv); i += 2 {
		s += "  " + kv[i] + ": '" + kv[i+1] + "'\n"
	}
	return s
}

func colours(blue, green string) map[string]string {
	return map[string]string{BGParityBlueFile: blue, BGParityGreenFile: green}
}

var parityBase = []string{
	"DAEDALUS_JOBS_GROUP", "daedalus-jobs",
	"GATE_JOB_NAMESPACE", "forge",
	"RECORD_LANES", `["gate","atoms","dev"]`,
	"GATE_JOB_SERVICE_ACCOUNT", "ci",
	"MUTATION_JOB_SERVICE_ACCOUNT", "ci",
	"CI_IDENTITY_V2_LANES", "gate,check",
	"RECIPE_JOB_LANES", `{"cast":{"service_account":"ci","call":["cast"]}}`,
}

func with(over ...string) []string {
	m := map[string]string{}
	var order []string
	for _, src := range [][]string{parityBase, over} {
		for i := 0; i < len(src); i += 2 {
			if _, ok := m[src[i]]; !ok {
				order = append(order, src[i])
			}
			m[src[i]] = src[i+1]
		}
	}
	var out []string
	for _, k := range order {
		if m[k] != "\x00" {
			out = append(out, k, m[k])
		}
	}
	return out
}

func TestBG1_ParityPassesWhenTheIdentityKeysAgree(t *testing.T) {
	blue := cm(with()...)
	// Order inside a list, an extra tuning key and a different call are not identity.
	green := cm(with("RECORD_LANES", `["dev","atoms","gate"]`, "EXECUTOR_LANES", `["check"]`, "CI_IDENTITY_V2_LANES", "check,gate", "RECIPE_JOB_LANES", `{"cast":{"service_account":"ci","call":["cast","--x"]}}`)...)
	if state, report := BGParity(colours(blue, green)); state != int(StatePass) {
		t.Fatalf("state %d:\n%s", state, report)
	}
}

func TestBG1_ParityTheGroupDefaultsToTheSameName(t *testing.T) {
	blue := cm(with("DAEDALUS_JOBS_GROUP", "\x00")...)
	green := cm(with("DAEDALUS_JOBS_GROUP", "daedalus-jobs")...)
	if state, report := BGParity(colours(blue, green)); state != int(StatePass) {
		t.Fatalf("unset group is the default name: state %d:\n%s", state, report)
	}
}

func TestBG1_ParityFindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		green []string
		want  string
	}{
		{"a group typo replays the topic", with("DAEDALUS_JOBS_GROUP", "daedalus-job"), "DAEDALUS_JOBS_GROUP: blue \"daedalus-jobs\", green \"daedalus-job\""},
		{"a namespace", with("GATE_JOB_NAMESPACE", "forge2"), "GATE_JOB_NAMESPACE"},
		{"a record lane", with("RECORD_LANES", `["gate","atoms"]`), "RECORD_LANES: blue [atoms dev gate], green [atoms gate]"},
		{"a lane account", with("MUTATION_JOB_SERVICE_ACCOUNT", "ca-mutation"), "MUTATION_JOB_SERVICE_ACCOUNT"},
		{"a lane account only one side has", with("BUILD_JOB_SERVICE_ACCOUNT", "ca-build"), "BUILD_JOB_SERVICE_ACCOUNT: blue unset"},
		{"an identity lane", with("CI_IDENTITY_V2_LANES", "gate"), "CI_IDENTITY_V2_LANES"},
		{"an identity list only green has", with("CI_IDENTITY_V3_LANES", "gate"), "CI_IDENTITY_V3_LANES"},
		{"a recipe lane account", with("RECIPE_JOB_LANES", `{"cast":{"service_account":"ca-cast"}}`), "RECIPE_JOB_LANES[cast].service_account"},
	} {
		state, report := BGParity(colours(cm(with()...), cm(tc.green...)))
		if state != int(StateFindings) || !strings.Contains(report, tc.want) {
			t.Errorf("%s: state %d, report lacks %q:\n%s", tc.name, state, tc.want, report)
		}
	}
}

func TestBG1_ParityCannotRun(t *testing.T) {
	ok := cm(with()...)
	for name, files := range map[string]map[string]string{
		"green is not here":         {BGParityBlueFile: ok},
		"blue is not here":          {BGParityGreenFile: ok},
		"blue is not YAML":          colours("a: [", ok),
		"RECORD_LANES not JSON":     colours(ok, cm(with("RECORD_LANES", "gate,atoms")...)),
		"RECIPE_JOB_LANES not JSON": colours(ok, cm(with("RECIPE_JOB_LANES", "{")...)),
		"no data in green":          colours(ok, "kind: ConfigMap\n"),
	} {
		if state, report := BGParity(files); state != int(StateCannotRun) {
			t.Errorf("%s: state %d, want cannot-run:\n%s", name, state, report)
		}
	}
}

func TestBG1_IsBGParityFile(t *testing.T) {
	for path, want := range map[string]bool{BGParityBlueFile: true, BGParityGreenFile: true, "prime/star-daedalus.yaml": false} {
		if got := IsBGParityFile(path); got != want {
			t.Errorf("IsBGParityFile(%q) = %v", path, got)
		}
	}
}

func TestBG1_ParityDefaultsAndAbsences(t *testing.T) {
	// A group set on blue and left to the default on green is the same name.
	if state, report := BGParity(colours(cm(with()...), cm(with("DAEDALUS_JOBS_GROUP", "\x00")...))); state != int(StatePass) {
		t.Fatalf("green on the default group: state %d:\n%s", state, report)
	}
	// Neither colour has recipe lanes: nothing to compare.
	none := with("RECIPE_JOB_LANES", "\x00")
	if state, report := BGParity(colours(cm(none...), cm(none...))); state != int(StatePass) {
		t.Fatalf("no recipe lanes on either: state %d:\n%s", state, report)
	}
	// The blue colour's recipe lanes unreadable is as unjudged as the green's.
	if state, _ := BGParity(colours(cm(with("RECIPE_JOB_LANES", "{")...), cm(with()...))); state != int(StateCannotRun) {
		t.Fatalf("blue RECIPE_JOB_LANES not JSON: state %d", state)
	}
}
