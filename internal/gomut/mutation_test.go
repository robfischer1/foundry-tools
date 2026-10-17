package gomut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate measures the pull's diff in Go: the knobs on the argv, the canary,
// the bound through the environment, the fleet's exclusions and the base — no
// config file, no script, no shell, no foundry-stocks mount.
//
// -cache=off ON THE CONTROLS IS LOAD-BEARING. gomutants' incremental cache skips
// a mutant whose package and covering tests are byte-identical to a cached run,
// and the control fixtures ARE byte-identical on every run of every repo — a
// cached control answers from the last verdict instead of re-taking it, which is
// a control that stopped controlling. The measured run keeps the cache, where
// re-using an unchanged package's verdict is the point.
func TestGoMutationMeasuresTheDiffAndSettlesInGo(t *testing.T) {
	p := newPull(t, ".")
	v := p.run()
	if v.State != 0 {
		t.Fatalf("%+v", v)
	}
	c, ok := p.call("-changed-since")
	if !ok {
		t.Fatalf("gomutants never ran: %v", p.runs)
	}
	report := filepath.Join(p.work, "go", "root", goMutationReport)
	cache := filepath.Join(p.work, "go", "root", goMutationCacheName)
	want := []string{"-output", report, "-config", "/dev/null", "-workers", "4", "-disable", goMutationDisable,
		"-exclude-files", goMutationExclude, "-cache", cache, "-changed-since", sinceSha, "."}
	if strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("gomutants argv:\n%q\nwant\n%q", c.Args, want)
	}
	if c.Dir != p.in.Root || !c.Both {
		t.Errorf("gomutants runs in the module and reads both streams: %+v", c)
	}
	env := strings.Join(c.Env, "\n")
	// gomutants' 2 GiB cap reads `ps -g`, which procps answers by session: the
	// atom's own binary, linked as ps, is the ps this exec finds first.
	link := filepath.Join(p.work, "bin", "ps")
	if target, err := os.Readlink(link); err != nil || target == "" {
		t.Errorf("ps is not linked: %v", err)
	}
	for _, need := range []string{
		"PATH=" + filepath.Join(p.work, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GOMAXPROCS=1",
		// -count=1: the runner's own baseline is what every mutant's timeout is
		// derived from, and a CACHED baseline is ~1s for a module that tests in
		// minutes (ourea aeb9cd9 under gremlins: killed 72, TIMED OUT 79).
		"GOFLAGS=-p=1 -count=1",
		"GIT_CONFIG_KEY_0=diff.relative", "GIT_CONFIG_VALUE_0=true", "GIT_CONFIG_KEY_1=diff.context", "GIT_CONFIG_VALUE_1=0", "GIT_CONFIG_COUNT=2",
	} {
		if !strings.Contains(env, need) {
			t.Errorf("gomutants' environment lacks %s:\n%s", need, env)
		}
	}
	// the change set is asked first, in the module, against the merge base; the
	// download follows, and only a run that will mutate gets it
	order := []string{"rev-parse --verify --quiet abc123^{commit}", "merge-base abc123 HEAD", "diff --relative " + sinceSha + " --name-only -- *.go", "mod download", "ls-tree"}
	at := -1
	for _, o := range order {
		i := -1
		for j, r := range p.runs {
			if j > at && strings.Contains(r, o) {
				i = j
				break
			}
		}
		if i < 0 {
			t.Fatalf("%q was not asked after run %d: %v", o, at, p.runs)
		}
		at = i
	}
	// no config file is written or fetched, no shell is run
	for _, r := range p.runs {
		if strings.Contains(r, "sh -c") || strings.Contains(r, "bash") || strings.Contains(r, "gremlins.yaml") {
			t.Errorf("the gate ran %q", r)
		}
	}
	// THE CONTROLS ARE MODULES OF THEIR OWN, run with the cache off.
	for _, ctl := range []struct {
		dir   string
		files []string
	}{
		{"canary", []string{"go.mod", "canary.go", "canary_test.go"}},
		// THE SECOND CONTROL'S SHAPE IS THE TEST. A main package AT the module root
		// is graded correctly by the gremlins this works around, so a control
		// written that way proves nothing; the main package must sit in a
		// subdirectory with NOTHING at the root.
		{"maincanary", []string{"go.mod", "cmd/tool/main.go", "cmd/tool/main_test.go"}},
	} {
		dir := filepath.Join(p.work, "go", "root", ctl.dir)
		for _, f := range ctl.files {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("control %s lacks %s", ctl.dir, f)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
			t.Errorf("the package-main control grew a root package, which this gremlins grades correctly")
		}
		var ran bool
		for _, c := range p.cmds {
			if c.Dir == dir && c.Name == "gomutants" {
				ran = true
				want := []string{"-config", "/dev/null", "-workers", "1", "-cache=off", "-disable", goMutationDisable, "-output", filepath.Join(dir, goMutationReport), "./..."}
				if strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
					t.Errorf("control %s argv:\n%q\nwant\n%q", ctl.dir, c.Args, want)
				}
			}
		}
		if !ran {
			t.Errorf("control %s never ran gomutants", ctl.dir)
		}
	}
	// And the set the second control governs is read from the toolchain, in the
	// module — `go list`, not a grep over package clauses.
	if c, ok := p.call("-f {{if eq .Name"); !ok || c.Dir != p.in.Root || strings.Contains(strings.Join(c.Args, " "), "-tags") {
		t.Errorf("the misgraded-file listing: %+v", c)
	}
}

// A pull with nothing to scope to stands down clean, and never mutates the
// whole module; what the gate cannot evaluate is could-not-run.
func TestGoMutationStandsDownOrCannotRun(t *testing.T) {
	const baseNeedle = "rev-parse --verify --quiet abc123^{commit}"
	for _, tc := range []struct {
		name   string
		base   string
		script func(p *pull)
		state  int
		// reason is checked off a could-not-run only: a pass keeps no output
		// (checks.VerdictOf), so a stand-down is told apart by what it ran.
		reason  string
		reached []string
		never   []string
	}{
		{"no base", "-", nil, 0, "", nil, []string{baseNeedle, "diff --relative"}},
		{"a base the history lacks", "abc123", func(p *pull) { p.first(rule{baseNeedle, "", 1, ""}) }, 0, "",
			[]string{baseNeedle}, []string{"merge-base", "diff --relative"}},
		{"no merge base", "abc123", func(p *pull) { p.first(rule{"merge-base", "", 1, ""}) }, 2,
			"no merge base between the base abc123 and HEAD (exit 1)", []string{"merge-base"}, []string{"diff --relative"}},
		{"no Go changed", "abc123", func(p *pull) { p.first(rule{"diff --relative", "", 0, ""}) }, 0, "",
			[]string{"diff --relative"}, []string{"gomutants", "mod download"}},
		{"git cannot diff", "abc123", func(p *pull) { p.first(rule{"diff --relative", "bad object", 1, ""}) }, 2,
			"git could not diff the pull against its base since0: bad object", []string{"diff --relative"}, []string{"gomutants"}},
		{"the base check never ran", "abc123", func(p *pull) { p.first(rule{baseNeedle, "git: not found", -1, ""}) }, 2,
			"the atom never ran: git: not found", nil, []string{"merge-base"}},
		{"the diff never ran", "abc123", func(p *pull) { p.first(rule{"diff --relative", "git: not found", -1, ""}) }, 2,
			"the atom never ran: git: not found", nil, []string{"gomutants"}},
		{"the diff was killed", "abc123", func(p *pull) { p.first(rule{"diff --relative", "Killed", 137, ""}) }, 2,
			"the atom never ran: exit code: 137\nfailed: git diff --relative since0 --name-only -- *.go\nKilled", nil, []string{"gomutants"}},
		{"the download fails", "abc123", func(p *pull) { p.first(rule{"mod download", "proxy.golang.org: 502 Bad Gateway", 1, ""}) }, 2,
			"CANNOT RUN - preflight `go mod download` exited 1: proxy.golang.org: 502 Bad Gateway", nil, []string{"gomutants"}},
		{"gomutants never ran", "abc123", func(p *pull) { p.first(rule{"-changed-since", "gomutants: not found", -1, ""}) }, 2,
			"the atom never ran: gomutants: not found", nil, nil},
		{"gomutants was killed", "abc123", func(p *pull) { p.first(rule{"-changed-since", "tail of the log", 137, ""}) }, 2,
			"the atom never ran: exit code: 137\nfailed: gomutants", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			if tc.base != "-" {
				p.in.Base = tc.base
			} else {
				p.in.Base = ""
			}
			if tc.script != nil {
				tc.script(p)
			}
			v := p.run()
			if v.State != tc.state {
				t.Fatalf("state %d (%s), want %d:\n%s", v.State, v.Result, tc.state, v.Reason)
			}
			if tc.reason != "" && !strings.Contains(said(v), id+": "+tc.reason) && !strings.Contains(said(v), tc.reason) {
				t.Errorf("the verdict lacks %q:\n%s", tc.reason, said(v))
			}
			if tc.state == 0 && p.ran("gomutants") {
				t.Errorf("a stand-down ran gomutants")
			}
			for _, n := range tc.reached {
				if !p.ran(n) {
					t.Errorf("never reached %s", n)
				}
			}
			for _, n := range tc.never {
				if p.ran(n) {
					t.Errorf("went on to %s: %v", n, p.runs)
				}
			}
		})
	}
}

// A base the door named and the history lacks is LOUD on the door's own tree:
// never the clean "no usable PR base" stand-down, which would pass a pull having
// mutated nothing. The same miss on a local snapshot stands down 0.
func TestGoMutationFailsLoudOnAFetchedTreeMissingItsBase(t *testing.T) {
	p := newPull(t, ".")
	p.first(rule{"rev-parse --verify", "", 1, ""})
	t.Setenv("ATOM_ORIGIN", "https://door/x")
	v := p.run()
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN - the base abc123 is not in this history, though the door named it and it was fetched") {
		t.Fatalf("%+v", v)
	}
	if p.ran("merge-base") {
		t.Error("went on to the merge base after the base was not found")
	}
	t.Setenv("ATOM_ORIGIN", "")
	q := newPull(t, ".")
	q.first(rule{"rev-parse --verify", "", 1, ""})
	if v := q.run(); v.State != 0 {
		t.Errorf("a local snapshot stands down clean: %+v", v)
	}
}

// THE SERVERS' SCOPE LINE RIDES EVERY VERDICT, an empty change set's included.
func TestTheTestServerScopeLinesSayWhyOnEveryBranch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script func(p *pull)
		want   string
	}{
		{"a tagged suite", func(p *pull) { p.first(rule{"grep -rhoE", "//go:build live_db\n", 0, ""}) }, "live_db → TEST_DATABASE_URL"},
		{"no tagged suite", nil, "test databases: none"},
		{"a tag read that failed", func(p *pull) { p.first(rule{"grep -rhoE", "grep: boom", 2, ""}) }, "the tree's build tags could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			if tc.script != nil {
				tc.script(p)
			}
			v := p.run()
			if v.State != 0 || !strings.Contains(said(v), tc.want) {
				t.Errorf("the scope line lacks %q:\n%+v", tc.want, v)
			}
			// the same on a pull that mutates nothing
			q := newPull(t, ".")
			if tc.script != nil {
				tc.script(q)
			}
			q.first(rule{"diff --relative", "", 0, ""})
			if v := q.run(); !strings.Contains(said(v), tc.want) {
				t.Errorf("an empty change set drops the scope line %q:\n%+v", tc.want, v)
			}
		})
	}
}

// THE RECORD'S DATABASE TAGS ARE COMPILED, in every listing the run asks: the
// mutation run, the misgraded-file listing (a build-tagged main file is placed by
// the tags in force), the package listing the report's base comes from, and the
// source-package listing the scope is checked against.
func TestGoMutationCompilesTheTreesDBTags(t *testing.T) {
	p := newPull(t, ".")
	p.first(rule{"grep -rhoE", "//go:build live_db\n", 0, ""})
	p.report = survivors // a report, so the package listing the base comes from is asked
	if v := p.run(); v.State != 1 {
		t.Fatalf("%+v", v)
	}
	for _, needle := range []string{"-f {{if eq .Name", "-f {{.ImportPath}}", "-f {{if or .GoFiles .CgoFiles}}", "-deps -test"} {
		c, ok := p.call(needle)
		if !ok || !strings.Contains(strings.Join(c.Args, " "), "-tags live_db") {
			t.Errorf("the listing %q dropped the record's build tags: %+v", needle, c)
		}
	}
	c, _ := p.call("-changed-since")
	if !strings.Contains(strings.Join(c.Args, " "), "-changed-since "+sinceSha+" -tags live_db .") {
		t.Errorf("THE MUTATION RUN COMPILES THE TAG, after -changed-since and before the package: %q", c.Args)
	}
	// NO TAGGED SUITE, NO TAGS AT ALL — not an empty one: an empty -tags word is
	// still `-tags ""`.
	q := newPull(t, ".")
	q.report = survivors
	q.run()
	for _, r := range q.runs {
		if strings.Contains(r, "-tags") {
			t.Errorf("a tree with no tagged suite set tags: %s", r)
		}
	}
}

// A nested module is graded in its own directory, and its diff is taken relative
// to it. gremlins scoped to `git diff --merge-base <base>`, whose paths are
// relative to the repository, and matched them against paths relative to the
// module: in tools/forge nothing matched and the run scored zero mutants on a
// pull that changed three of its files (foundry-stocks #199).
func TestGoMutationInANestedModuleDiffsRelativeToIt(t *testing.T) {
	p := newPull(t, "tools/forge")
	if v := p.run(); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	dir := filepath.Join(p.in.Root, "tools/forge")
	c, ok := p.call("-changed-since")
	if !ok || c.Dir != dir {
		t.Fatalf("gomutants ran in %q, want the module's directory %q", c.Dir, dir)
	}
	env := strings.Join(c.Env, "\n")
	if !strings.Contains(env, "GIT_CONFIG_KEY_0=diff.relative") || !strings.Contains(env, "GIT_CONFIG_VALUE_0=true") {
		t.Errorf("a nested module needs diff.relative or it measures nothing:\n%s", env)
	}
	if d, ok := p.call("diff --relative"); !ok || d.Dir != dir {
		t.Errorf("the change set was not asked in the module: %+v", d)
	}
	// each module's work is its own
	if _, err := os.Stat(filepath.Join(p.work, "go", "tools_forge", "canary", "go.mod")); err != nil {
		t.Errorf("the controls were not written under the module's own work directory: %v", err)
	}
}

// A repository with no Go module has nothing for this atom to grade.
func TestGoMutationIsAbsentWithoutAGoModule(t *testing.T) {
	p := newPull(t, ".")
	p.in.Files = []string{"README.md"}
	if v := p.run(); v.Result != "absent" || p.ran("gomutants") {
		t.Errorf("%+v", v)
	}
	p.in.FilesErr = os.ErrPermission
	if v := p.run(); v.State != 2 || !strings.Contains(v.Reason, "could not enumerate the tree's Go modules") {
		t.Errorf("%+v", v)
	}
}
