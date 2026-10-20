package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// The legacy tests grade through the chains; the ones that mean the vote flip it
// back (atoms_vote_test.go) and restore it.
func init() { atomsVoter = voterChains }

// coveredBy says whether the include list reaches dir: by its own entry, or by a
// directory entry above it.
func coveredBy(dir string) bool {
	for _, p := range atomsSourceInclude {
		root, ok := strings.CutSuffix(p, "/**")
		if ok && (dir == root || strings.HasPrefix(dir, root+"/")) {
			return true
		}
	}
	return false
}

// THE FILTERED SOURCE IS ONLY AS GOOD AS ITS LIST. The atoms binary is built in
// the engine from atomsSourceInclude and nothing else, so a package it imports
// that the list omits is a `go build` failure no unit test can see: the vote
// would report "the binary did not answer" on every run, forever, and look like
// a finding about the binary. This walks the import closure of ./atoms through
// the module's own packages and holds the list to it in both directions - a
// missing directory fails the build, a stale one makes every unrelated edit
// rebuild the binary.
func TestAtomsSourceCoversImports(t *testing.T) {
	const prefix = "dagger/foundry-tools/"
	reached := map[string]bool{}
	queue := []string{"atoms"}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if reached[dir] {
			continue
		}
		reached[dir] = true
		if !coveredBy(dir) {
			t.Errorf("%s is imported by the atoms binary but atomsSourceInclude does not include it", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if rel, ok := strings.CutPrefix(path, prefix); ok {
					queue = append(queue, rel)
				}
			}
		}
	}
	for _, p := range atomsSourceInclude {
		root, ok := strings.CutSuffix(p, "/**")
		if !ok {
			if p != "go.mod" && p != "go.sum" {
				t.Errorf("%q is neither a package directory (/**) nor a module file", p)
			}
			continue
		}
		if !reached[root] {
			t.Errorf("%s is in atomsSourceInclude but the atoms binary does not import it", root)
		}
	}
	if !reached["internal/atoms"] || !reached["internal/checks"] {
		t.Errorf("the walk never reached the packages it exists for: %v", reached)
	}
}

func TestAtomsBinaryIsBuiltFromTheFilteredSourceInTheGoToolchain(t *testing.T) {
	engine.reset()
	if _, err := dag.Container().From("scratch").WithFile("/b", atomsBinary()).Stdout(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"go","build","-trimpath","-o","/out/atoms","./atoms"`)
	if c == "" {
		t.Fatalf("no build chain; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
	}
	for _, want := range []string{`from(address:"` + checks.ImageGo + `")`, `withMountedCache`, `withWorkdir`} {
		if !strings.Contains(c, want) {
			t.Errorf("the build lacks %s:\n%s", want, c)
		}
	}
	// The filtered source is an object of its own: the build mounts it by id, so
	// the include and exclude lists are read off the query that made it.
	f := engine.chain(`"atoms/**"`, `"**/*_test.go"`)
	if f == "" {
		t.Fatalf("no filter query carried the include and exclude lists; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
	}
	for _, want := range []string{`"go.mod"`, `"go.sum"`, `"internal/atoms/**"`, `"internal/checks/**"`, `"internal/retiredverbs/**"`} {
		if !strings.Contains(f, want) {
			t.Errorf("the filter lacks %s:\n%s", want, f)
		}
	}
}

func TestAtomsRunRunsTheBinaryInTheToolsContainer(t *testing.T) {
	const argv = `"/usr/local/bin/atoms","-root","/src","-base","abc","-origin","http://door/rob/x.git","-stage","","-timeout","4m"`
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	for _, tc := range []struct {
		name    string
		script  func()
		want    string
		wantErr string
	}{
		{"the vector is its stdout", func() { engine.stdout(`"/usr/local/bin/atoms"`, "[]") }, "[]", ""},
		{"a non-zero exit is an error, not a vector", func() { engine.exitCode(`"/usr/local/bin/atoms"`, 3) }, "", "exited 3"},
		{"an engine that would not run it is an error", func() { engine.failLeaf(`"/usr/local/bin/atoms"`, "exitCode", "engine gone") }, "", "engine gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(map[string]string{"go.mod": "module x\n"})
			tc.script()
			got, err := m.atomsRun(context.Background(), "", "abc")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tc.wantErr)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			c := engine.chain(argv)
			for _, want := range []string{`expect:ANY`, `from(address:"` + checks.ImageTools + `")`, `path:"/usr/local/bin/atoms"`} {
				if !strings.Contains(c, want) {
					t.Errorf("the run chain lacks %s:\n%s", want, c)
				}
			}
		})
	}
}

// THE STAGE REACHES THE BINARY, and the tools are the container's, the same for
// every stage: each pinned tool is a layer whatever the stage (a layer nobody
// execs costs nothing once cached, and a stage-shaped container would be a
// second container to build).
func TestAtomsRunCarriesTheToolsForEveryStage(t *testing.T) {
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	for _, tc := range []struct {
		stage string
		argv  string
	}{
		{"prepush", `"-stage","prepush","-timeout","4m"`},
		{"precommit", `"-stage","precommit","-timeout","4m"`},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			engine.reset()
			engine.withTree(map[string]string{"go.mod": "module x\n"})
			engine.stdout(`"/usr/local/bin/atoms"`, "[]")
			if _, err := m.atomsRun(context.Background(), tc.stage, "abc"); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(tc.argv)
			if c == "" {
				t.Fatalf("no run chain carried %s; the engine saw:\n%s", tc.argv, strings.Join(engine.chains(), "\n"))
			}
			// The mounts, not the flags that name them.
			for _, l := range toolsPlan() {
				if l.kind != layerTool {
					continue
				}
				if want := `path:"/usr/local/bin/` + l.name + `"`; !strings.Contains(c, want) {
					t.Errorf("the run chain lacks the %s layer (%s):\n%s", l.name, want, c)
				}
			}
			if strings.Contains(c, checks.ImageFleet) {
				t.Errorf("the binary ran on the python fleet image:\n%s", c)
			}
		})
	}
}

// THE BINARY'S EXEC IS KEYED AFRESH ON EVERY CALL. Its inputs are the same on
// every run of one tree, so without CA_REASK the engine would serve the first
// answer for the atoms that read the network. Two calls never share a value.
func TestAtomsRunCarriesAPerCallReask(t *testing.T) {
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	// THE CALL'S OWN VALUE, in either argument order. The fake engine renders a
	// call's arguments in map order, so CA_REASK's withEnvVariable reads
	// (name, value) on one run and (value, name) on the next. The old
	// `CA_REASK\D+(\d+)` read past the name to the first digit after it — under
	// (value, name) that was the 6 in the next call's "id-e6d88a18", the same on
	// both calls, and the gate went red on foundry-tools#332.
	re := regexp.MustCompile(`withEnvVariable\((?:name:"CA_REASK", value:"(\d+)"|value:"(\d+)", name:"CA_REASK")\)`)
	var seen []string
	for i := range 2 {
		engine.reset()
		engine.withTree(map[string]string{"go.mod": "module x\n"})
		engine.stdout(`"/usr/local/bin/atoms"`, "[]")
		// Each call has a base of its own, and the chain is matched on it: the fake
		// engine is shared, and another goroutine's binary query must not be read.
		base := "abc-" + strconv.Itoa(i)
		if _, err := m.atomsRun(context.Background(), "", base); err != nil {
			t.Fatal(err)
		}
		c := engine.chain(`"/usr/local/bin/atoms"`, `"-base","`+base+`"`)
		match := re.FindStringSubmatch(c)
		if match == nil {
			t.Fatalf("the binary's exec carries no CA_REASK:\n%s", c)
		}
		seen = append(seen, match[1]+match[2])
	}
	if seen[0] == seen[1] {
		t.Errorf("two calls shared the reask value %s", seen[0])
	}
}

// THE WITNESS ASKS FOR REAL, never dry: the socket is mounted and -spire passed
// exactly for the stages with fleet:witness when the lane forwarded one.
func TestAtomsRunAsksTheWitnessForRealAndMountsTheSocketWhereItIsCarried(t *testing.T) {
	for _, tc := range []struct {
		stage  string
		spire  bool
		socket bool
	}{
		{"prepush", true, true},
		{"prepush", false, false},
		{"", true, true},
		{"", false, false},
		{"precommit", true, false},
	} {
		t.Run(tc.stage+map[bool]string{true: " with a socket", false: " without"}[tc.spire], func(t *testing.T) {
			m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
			if tc.spire {
				m.spire = dag.LoadSocketFromID("spire-agent-socket")
			}
			engine.reset()
			engine.withTree(map[string]string{"go.mod": "module x\n"})
			engine.stdout(`"/usr/local/bin/atoms"`, "[]")
			if _, err := m.atomsRun(context.Background(), tc.stage, "abc"); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(`"/usr/local/bin/atoms"`)
			if got := strings.Contains(c, `"-spire"`) && strings.Contains(c, "withUnixSocket"); got != tc.socket {
				t.Errorf("socket mounted: %v, want %v\n%s", got, tc.socket, c)
			}
		})
	}
	t.Run("the voter's socket helper still forwards one", func(t *testing.T) {
		r := newRun(dag.Directory(), "http://door/rob/x.git", "abc").withSpire(dag.LoadSocketFromID("spire-agent-socket"))
		ctr, flags := r.withAtomSpire(dag.Container(), "prepush")
		if ctr == nil || len(flags) != 2 || flags[0] != "-spire" || flags[1] != atomsSpirePath {
			t.Errorf("container %v, flags %v", ctr, flags)
		}
		if ctr, flags := r.withAtomSpire(dag.Container(), "precommit"); ctr == nil || len(flags) != 0 {
			t.Errorf("a stage without the witness got %v", flags)
		}
		bare := newRun(dag.Directory(), "http://door/rob/x.git", "abc")
		if ctr, flags := bare.withAtomSpire(dag.Container(), "prepush"); ctr == nil || len(flags) != 0 {
			t.Errorf("a run with no socket got %v", flags)
		}
	})
}

// THE BASE IS FETCHED ONCE PER RUN, by the module, for the binary's one change
// set: not once per change-reading atom. fleet:witness and ops:immutable read
// Input.Changed and Input.Base and fetch nothing themselves.
func TestAtomsRunFetchesTheBaseOnce(t *testing.T) {
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	engine.stdout(`"/usr/local/bin/atoms"`, "[]")
	if _, err := m.atomsRun(context.Background(), "prepush", "abc"); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"/usr/local/bin/atoms"`)
	// Every fetch exec, whatever its argv: a second spelling of the fetch is
	// still a second fetch.
	if n := strings.Count(c, `"fetch"`); n != 1 {
		t.Errorf("%d fetch exec(s) in the run's chain, want one:\n%s", n, c)
	}
}

// No atom fetches: the binary's source never execs `git fetch`, so the base the
// change set is measured from can only have come from the module's one fetch.
func TestNoAtomExecsGitFetch(t *testing.T) {
	files, err := filepath.Glob("internal/atoms/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, _ := strconv.Unquote(lit.Value); v == "fetch" || v == "pull" {
					t.Errorf("%s: %s names git %s, which only the module may run", f, fset.Position(lit.Pos()), v)
				}
			}
			return true
		})
	}
}

// A FORK IS NAMED TO THE BINARY (-fork) by its record; a star is not.
func TestAtomsRunNamesAForkToTheBinary(t *testing.T) {
	for name, tc := range map[string]struct {
		kind string
		want bool
	}{"a fork": {"vendored", true}, "a star": {"rust", false}} {
		t.Run(name, func(t *testing.T) {
			m := &FoundryTools{Source: dag.Directory(), Repo: forkDoor, Sha: buildSha}
			engine.reset()
			engine.withTree(map[string]string{"go.mod": "module x\n"})
			engine.withTree(map[string]string{"/dies/fleet/repos/gravity.json": `{"name":"gravity","kind":"` + tc.kind + `","repo":"forks/gravity"}`})
			engine.stdout(`"/usr/local/bin/atoms"`, "[]")
			if _, err := m.atomsRun(context.Background(), checks.StagePrecommit, "abc"); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(`"-stage","precommit"`)
			if got := strings.Contains(c, `"-fork","gravity"`); got != tc.want {
				t.Errorf("-fork present %v, want %v:\n%s", got, tc.want, c)
			}
		})
	}
}
