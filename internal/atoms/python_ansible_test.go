package atoms

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ansibleFiles is a tree with one playbook and the config beside it.
func ansibleFiles() map[string]string {
	return map[string]string{
		"ansible/playbooks/site.yml": "- hosts: all\n",
		"ansible/ansible.cfg":        "[defaults]\n",
		"flux/x.yaml":                "a: 1\n",
	}
}

// provision lays a collection the way galaxy installs one, and points the atom
// at the directory.
func provision(t *testing.T, collections map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, version := range collections {
		namespace, collection, _ := strings.Cut(name, ".")
		put(t, dir, filepath.Join("ansible_collections", namespace, collection, "MANIFEST.json"), `{"collection_info": {"version": "`+version+`"}}`)
	}
	old := ansibleCollectionsDir
	ansibleCollectionsDir = dir
	t.Cleanup(func() { ansibleCollectionsDir = old })
}

// ansibleAnswer answers the probes, the private tree's repository, and each tool
// from a script keyed by its name.
func ansibleAnswer(tool func(c Cmd) (string, int)) func(Cmd) (string, int) {
	return func(c Cmd) (string, int) {
		switch {
		case c.Name == "ansible-playbook" && c.Args[0] == "--version", c.Name == "python3", c.Name == "git":
			return "ok", 0
		}
		return tool(c)
	}
}

func TestOpsAnsible(t *testing.T) {
	const id = "ops:ansible"
	t.Run("the playbooks are syntax-checked, then linted at min, then counted at basic", func(t *testing.T) {
		in, f := toolTree(t, ansibleFiles(), ansibleAnswer(func(c Cmd) (string, int) {
			if c.Name == "ansible-lint" && c.Args[3] == "basic" {
				return "yaml[truthy]: x\nname[play]: y\nnoise\n", 2
			}
			return "done", 0
		}))
		expect(t, runAtom(t, id, in), stateOf(0), pass,
			"syntax-check playbooks/site.yml", "ansible-lint --profile min (gates)", "ansible-lint --profile basic: 2 finding(s) — reported, not gating")
		if got := strings.Join(f.ran(), " "); got != "ansible-playbook python3 git git ansible-playbook ansible-lint ansible-lint" {
			t.Fatalf("ran %s", got)
		}
		// All three ansible calls run in the private tree's ansible/, never the shared one.
		syntax, min, basic := f.calls[4], f.calls[5], f.calls[6]
		if flagged(syntax) != "--syntax-check playbooks/site.yml" || flagged(min) != "--offline -q --profile min ." || flagged(basic) != "--offline -q --profile basic ." {
			t.Errorf("argv: %q / %q / %q", flagged(syntax), flagged(min), flagged(basic))
		}
		wd := syntax.Dir
		if wd == filepath.Join(in.Root, "ansible") || filepath.Base(wd) != "ansible" || min.Dir != wd || basic.Dir != wd {
			t.Errorf("the tools ran in %q, %q, %q: want one private ansible/ directory", syntax.Dir, min.Dir, basic.Dir)
		}
		private := filepath.Dir(wd)
		if strings.HasPrefix(private, in.Root) {
			t.Errorf("the private tree %s is inside the shared one", private)
		}
		if !syntax.Both || !min.Both || basic.Both || !basic.StdoutOnly {
			t.Errorf("streams: syntax %v min %v basic both=%v stdout-only=%v: the debt is counted off stdout alone", syntax.Both, min.Both, basic.Both, basic.StdoutOnly)
		}
		for _, c := range f.calls[4:] {
			env := strings.Join(c.Env, " ")
			for _, want := range []string{"PYTHONDONTWRITEBYTECODE=1", "ANSIBLE_HOME=" + private, "XDG_CACHE_HOME=" + private} {
				if !strings.Contains(env, want) {
					t.Errorf("%s ran with %v, lacking %s", c.Name, c.Env, want)
				}
			}
			if strings.Contains(env, "ANSIBLE_COLLECTIONS_PATH") {
				t.Errorf("a tree that declares no collections was handed a collections path: %v", c.Env)
			}
		}
		if _, err := os.Stat(private); err == nil {
			t.Errorf("the private tree %s was left behind", private)
		}
	})
	t.Run("the private tree holds every committable file, as a repository", func(t *testing.T) {
		files := ansibleFiles()
		files["ansible/roles/x/tasks/main.yml"] = "- debug: {}\n"
		files["README.md"] = "outside ansible/\n"
		var seen []string
		in, f := toolTree(t, files, ansibleAnswer(func(c Cmd) (string, int) {
			if c.Name == "ansible-playbook" {
				seen = listTree(t, filepath.Dir(c.Dir))
			}
			return "", 0
		}))
		expect(t, runAtom(t, id, in), stateOf(0), pass)
		want := []string{"README.md", "ansible/ansible.cfg", "ansible/playbooks/site.yml", "ansible/roles/x/tasks/main.yml", "flux/x.yaml"}
		if !reflect.DeepEqual(seen, want) {
			t.Errorf("the private tree held %v, want %v", seen, want)
		}
		for _, g := range f.calls[2:4] {
			if g.Name != "git" || !slices.Contains(g.Env, "GIT_CONFIG_NOSYSTEM=1") {
				t.Errorf("the repository was not made off the developer's configuration: %+v", g)
			}
		}
		if flagged(f.calls[2]) != "init -q ." || flagged(f.calls[3]) != "add -A" {
			t.Errorf("git ran as %q then %q", flagged(f.calls[2]), flagged(f.calls[3]))
		}
	})
	t.Run("a tool that writes beside what it reads leaves the shared tree as it was", func(t *testing.T) {
		in, _ := toolTree(t, ansibleFiles(), ansibleAnswer(func(c Cmd) (string, int) {
			put(t, c.Dir, ".ansible/cache/x", "a cache")
			put(t, c.Dir, "playbooks/site.retry", "a retry file")
			put(t, c.Dir, "ansible.cfg", "overwritten")
			return "", 0
		}))
		before := listTree(t, in.Root)
		expect(t, runAtom(t, id, in), stateOf(0), pass)
		if after := listTree(t, in.Root); !reflect.DeepEqual(after, before) {
			t.Errorf("the shared tree changed: before %v, after %v", before, after)
		}
		if body, _ := os.ReadFile(filepath.Join(in.Root, "ansible/ansible.cfg")); string(body) != "[defaults]\n" {
			t.Errorf("a tracked file was overwritten through the run: %q", body)
		}
	})
	t.Run("the inventory a tree tracks is passed to the syntax check", func(t *testing.T) {
		files := ansibleFiles()
		files["ansible/inventory/hosts.yml"] = "all: {}\n"
		in, f := toolTree(t, files, ansibleAnswer(func(Cmd) (string, int) { return "", 0 }))
		expect(t, runAtom(t, id, in), stateOf(0), pass)
		if got := flagged(f.calls[4]); got != "--syntax-check -i inventory/hosts.yml playbooks/site.yml" {
			t.Errorf("the syntax check ran as %q", got)
		}
	})
	t.Run("every playbook is checked, in order, and the first to fail ends it", func(t *testing.T) {
		files := ansibleFiles()
		files["ansible/playbooks/a.yml"] = "- hosts: all\n"
		files["ansible/playbooks/b.yml"] = "- hosts: all\n"
		in, f := toolTree(t, files, ansibleAnswer(func(c Cmd) (string, int) {
			if c.Name == "ansible-playbook" && c.Args[len(c.Args)-1] == "playbooks/b.yml" {
				return "ERROR! the field 'hosts' is required", 4
			}
			return "", 0
		}))
		expect(t, runAtom(t, id, in), stateOf(1), findings,
			"syntax-check playbooks/a.yml", "syntax-check playbooks/b.yml", "the field 'hosts' is required", "ansible failed (rc=4) — findings")
		for _, c := range f.calls {
			if c.Name == "ansible-lint" || (c.Name == "ansible-playbook" && c.Args[len(c.Args)-1] == "playbooks/site.yml") {
				t.Errorf("%s ran after the failing playbook", c.Name)
			}
		}
	})
	for _, tc := range []struct {
		name    string
		tool    string
		profile string
		out     string
		code    int
		state   int
		result  string
		needles []string
	}{
		{"a syntax check that hit the network is a 2", "ansible-playbook", "", "Could not resolve host: galaxy.ansible.com", 1, 2, cannot, []string{"failed on a fault of the substrate"}},
		{"a lint finding is a finding", "ansible-lint", "min", "site.yml:1: syntax-check[x]", 2, 1, findings, []string{"ansible-lint --profile min (gates)", "site.yml:1: syntax-check[x]", "ansible failed (rc=2) — findings"}},
		{"a syntax check that would not start never ran", "ansible-playbook", "", "ansible-playbook: gone", -1, 2, cannot, []string{"the atom never ran: ansible-playbook: gone"}},
		{"a lint that would not start never ran", "ansible-lint", "min", "ansible-lint: gone", -1, 2, cannot, []string{"the atom never ran: ansible-lint: gone"}},
		{"a debt count that would not start never ran", "ansible-lint", "basic", "ansible-lint: gone", -1, 2, cannot, []string{"the atom never ran: ansible-lint: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := toolTree(t, ansibleFiles(), ansibleAnswer(func(c Cmd) (string, int) {
				if c.Name == tc.tool && (tc.profile == "" || c.Args[3] == tc.profile) {
					return tc.out, tc.code
				}
				return "", 0
			}))
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a lint that fails at basic alone is debt: the exit is the gating profile's, the stdout the count's", func(t *testing.T) {
		in, _ := toolTree(t, ansibleFiles(), ansibleAnswer(func(c Cmd) (string, int) {
			if c.Name == "ansible-lint" && c.Args[3] == "basic" {
				return "var-naming[no-role-prefix]: a\n", 2
			}
			return "", 0
		}))
		expect(t, runAtom(t, id, in), stateOf(0), pass, "ansible-lint --profile basic: 1 finding(s) — reported, not gating")
	})

	t.Run("the collections a tree declares, at the versions the container carries, are on the tools' path", func(t *testing.T) {
		provision(t, map[string]string{"community.routeros": "3.22.0", "vyos.vyos": "6.0.0", "ansible.utils": "6.1.0"})
		files := ansibleFiles()
		files["ansible/requirements.yml"] = "collections:\n  - name: community.routeros\n    version: \"3.22.0\"\n  - name: vyos.vyos\n    version: 6.0.0\n  - ansible.utils\n  - name: vyos.vyos\n    version: \"*\"\n"
		in, f := toolTree(t, files, ansibleAnswer(func(Cmd) (string, int) { return "", 0 }))
		expect(t, runAtom(t, id, in), stateOf(0), pass)
		for _, c := range f.calls[4:] {
			if !slices.Contains(c.Env, "ANSIBLE_COLLECTIONS_PATH="+ansibleCollectionsDir) {
				t.Errorf("%s was not told where the collections are: %v", c.Name, c.Env)
			}
		}
	})
	for _, tc := range []struct {
		name         string
		provisioned  map[string]string
		requirements string
		needles      []string
	}{
		{"a collection the container does not carry", map[string]string{"vyos.vyos": "6.0.0"},
			"collections:\n  - name: community.routeros\n    version: \"3.22.0\"\n", []string{"community.routeros 3.22.0 (the container carries none)"}},
		{"a collection at another version", map[string]string{"vyos.vyos": "5.0.0"},
			"collections:\n  - name: vyos.vyos\n    version: \"6.0.0\"\n", []string{"vyos.vyos 6.0.0 (the container carries 5.0.0)"}},
		{"a range the container cannot be held to", map[string]string{"vyos.vyos": "6.0.0"},
			"collections:\n  - name: vyos.vyos\n    version: \">=5.0.0\"\n", []string{"vyos.vyos >=5.0.0 (a range; the container carries 6.0.0)"}},
		{"several, all named", map[string]string{},
			"collections:\n  - name: a.b\n    version: \"1\"\n  - c.d\n", []string{"a.b 1 (the container carries none); c.d  (the container carries none)"}},
	} {
		t.Run(tc.name+" is a could-not-run that names the difference and runs nothing", func(t *testing.T) {
			provision(t, tc.provisioned)
			files := ansibleFiles()
			files["ansible/requirements.yml"] = tc.requirements
			in, f := toolTree(t, files, ansibleAnswer(func(Cmd) (string, int) { return "", 0 }))
			expect(t, runAtom(t, id, in), stateOf(2), cannot,
				append(tc.needles, "the tools container does not carry what ansible/requirements.yml declares", "ansible: declares collections that would not install — did not look")...)
			if len(f.calls) != 2 {
				t.Errorf("the tools ran with a collection missing: %v", f.ran())
			}
		})
	}
	t.Run("a requirements file that does not parse", func(t *testing.T) {
		files := ansibleFiles()
		files["ansible/requirements.yml"] = "collections: [\n"
		in, _ := toolTree(t, files, ansibleAnswer(func(Cmd) (string, int) { return "", 0 }))
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "ansible/requirements.yml does not parse", "did not look")
	})
	t.Run("a requirements file that would not read", func(t *testing.T) {
		in, _ := toolTree(t, ansibleFiles(), ansibleAnswer(func(Cmd) (string, int) { return "", 0 }))
		in.Committable = append(in.Committable, "ansible/requirements.yml")
		v := runAtom(t, id, in)
		expect(t, v, stateOf(2), cannot, "the atom never ran", "ansible/requirements.yml")
		// It is the read that failed, not the copy of the tree that came after it.
		if strings.Contains(v.Reason, "private tree") {
			t.Errorf("the declaration was read after the tree was copied: %s", v.Reason)
		}
	})

	t.Run("a private tree that cannot be made is a 2 and no tool ran", func(t *testing.T) {
		in, f := toolTree(t, ansibleFiles(), func(c Cmd) (string, int) {
			if c.Name == "git" {
				return "fatal: no", 128
			}
			return "ok", 0
		})
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the private tree ansible runs in could not be made", "git init -q . exited 128")
		for _, c := range f.calls {
			if c.Name == "ansible-lint" {
				t.Error("lint ran without its tree")
			}
		}
	})
	for tool, probe := range map[string][]string{"ansible-playbook": {"--version"}, "python3": {"-c", "import ansiblelint"}} {
		t.Run("a container whose "+tool+" fails its probe is unprovisioned", func(t *testing.T) {
			in, f := toolTree(t, ansibleFiles(), func(c Cmd) (string, int) {
				if c.Name == tool {
					return "gone", 127
				}
				return "ok", 0
			})
			expect(t, runAtom(t, id, in), stateOf(2), cannot, "the phase's tool could not be provisioned", tool+" "+strings.Join(probe, " ")+" exited 127")
			for _, c := range f.calls {
				if c.Name == "git" || c.Name == "ansible-lint" {
					t.Errorf("%s ran after a failed probe", c.Name)
				}
			}
		})
	}
	t.Run("no playbook is absent, decided before any tool is touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "ansible/roles/x/tasks/main.yml": "- debug: {}\n"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "no ansible/playbooks in this tree")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a tree that is not an ops tree is absent", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "has no ops shape")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
}

func TestDeclaredCollections(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []collectionRef
		err  string
	}{
		{"names and versions", "collections:\n  - name: a.b\n    version: \"1.2.3\"\n  - name: c.d\n", []collectionRef{{"a.b", "1.2.3"}, {"c.d", ""}}, ""},
		{"a bare name", "collections:\n  - a.b\n", []collectionRef{{"a.b", ""}}, ""},
		{"roles are not read", "roles:\n  - name: x\ncollections: []\n", nil, ""},
		{"an empty file", "", nil, ""},
		{"an entry with no name", "collections:\n  - version: \"1\"\n", nil, "names none"},
		{"an entry that is neither", "collections:\n  - [a, b]\n", nil, "neither a name nor a mapping"},
		{"yaml that does not parse", "collections: [\n", nil, "yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := declaredCollections(tc.yaml)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error %v, want one containing %q", err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestProvisionedVersion(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "ansible_collections/a/b/MANIFEST.json", `{"collection_info": {"version": "1.2.3"}}`)
	put(t, dir, "ansible_collections/a/bad/MANIFEST.json", `not json`)
	for name, want := range map[string]string{"a.b": "1.2.3", "a.bad": "", "a.missing": "", "nodot": "", "": ""} {
		if got := provisionedVersion(dir, name); got != want {
			t.Errorf("provisionedVersion(%q) = %q, want %q", name, got, want)
		}
	}
}
