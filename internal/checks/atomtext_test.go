package checks

import (
	"reflect"
	"strings"
	"testing"
)

func TestCopierTemplate(t *testing.T) {
	for _, tc := range []struct{ name, answers, want string }{
		{"empty", "", ""},
		{"a fleet template", "_commit: v1\n_src_path: https://git.notusmi.com/rob/go-repo-template.git\n", "go-repo-template"},
		{"quoted, with no .git", "_src_path: 'git@h:rob/rust-repo-template'\n", "rust-repo-template"},
		{"double-quoted", `_src_path: "https://h/x/frontend-repo-template.git"` + "\n", "frontend-repo-template"},
		{"a template that ships no ruleset", "_src_path: https://h/x/config-repo-template.git\n", ""},
		{"speckit's is not ours", "_src_path: https://h/x/speckit.git\n", ""},
		{"the first _src_path decides", "_src_path: https://h/x/other.git\n_src_path: https://h/x/go-repo-template.git\n", ""},
		{"indented key is read", "  _src_path: https://h/x/go-repo-template.git\n", "go-repo-template"},
		{"no key", "name: x\n", ""},
	} {
		if got := CopierTemplate(tc.answers); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCopierAndSastWords(t *testing.T) {
	if got := CopierMarkerReport(); !strings.HasPrefix(got, `.copier-answers.yml carries renovate's "#copier updated" marker.`) || !strings.Contains(got, "FIX: delete the marker line") {
		t.Errorf("marker report: %s", got)
	}
	got := SastAbsentStamped("fleet:sast-ruleset-lanes", "go-repo-template")
	for _, want := range []string{"fleet:sast-ruleset-lanes: CANNOT RUN - this tree has no rules/sast, and go-repo-template stamped it.", "FIX: copy go-repo-template's template/rules/sast/dataflow.yml"} {
		if !strings.Contains(got, want) {
			t.Errorf("stamped-absent report lacks %q:\n%s", want, got)
		}
	}
}

func TestOureaConfigKeys(t *testing.T) {
	cm := func(body string) string { return "data:\n  config.toml: |\n" + body }
	for _, tc := range []struct {
		name    string
		in      string
		keys    []string
		wantErr string
	}{
		{"top-level keys", cm("    a = 1\n    b = \"x\"\n"), []string{"a", "b"}, ""},
		{"a table is one key, not its members", cm("    [hooks]\n    pin_hook_url = 1\n"), []string{"hooks"}, ""},
		{"a commented key is not set", cm("    # old = 1\n    a = 1\n"), []string{"a"}, ""},
		{"not YAML", "data: [", nil, "the ConfigMap is not YAML"},
		{"no config.toml", "data: {}\n", nil, `it carries no data["config.toml"]`},
		{"not TOML", cm("    = nope\n"), nil, `data["config.toml"] is not TOML`},
	} {
		got, err := OureaConfigKeys(tc.in)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: error %v, want %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		var keys []string
		for k := range got {
			keys = append(keys, k)
		}
		if err != nil || len(keys) != len(tc.keys) {
			t.Errorf("%s: %v, %v; want %v", tc.name, keys, err, tc.keys)
		}
	}
}

func TestOureaRetired(t *testing.T) {
	list, err := OureaRetiredList(`{"old": "went in #1", "older": "went in #0"}`)
	if err != nil || len(list) != 2 {
		t.Fatalf("list %v, %v", list, err)
	}
	for _, bad := range []string{`[`, `{}`, `{"a": 1}`} {
		if _, err := OureaRetiredList(bad); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
	named := OureaRetiredNamed(map[string]any{"older": 1, "live": 2, "old": 3}, list)
	if !reflect.DeepEqual(named, []string{"old", "older"}) {
		t.Errorf("named %v: want the retired keys the ConfigMap sets, sorted", named)
	}
	rep := OureaRetiredReport(named, list)
	for _, want := range []string{"prime/ourea-config.yaml carries 2 key(s) the door no longer reads.", "  old\n    went in #1\n", "internal/config/retired-keys.json at main"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
	if OureaConfigPath != OureaConfigDir+"/"+OureaConfigFile {
		t.Errorf("config path %q", OureaConfigPath)
	}
}
