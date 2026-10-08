package atoms

import (
	"os"
	"path/filepath"
	"testing"
)

const stamped = "_src_path: https://git.notusmi.com/rob/go-repo-template.git\n"

func TestSastRulesetLanes(t *testing.T) {
	const id = "fleet:sast-ruleset-lanes"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no rules at all is absent when nobody stamped it", map[string]string{"main.go": "x"}, 0, absent,
			[]string{"no rules/sast in this tree, and no SAST-shipping fleet template stamped it"}},
		{"a template that ships no ruleset leaves absence the correct state",
			map[string]string{".copier-answers.yml": "_src_path: https://git.notusmi.com/rob/config-repo-template.git\n"}, 0, absent, nil},
		{"a rules directory without sast is absent too", map[string]string{"rules/other.txt": "x"}, 0, absent,
			[]string{"no rules/sast in this tree"}},
		{"a stamped tree without the ruleset never received it", map[string]string{".copier-answers.yml": stamped}, 2, cannot,
			[]string{"go-repo-template stamped it", "NOTHING SCANNED THIS REPO"}},
		{"an answers file pointing at a template that is not ours does not make it stricter",
			map[string]string{".copier-answers.yml": "_src_path: https://x/y/speckit.git\n"}, 0, absent, nil},
		{"a lane the ruleset never names is a scan that did not happen",
			map[string]string{"rules/sast/a.yml": "rules:\n- languages: [python]\n", "go.mod": "module x\n"}, 2, cannot,
			[]string{"rules/sast declares [python] but this repo also builds: go"}},
		{"a commented-out rule is not coverage",
			map[string]string{"rules/sast/a.yml": "# languages: [go]\n", "go.mod": "module x\n"}, 2, cannot, []string{"declares []"}},
		{"every lane declared passes",
			map[string]string{"rules/sast/a.yml": "languages: [\"go\", rust]\n", "go.mod": "module x\n", "Cargo.toml": "x"}, 0, pass,
			[]string{"ruleset declares every lane this repo builds"}},
		{"a ruleset file that will not read is a ruleset not examined",
			map[string]string{"rules/sast/a.yml/inner.txt": "x"}, 2, cannot, []string{"rules/sast/a.yml/ would not read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestCopierAnswersIntact(t *testing.T) {
	const id = "fleet:copier-answers-intact"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no answers file is absent", map[string]string{"a": "x"}, 0, absent, []string{"carries no .copier-answers.yml", "not copier-stamped"}},
		{"a clean answers file passes", map[string]string{".copier-answers.yml": "_commit: v1\n"}, 0, pass, []string{"no crash receipt"}},
		{"the crash receipt is a finding", map[string]string{".copier-answers.yml": "_commit: v1\n#copier updated"}, 1, findings,
			[]string{"#copier updated", "COPIER CRASHED", "FIX: delete the marker line"}},
		{"an answers file that will not read is a file not examined",
			map[string]string{".copier-answers.yml/x": "x"}, 2, cannot, []string{".copier-answers.yml would not read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

const ourea = "ourea internal/config/retired-keys.json"

func oureaMap(keys string) map[string]string {
	return map[string]string{"prime/ourea-config.yaml": "kind: ConfigMap\ndata:\n  config.toml: |\n" + keys}
}

func TestOureaConfigRetiredKeys(t *testing.T) {
	const id = "fleet:ourea-config-retired-keys"
	list := map[string]string{"rob/" + ourea: `{"old_key": "went in #12"}`}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		door    map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"not the flux tree is absent", map[string]string{"main.go": "x"}, list, false, 0, absent, []string{"carries no prime/"}},
		{"prime without the ConfigMap means it moved, which is not absence", map[string]string{"prime/other.yaml": "x"}, list, false, 2, cannot,
			[]string{"is not in it: the door's ConfigMap moved"}},
		{"a ConfigMap that names no retired key passes", oureaMap("    live_key = 1\n"), list, false, 0, pass,
			[]string{"config.toml names none of ourea's 1 retired keys"}},
		{"a retired key in a comment is not a key", oureaMap("    # old_key = 1 was removed\n    live = 1\n"), list, false, 0, pass, nil},
		{"a retired key is a finding", oureaMap("    old_key = 2\n    live_key = 1\n"), list, false, 1, findings,
			[]string{"carries 1 key(s) the door no longer reads", "old_key", "went in #12"}},
		{"a ConfigMap with no door config will not parse", map[string]string{"prime/ourea-config.yaml": "kind: ConfigMap\ndata: {}\n"}, list, false, 2, cannot,
			[]string{"would not parse"}},
		{"a door that does not answer leaves the check without its list", oureaMap("    live_key = 1\n"), nil, true, 2, cannot,
			[]string{"nothing says which keys are retired"}},
		{"a door that answers 404 is not a list", oureaMap("    live_key = 1\n"), map[string]string{}, false, 2, cannot,
			[]string{"HTTP 404"}},
		{"an empty list grades against nothing", oureaMap("    live_key = 1\n"), map[string]string{"rob/" + ourea: `{}`}, false, 2, cannot,
			[]string{"decoded empty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, tc.door)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a prime that will not list", func(t *testing.T) {
		in := treeIn(t, map[string]string{"main.go": "x"})
		// prime is a file, not a directory: it is in the root listing and cannot list.
		put(t, in.Root, "prime", "not a directory")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "prime/ would not enumerate")
	})
	t.Run("a ConfigMap that will not read", func(t *testing.T) {
		in := treeIn(t, map[string]string{"prime/ourea-config.yaml/x": "x"})
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

const ledger = "[retired.old_verb]\nstar = \"x\"\nsuccessor = \"new_verb\"\nretired_by = \"y\"\n"

func TestRetiredVerbs(t *testing.T) {
	const id = "fleet:retired-verbs"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no ledger is absent", map[string]string{"skills/a.md": "old_verb"}, 0, absent, []string{"carries no retired-verbs.toml"}},
		{"units that name no retired verb pass", map[string]string{"retired-verbs.toml": ledger, "skills/a.md": "new_verb only"}, 0, pass,
			[]string{"1 unit(s) name none of the 1 retired verbs"}},
		{"a unit naming a retired verb is a finding with its successor",
			map[string]string{"retired-verbs.toml": ledger, "skills/a.md": "ok\nuse old_verb here\n", "agents/b.toml": "fine"}, 1, findings,
			[]string{"1 line(s) still name a verb", "skills/a.md:2  old_verb", "call instead: new_verb"}},
		{"a name inside a longer identifier is not the verb",
			map[string]string{"retired-verbs.toml": ledger, "skills/a.md": "old_verb_total"}, 0, pass, nil},
		{"a file outside the composition surface is not a unit",
			map[string]string{"retired-verbs.toml": ledger, "docs/a.md": "old_verb", "skills/ok.md": "x"}, 0, pass, nil},
		{"a ledger that is not TOML cannot grade", map[string]string{"retired-verbs.toml": "= not toml", "skills/a.md": "x"}, 2, cannot,
			[]string{"is not a ledger this atom can grade against"}},
		{"a ledger with no unit beside it is a surface that moved", map[string]string{"retired-verbs.toml": ledger, "README.md": "x"}, 2, cannot,
			[]string{"is here and no unit is"}},
		{"a ledger that will not read", map[string]string{"retired-verbs.toml/x": "x"}, 2, cannot, []string{"retired-verbs.toml would not read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a unit that will not read", func(t *testing.T) {
		in := treeIn(t, map[string]string{"retired-verbs.toml": ledger, "skills/ok.md": "x"})
		if err := os.Symlink(filepath.Join(in.Root, "nowhere"), filepath.Join(in.Root, "skills", "broken.md")); err != nil {
			t.Fatal(err)
		}
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "skills/broken.md would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}
