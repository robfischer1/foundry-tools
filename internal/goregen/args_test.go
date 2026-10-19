package goregen

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseArgsReadsExistingCoresAndNewOnes(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want Request
	}{
		"nothing is an empty request": {nil, Request{}},
		"existing cores in order":     {[]string{"alarm", "converge", "classescore"}, Request{Existing: []string{"alarm", "converge", "classescore"}}},
		"a new core, flags spaced": {
			[]string{"--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"},
			Request{New: []NewCore{{Core: "sqlport", Rev: "v0.78.0", World: "aiws:sqlport/sqlport-core@0.2.0"}}},
		},
		"a new core, flags joined and reordered": {
			[]string{"--new=kube", "--world=aiws:kube/kube-core@0.1.0", "--rev=" + c40},
			Request{New: []NewCore{{Core: "kube", Rev: c40, World: "aiws:kube/kube-core@0.1.0"}}},
		},
		"repeatable and mixed with existing": {
			[]string{"alarm", "--new", "mode", "--rev", "v1", "--world", "aiws:mode/mode-core@0.1.0", "converge",
				"--new", "producer", "--rev", "v1", "--world", "aiws:producer/producer-core@0.1.0"},
			Request{Existing: []string{"alarm", "converge"}, New: []NewCore{
				{Core: "mode", Rev: "v1", World: "aiws:mode/mode-core@0.1.0"},
				{Core: "producer", Rev: "v1", World: "aiws:producer/producer-core@0.1.0"}}},
		},
	} {
		got, err := ParseArgs(tc.args)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, tc.want)
		}
	}
}

func TestParseArgsRefusesWhatItCannotRead(t *testing.T) {
	const world = "aiws:mode/mode-core@0.1.0"
	for name, tc := range map[string]struct {
		args []string
		says string
	}{
		"an unknown flag":             {[]string{"--fast", "alarm"}, "unknown option --fast"},
		"a flag with no value":        {[]string{"--new"}, "--new needs a value"},
		"a value that is a flag":      {[]string{"--new", "--rev"}, "--new needs a value"},
		"a rev before any new":        {[]string{"--rev", "v1"}, "--rev names no core"},
		"a world before any new":      {[]string{"--world", world}, "--world names no core"},
		"a rev twice":                 {[]string{"--new", "mode", "--rev", "v1", "--rev", "v2"}, "--rev given twice for mode"},
		"a world twice":               {[]string{"--new", "mode", "--world", world, "--world", world}, "--world given twice for mode"},
		"a new core with no rev":      {[]string{"--new", "mode", "--world", world}, "--new mode needs --rev"},
		"a new core with no world":    {[]string{"--new", "mode", "--rev", "v1"}, "--new mode needs --world"},
		"a core that is a path":       {[]string{"../alarm"}, "is not a core directory name"},
		"a core with a slash":         {[]string{"a/b"}, "is not a core directory name"},
		"an uppercase core":           {[]string{"Alarm"}, "is not a core directory name"},
		"a new core that is a path":   {[]string{"--new", "a/b", "--rev", "v1", "--world", world}, "is not a core directory name"},
		"a core named twice":          {[]string{"alarm", "alarm"}, "alarm is named twice"},
		"a new core that exists":      {[]string{"alarm", "--new", "alarm", "--rev", "v1", "--world", world}, "alarm is named twice"},
		"a rev that is an option":     {[]string{"--new", "mode", "--rev", "-x", "--world", world}, "--rev needs a value"},
		"a rev with dots":             {[]string{"--new", "mode", "--rev", "a..b", "--world", world}, "is not a tag or a commit"},
		"a world that is not a world": {[]string{"--new", "mode", "--rev", "v1", "--world", "mode"}, "is not aiws:<package>/<world>@<version>"},
	} {
		_, err := ParseArgs(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v, want %q", name, err, tc.says)
		}
	}
}

func TestParseWorldSplitsPackageWorldAndVersion(t *testing.T) {
	w, err := ParseWorld("aiws:topic-contract/topic-contract-core@0.1.0")
	if err != nil || w != (World{Pkg: "topic-contract", Name: "topic-contract-core", Version: "0.1.0"}) {
		t.Errorf("%+v %v", w, err)
	}
	for _, bad := range []string{"", "aiws:x", "aiws:x/y", "aiws:x/y@1", "x:y/z@1.0.0", "aiws:X/y@1.0.0", "aiws:x/y@1.0.0-rc1"} {
		if _, err := ParseWorld(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

const c40 = "0123456789abcdef0123456789abcdef01234567"
