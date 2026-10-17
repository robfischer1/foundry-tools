package tsmutation

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageMutation || Def.Lane != checks.LaneTS || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "bun" || Def.Surface != "root:package.json" || Def.NotOnForks != checks.ForkChangeBar ||
		Def.History != "full" || Def.WritesTree || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
	if row.ID != id || row.Stage != Def.Stage || row.Lane != Def.Lane {
		t.Errorf("the verdict's row is not the declaration's: %+v", row)
	}
}

// ts:mutation runs git, bun and the package's own stryker as plain execs, and no
// script: nothing mounts foundry-stocks and nothing runs bash.
func TestTSMutationMeasuresTheDiffWithThePackagesStryker(t *testing.T) {
	p := newPull(t, "")
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	bin := filepath.Join(p.in.Root, "node_modules/.bin/stryker")
	c, ok := p.call("--reporters clear-text,json")
	if !ok {
		t.Fatalf("stryker never ran: %v", p.runs)
	}
	if c.Name != bin || c.Dir != p.in.Root || strings.Join(c.Args, " ") != "run .stryker-forge.mjs --mutate src/gate.ts:2-3 --concurrency 4 --reporters clear-text,json" {
		t.Errorf("stryker: %+v", c)
	}
	// The package's own config, wrapped with the fleet's skipped mutators.
	override, err := os.ReadFile(filepath.Join(p.in.Root, ".stryker-forge.mjs"))
	if err != nil || !strings.Contains(string(override), "ArithmeticOperator") || !strings.Contains(string(override), "excludedMutations") {
		t.Errorf("the override: %q %v", override, err)
	}
	// provisioning first: bun, then the history, then the diff, then the install
	order := []string{"bun --version", "rev-parse --verify --quiet abc123^{commit}", "merge-base abc123 HEAD", "git diff --unified=0", "bun install --frozen-lockfile", "--reporters"}
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
	for _, r := range p.runs {
		for _, relic := range []string{"sh -c", "bash", "MUT_", "cargo"} {
			if strings.Contains(r, relic) {
				t.Errorf("the atom ran %q", r)
			}
		}
	}
	// No declaration: every TypeScript source outside the tests is the scope.
	d, _ := p.call("git diff --unified=0")
	if strings.Join(d.Args, " ") != "diff --unified=0 since0 HEAD -- *.ts *.tsx :!*.test.ts :!*.test.tsx :!*.spec.ts :!*.spec.tsx :!*.config.ts :!*__tests__/* :!node_modules/" || d.Dir != p.in.Root {
		t.Errorf("the diff: %+v", d)
	}

	// A declared module is a :(glob) pathspec, and survivors are findings with the
	// honest table and the list.
	q := newPull(t, "critical_modules: src/**/*.ts\n")
	q.reports["."] = tsReport(tsKilled + `,{"status":"Survived","mutatorName":"ConditionalExpression","testsCompleted":1,"location":{"start":{"line":3}}}`)
	v := q.run()
	if v.State != 1 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"scoped to the declared critical modules: src/**/*.ts", "1 mutant(s) survived or were never covered",
		"| 1 | 1 | 0 | 0 | 50% of 2 | 50% of 2 |", "src/gate.ts:3  Survived   ConditionalExpression"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
	if d, _ := q.call("git diff --unified=0"); strings.Join(d.Args, " ") != "diff --unified=0 since0 HEAD -- :(glob)src/**/*.ts" {
		t.Errorf("the declared diff: %q", d.Args)
	}
}

// In a monorepo each range is mutated in the package whose Stryker config owns
// it, on the nearest stryker bin, and the packages settle as one verdict.
func TestTSMutationRunsEachPackageThatOwnsTheDiff(t *testing.T) {
	p := newPull(t, "")
	putFile(t, p.in.Root, "packages/engine/stryker.config.mjs", "")
	p.in.Files = append(p.in.Files, "packages/engine/stryker.config.mjs")
	p.reports["packages/engine"] = tsReport(`{"status":"NoCoverage","mutatorName":"BlockStatement","location":{"start":{"line":2}}}`)
	p.first(rule{"git diff --unified=0", "+++ b/packages/engine/src/index.ts\n@@ -1,0 +2 @@\n+x\n" + tsDiff, 0})
	v := p.run()
	if v.State != 1 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"1 mutant(s) survived or were never covered", "### packages/engine", "### ."} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
	// The verdict carries the mutant as a finding, rooted at the repository, the
	// way go:mutation's do — the door's record reads it, not the prose.
	if len(v.Findings) != 1 || v.Findings[0].Verdict != "violated" || v.Findings[0].Cause != "BlockStatement" ||
		!strings.HasPrefix(v.Findings[0].Subject, "packages/engine/") || v.Findings[0].Probe != "ts:mutation" {
		t.Errorf("findings = %+v", v.Findings)
	}
	// Each package runs in its own directory on the nearest bin, the root's here,
	// with its ranges relative to it.
	bin := filepath.Join(p.in.Root, "node_modules/.bin/stryker")
	var engine, root bool
	for _, c := range p.cmds {
		if strings.Contains(strings.Join(c.Args, " "), "--mutate src/index.ts:2-2") {
			engine = c.Dir == filepath.Join(p.in.Root, "packages/engine") && c.Name == bin
		}
		if strings.Contains(strings.Join(c.Args, " "), "--mutate src/gate.ts:2-3") {
			root = c.Dir == p.in.Root && c.Name == bin
		}
	}
	if !engine || !root {
		t.Errorf("each package runs in its directory on the nearest bin: engine %v root %v\n%v", engine, root, p.runs)
	}

	// The package's own bin is nearer than the root's.
	q := newPull(t, "")
	putFile(t, q.in.Root, "packages/engine/stryker.config.mjs", "")
	putFile(t, q.in.Root, "packages/engine/node_modules/.bin/stryker", "")
	q.in.Files = append(q.in.Files, "packages/engine/stryker.config.mjs")
	q.reports = map[string]string{"packages/engine": tsReport(tsKilled)}
	q.first(rule{"git diff --unified=0", "+++ b/packages/engine/src/index.ts\n@@ -1,0 +2 @@\n+x", 0})
	if v := q.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := q.call("--mutate src/index.ts:2-2")
	if c.Name != filepath.Join(q.in.Root, "packages/engine/node_modules/.bin/stryker") {
		t.Errorf("the package's own bin is nearer than the root's: %s", c.Name)
	}

	// A change no config owns is a finding, and nothing installs.
	r := newPull(t, "")
	r.in.Files = []string{"package.json"}
	r.first(rule{"git diff --unified=0", "+++ b/apps/web/src/x.ts\n@@ -1,0 +2 @@\n+x", 0})
	v = r.run()
	if v.State != 1 || !strings.Contains(said(v), "no Stryker config owns apps/web/src/x.ts") || r.ran("bun install") {
		t.Errorf("%s", said(v))
	}
}

// A frozen install that fails is diagnosed against the registry the lockfile
// names, with curl from the lane: a tarball the metadata lists and the proxy will
// not serve is a finding naming the versions that do.
func TestTSMutationDiagnosesAFailedInstall(t *testing.T) {
	const tgz = "https://nexus.example/npm/left-pad/-/left-pad-1.3.0.tgz"
	p := newPull(t, "")
	p.first(rule{"bun install", "GET " + tgz + " - 404\nerror: failed", 1})
	p.first(rule{"curl -fsSL https://nexus.example/npm/left-pad", `{"name":"left-pad","versions":{"1.1.0":{},"1.2.0":{},"1.3.0":{}}}`, 0})
	p.first(rule{"curl -fsSI -o /dev/null https://nexus.example/npm/left-pad/-/left-pad-1.2.0.tgz", "", 22})
	v := p.run()
	if v.State != 1 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"Registry index/tarball mismatch: left-pad@1.3.0 IS listed", "Versions that DO serve: 1.1.0", "running again changes nothing"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
	if p.ran("--reporters") {
		t.Errorf("a failed install went on to stryker")
	}

	// Metadata that does not answer lists nothing: the version is not indexed.
	q := newPull(t, "")
	q.first(rule{"bun install", "GET " + tgz + " - 404", 1})
	q.first(rule{"curl -fsSL", "", 22})
	if v := q.run(); v.State != 1 || !strings.Contains(said(v), "Version not in registry: left-pad@1.3.0") {
		t.Errorf("%s", said(v))
	}

	r := newPull(t, "")
	r.first(rule{"bun install", "error: ECONNRESET reading the lockfile's registry", 1})
	if v := r.run(); v.State != 2 || !strings.Contains(said(v), "network fault (rc=1)") || !strings.Contains(said(v), "ECONNRESET") {
		t.Errorf("%s", said(v))
	}
}

// stryker-js#6210: a runner 10.0.0 beside vitest 5 is patched in the tree stryker
// runs in; the files are rewritten, never edited in place.
//
// AND THE ATOM SAYS SO ON EVERY VERDICT, pass included. The patch used to be
// invisible: this test asserted only that withNewFile was CALLED, which is a claim
// about the chain and not about the runner stryker loaded, and a repo where the
// search matched nothing said nothing at all (gijmo-ui#28).
func TestTSMutationPatchesTheBrokenVitestRunnerAndSaysIt(t *testing.T) {
	const runner = "node_modules/@stryker-mutator/vitest-runner/"
	old := "x;\nreturn nameParts.join(' ').trim();\n"
	broken := func(p *pull, vitest string) {
		putFile(t, p.in.Root, runner+"package.json", `{"version":"10.0.0"}`)
		putFile(t, p.in.Root, runner+"dist/src/stryker-setup.js", old)
		putFile(t, p.in.Root, runner+"dist/src/test-helpers.js", old)
		putFile(t, p.in.Root, "node_modules/vitest/package.json", `{"version":"`+vitest+`"}`)
	}

	p := newPull(t, "")
	broken(p, "5.0.0")
	v := p.run()
	if v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"@stryker-mutator/vitest-runner: 1 copy(s) under " + p.in.Root,
		"runner 10.0.0, vitest 5.0.0 — patched for stryker-js#6210, 2 file(s), each read back"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
	for _, f := range []string{"stryker-setup.js", "test-helpers.js"} {
		body, _ := os.ReadFile(filepath.Join(p.in.Root, runner, "dist/src", f))
		if !strings.Contains(string(body), "return nameParts.filter(Boolean).join(' > ').trim();") {
			t.Errorf("%s was not patched: %q", f, body)
		}
	}
	// the patch lands after the install that wrote the runner, before stryker
	if !(strings.Index(strings.Join(p.runs, "\n"), "bun install") < strings.Index(strings.Join(p.runs, "\n"), "--reporters")) {
		t.Errorf("the patch must land after the install and before stryker:\n%s", strings.Join(p.runs, "\n"))
	}

	// Under vitest 4 the runner is left as shipped — and that is said too.
	q := newPull(t, "")
	broken(q, "4.1.11")
	v = q.run()
	if v.State != 0 || !strings.Contains(said(v), "runner 10.0.0, vitest 4.1.11 — left as shipped") {
		t.Errorf("%s", said(v))
	}
	if body, _ := os.ReadFile(filepath.Join(q.in.Root, runner, "dist/src/stryker-setup.js")); string(body) != old {
		t.Errorf("a runner under vitest 4 was patched: %q", body)
	}

	// A search that matches nothing is the silence this replaces: nothing to patch
	// is not a red, but it is never again unsaid.
	r := newPull(t, "")
	if v := r.run(); v.State != 0 || !strings.Contains(said(v), "@stryker-mutator/vitest-runner: no installed copy under "+r.in.Root) {
		t.Errorf("%s", said(v))
	}

	// A patch that does not read back is a measurement that is not taken.
	s := newPull(t, "")
	broken(s, "5.0.0")
	writeFile = func(p string, b []byte, m os.FileMode) error {
		if strings.Contains(p, "dist/src") {
			return os.ErrPermission
		}
		return os.WriteFile(p, b, m)
	}
	t.Cleanup(func() { writeFile = os.WriteFile })
	v = s.run()
	if v.State != 2 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"PATCH DID NOT LAND — 0 of 2 file(s)", "it joins test names with a space", "it is not measured"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
	if s.ran("--reporters") {
		t.Errorf("stryker ran through a runner the lane could not patch")
	}
	writeFile = os.WriteFile

	// A search that cannot run is the same silence wearing a different hat.
	u := newPull(t, "")
	broken(u, "5.0.0")
	walk = func(string, fs.WalkDirFunc) error { return os.ErrPermission }
	t.Cleanup(func() { walk = filepath.WalkDir })
	v = u.run()
	if v.State != 2 || !strings.Contains(said(v), "find could not enumerate "+u.in.Root) || !strings.Contains(said(v), "permission denied") {
		t.Errorf("%s", said(v))
	}
}

func TestTSMutationStandsDownOrCannotRun(t *testing.T) {
	const baseNeedle = "rev-parse --verify --quiet abc123^{commit}"
	for _, tc := range []struct {
		name   string
		base   string
		script func(p *pull)
		state  int
		// reason is checked off a finding or could-not-run: a pass keeps no output
		// (checks.VerdictOf), so a stand-down is told apart by what ran.
		reason  []string
		reached []string
		never   []string
	}{
		{"no base", "-", nil, 0, nil, nil, []string{baseNeedle, "bun --version"}},
		{"a base the history lacks", "abc123", func(p *pull) { p.first(rule{baseNeedle, "", 1}) }, 0, nil,
			[]string{baseNeedle}, []string{"merge-base", "git diff"}},
		{"no merge base", "abc123", func(p *pull) { p.first(rule{"merge-base", "", 1}) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 1)"}, []string{"merge-base"}, []string{"git diff"}},
		{"no TypeScript changed", "abc123", func(p *pull) { p.first(rule{"git diff --unified=0", "", 0}) }, 0, nil,
			[]string{"git diff"}, []string{"bun install"}},
		{"lines only removed", "abc123", func(p *pull) {
			p.first(rule{"git diff --unified=0", "+++ b/src/gate.ts\n@@ -2,2 +1,0 @@\n-a\n-b", 0})
		}, 0, nil, []string{"git diff"}, []string{"bun install"}},
		{"git cannot diff", "abc123", func(p *pull) { p.first(rule{"git diff --unified=0", "bad object", 1}) }, 2,
			[]string{"git could not diff the pull against its base since0: bad object"}, nil, []string{"bun install"}},
		{"the configs cannot be listed", "abc123", func(p *pull) { p.in.FilesErr = os.ErrPermission }, 2,
			[]string{"could not enumerate the tree's Stryker configs", "permission denied"}, nil, []string{"bun install"}},
		{"zero mutants instrumented", "abc123", func(p *pull) {
			p.code, p.printed = 1, "Instrumented 1 source file(s) with 0 mutant(s)\nNo tests were executed"
			delete(p.reports, ".")
		}, 0, nil, []string{"--reporters"}, nil},
		{"no report", "abc123", func(p *pull) {
			p.code, p.printed = 1, "ConfigError: No tests were executed"
			delete(p.reports, ".")
		}, 2, []string{"stryker exited 1 and wrote no reports/mutation/mutation.json", "No tests were executed"}, nil, nil},
		{"a broken run with a report", "abc123", func(p *pull) { p.code = 1 }, 2,
			[]string{"stryker exited 1 — a broken run"}, nil, nil},
		{"a report that does not parse", "abc123", func(p *pull) { p.reports["."] = "{" }, 2,
			[]string{"the mutation report could not be read"}, nil, nil},
		{"no bun in the image", "abc123", func(p *pull) { p.first(rule{"bun --version", "exec: bun: not found", -1}) }, 2,
			[]string{"the atom never ran: exec: bun: not found"}, nil, []string{"git diff"}},
		{"the base check never ran", "abc123", func(p *pull) { p.first(rule{baseNeedle, "git: not found", -1}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"merge-base"}},
		{"the diff never ran", "abc123", func(p *pull) { p.first(rule{"git diff --unified=0", "git: not found", -1}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"bun install"}},
		{"the diff was killed", "abc123", func(p *pull) { p.first(rule{"git diff --unified=0", "Killed", 137}) }, 2,
			[]string{"the atom never ran: exit code: 137"}, nil, []string{"bun install"}},
		{"the install never ran", "abc123", func(p *pull) { p.first(rule{"bun install", "bun: not found", -1}) }, 2,
			[]string{"the atom never ran: bun: not found"}, nil, []string{"--reporters"}},
		{"stryker never ran", "abc123", func(p *pull) { p.first(rule{"--reporters", "node: not found", -1}) }, 2,
			[]string{"the atom never ran: node: not found"}, nil, nil},
		{"stryker was killed", "abc123", func(p *pull) { p.first(rule{"--reporters", "Killed", 137}) }, 2,
			[]string{"the atom never ran: exit code: 137"}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, "")
			if tc.base == "-" {
				p.in.Base = ""
			}
			if tc.script != nil {
				tc.script(p)
			}
			v := p.run()
			if v.State != tc.state {
				t.Fatalf("state %d (%s), want %d:\n%s", v.State, v.Result, tc.state, said(v))
			}
			for _, w := range tc.reason {
				if !strings.Contains(said(v), w) {
					t.Errorf("the verdict lacks %q:\n%s", w, said(v))
				}
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

// A missing base the door named is LOUD on the door's own tree, and a stand-down
// on a local snapshot.
func TestTSMutationFailsLoudOnAFetchedTreeMissingItsBase(t *testing.T) {
	p := newPull(t, "")
	p.first(rule{"rev-parse --verify", "", 1})
	t.Setenv("ATOM_ORIGIN", "https://door/x")
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "CANNOT RUN - the base abc123 is not in this history, though the door named it and it was fetched") {
		t.Fatalf("%s", said(v))
	}
}

// A locked tree (the executor's) is installed in and run from a copy; the history
// stays with the original.
func TestALockedTreeIsInstalledInACopy(t *testing.T) {
	p := newPull(t, "")
	for _, f := range []string{"package.json", "stryker.config.json"} {
		if err := os.Chmod(filepath.Join(p.in.Root, f), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("bun install")
	want := filepath.Join(os.Getenv("ATOM_WORK"), "ts", "src")
	if c.Dir != want {
		t.Errorf("a locked tree is installed in its copy %s, not %s", want, c.Dir)
	}
	if d, _ := p.call("git diff"); d.Dir != p.in.Root {
		t.Errorf("the history is the original's: git ran in %s", d.Dir)
	}
	// and a tree that cannot be copied is the atom's could-not-run
	q := newPull(t, "")
	for _, f := range []string{"package.json"} {
		if err := os.Chmod(filepath.Join(q.in.Root, f), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	putFile(t, os.Getenv("ATOM_WORK"), "ts", "a file where the work directory goes")
	if v := q.run(); v.State != 2 || !strings.Contains(said(v), "the tree could not be copied to install in") {
		t.Errorf("%s", said(v))
	}
}

// The override file the atom writes beside the package's config, and the report it
// reads back, are the disk's: a write that fails is the atom's could-not-run.
func TestTSMutationCannotRunWhereItCannotWrite(t *testing.T) {
	p := newPull(t, "")
	writeFile = func(path string, b []byte, m os.FileMode) error {
		if strings.HasSuffix(path, ".stryker-forge.mjs") {
			return os.ErrPermission
		}
		return os.WriteFile(path, b, m)
	}
	t.Cleanup(func() { writeFile = os.WriteFile })
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "CANNOT RUN - the atom never ran: permission denied") {
		t.Errorf("%s", said(v))
	}
	if p.ran("--reporters") {
		t.Error("stryker ran without its config")
	}
}
