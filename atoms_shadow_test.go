package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// NO TEST RUNS THE REAL SHADOW BY ACCIDENT. This init is the seam's other half:
// the variables it overrides are documented in atoms_shadow.go (shadowRun and
// shadowOut). Gate calls startShadow, and the real shadow asks the paper engine for the whole vector and a binary run, which
// would add queries to every gate test's record and print a report into its
// stderr. The tests that mean the shadow set their own.
func init() {
	shadowRun = func(context.Context, *FoundryTools, string, string) string { return "" }
	shadowOut = func() io.Writer { return io.Discard }
}

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
// that the list omits is a `go build` failure no unit test can see: the shadow
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

func TestRenderShadow(t *testing.T) {
	yaml := checks.AtomByID("fleet:check-yaml")
	today := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	agree, _ := json.Marshal(today)
	differ, _ := json.Marshal([]checks.Verdict{checks.VerdictOf(yaml, 2, "binary could not")})
	for _, tc := range []struct {
		name     string
		today    []checks.Verdict
		todayErr error
		raw      string
		rawErr   error
		want     string
	}{
		{"the chains did not answer", nil, errors.New("engine gone"), string(agree), nil, "not compared - the chains did not answer: engine gone"},
		{"the binary did not answer", today, nil, "", errors.New("exit 1"), "not compared - the binary did not answer: exit 1"},
		{"the binary printed something that is not a vector", today, nil, "panic: oops", nil, "not compared - the module's output is not a verdict vector"},
		{"agreement", today, nil, string(agree), nil, "shadow atoms: 1 compared, 1 identical, 0 same state, 0 state differs"},
		{"disagreement", today, nil, string(differ), nil, "fleet:check-yaml: STATE DIFFERS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderShadow(tc.today, tc.todayErr, tc.raw, tc.rawErr, 0); !strings.Contains(got, tc.want) {
				t.Errorf("report %q lacks %q", got, tc.want)
			}
		})
	}
}

// The chains' error wins when both sides failed: it is the one whose absence
// makes the comparison meaningless, and the report names one cause.
func TestRenderShadowNamesTheChainsFirst(t *testing.T) {
	got := renderShadow(nil, errors.New("a"), "", errors.New("b"), 0)
	if !strings.Contains(got, "the chains did not answer: a") || strings.Contains(got, "binary") {
		t.Errorf("report %q", got)
	}
}

// THE SHADOW CANNOT VOTE. Whatever it does - hang past its deadline, panic,
// answer an error line, answer nothing - the record Gate returns is the bytes it
// returns without a shadow, and the call returns no error.
func TestAShadowThatMisbehavesChangesNothingTheGateAnswers(t *testing.T) {
	origRun, origGrace, origOut := shadowRun, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowGrace, shadowOut = origRun, origGrace, origOut })
	shadowOut = func() io.Writer { return io.Discard }
	shadowRun = func(context.Context, *FoundryTools, string, string) string { return "" }
	m := gateOn(t, redVector)
	want := runGate(t, m, "")

	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	for _, tc := range []struct {
		name string
		run  func(context.Context, *FoundryTools, string, string) string
		said string
	}{
		{"hangs", func(context.Context, *FoundryTools, string, string) string { <-stuck; return "late" }, "no answer within the 50ms grace"},
		{"panics", func(context.Context, *FoundryTools, string, string) string { panic("shadow bug") }, "the shadow panicked: shadow bug"},
		{"errors", func(context.Context, *FoundryTools, string, string) string {
			return "shadow atoms: not compared - boom"
		}, "not compared - boom"},
		{"says nothing", func(context.Context, *FoundryTools, string, string) string { return "" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			shadowOut = func() io.Writer { return &buf }
			// Only the hang needs a short grace; the others answer at once and a
			// short one would let a stalled node abandon them.
			shadowGrace = 10 * time.Second
			if tc.name == "hangs" {
				shadowGrace = 50 * time.Millisecond
			}
			shadowRun = tc.run
			m := gateOn(t, redVector)
			got, err := m.Gate(context.Background(), fakeTree, gatePin, "base-sha", "")
			if err != nil {
				t.Fatalf("a shadow turned the gate into an error: %v", err)
			}
			if got != want {
				t.Errorf("the record moved.\nwith:    %s\nwithout: %s", got, want)
			}
			if !strings.Contains(buf.String(), tc.said) {
				t.Errorf("stderr report %q lacks %q", buf.String(), tc.said)
			}
		})
	}
}

// The shadow runs for the gate and the orbit lane and nowhere else, runs exactly
// once, and is told the stage it compares.
func TestTheShadowRunsOnTheGateAndOrbitLanesOnly(t *testing.T) {
	origRun, origOut := shadowRun, shadowOut
	t.Cleanup(func() { shadowRun, shadowOut = origRun, origOut })
	shadowOut = func() io.Writer { return io.Discard }
	for stage, want := range map[string]int{"": 1, "prepush": 1, "orbit": 1, "precommit": 0, "mutation": 0, "mutation-bg": 0, "visual": 0} {
		var told []string
		shadowRun = func(_ context.Context, _ *FoundryTools, stage, _ string) string {
			told = append(told, stage)
			return "r"
		}
		(&FoundryTools{}).startShadow(context.Background(), stage, "b").finish()
		if len(told) != want {
			t.Errorf("stage %q: shadow ran %d times, want %d", stage, len(told), want)
		}
		if want == 1 && told[0] != stage {
			t.Errorf("stage %q: the shadow was told %q", stage, told[0])
		}
	}
}

// ShadowAtoms asks the chains for exactly the atoms the stage grades that the
// binary carries, in its order, at that stage, and holds the two answers against
// each other. The orbit lane's report compares the orbit lane's atoms and the
// gate's never carries one.
func TestShadowAtomsAsksTheChainsForTheStagesAtoms(t *testing.T) {
	origToday, origBinary := shadowToday, shadowBinary
	t.Cleanup(func() { shadowToday, shadowBinary = origToday, origBinary })
	yaml := checks.AtomByID("fleet:check-yaml")
	vec := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	raw, _ := json.Marshal(vec)
	for _, tc := range []struct {
		stage    string
		only     string
		notOnly  string
		compared bool
	}{
		{"prepush", "fleet:orbit-drift,fleet:dagger-lockstep,fleet:node-kinds-declared,fleet:consumed-events-emitted,dies:data-keys,ops:orbit-composed", "fleet:check-yaml", true},
		{"orbit", "orbit:contracts,orbit:sidecars,orbit:repo", "fleet:", true},
		{"", "fleet:check-yaml,fleet:check-added-large-files,fleet:check-merge-conflict,fleet:stop-justifications,", "orbit:", true},
		{"mutation", "", "", false},
	} {
		t.Run("stage "+tc.stage, func(t *testing.T) {
			var gotOnly, gotBase, binBase, todayStage, binStage string
			shadowToday = func(_ context.Context, _ *FoundryTools, stage, only, base string) ([]checks.Verdict, error) {
				gotOnly, gotBase, todayStage = only, base, stage
				return vec, nil
			}
			shadowBinary = func(_ context.Context, _ *FoundryTools, stage, base string) (string, error) {
				binBase, binStage = base, stage
				return string(raw), nil
			}
			report := (&FoundryTools{}).ShadowAtoms(context.Background(), "b4se", tc.stage)
			if !tc.compared {
				if !strings.Contains(report, "nothing to compare") || gotOnly != "" || binBase != "" {
					t.Errorf("a stage with no atom in the binary compared something: %q (%q)", report, gotOnly)
				}
				return
			}
			if !strings.Contains(gotOnly, tc.only) || (tc.stage != "" && gotOnly != tc.only) {
				t.Errorf("chains asked for %q, want %q", gotOnly, tc.only)
			}
			if tc.notOnly != "" && strings.Contains(gotOnly, tc.notOnly) && tc.stage == "orbit" {
				t.Errorf("the orbit lane's shadow asked for %q: %q", tc.notOnly, gotOnly)
			}
			if tc.stage == "" && strings.Contains(gotOnly, tc.notOnly) {
				t.Errorf("the gate's shadow asked for an orbit atom: %q", gotOnly)
			}
			if gotBase != "b4se" || binBase != "b4se" || todayStage != tc.stage || binStage != tc.stage {
				t.Errorf("base reached the chains as %q and the binary as %q; stage %q and %q, want %q", gotBase, binBase, todayStage, binStage, tc.stage)
			}
			if !strings.Contains(report, "1 compared, 1 identical") {
				t.Errorf("report %q", report)
			}
		})
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
	for _, want := range []string{`"go.mod"`, `"go.sum"`, `"internal/atoms/**"`, `"internal/checks/**"`, `"internal/orbitlane/**"`, `"internal/retiredverbs/**"`} {
		if !strings.Contains(f, want) {
			t.Errorf("the filter lacks %s:\n%s", want, f)
		}
	}
}

func TestAtomsVectorRunsTheBinaryInTheFleetLane(t *testing.T) {
	const argv = `"/usr/local/bin/atoms","-root","/src","-base","abc","-origin","http://door/rob/x.git","-stage","","-dies","/dies"`
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
			got, err := m.atomsVector(context.Background(), "", "abc")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tc.wantErr)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			c := engine.chain(argv)
			for _, want := range []string{`expect:ANY`, `from(address:"` + checks.ImageFleet + `")`, `path:"/usr/local/bin/atoms"`} {
				if !strings.Contains(c, want) {
					t.Errorf("the run chain lacks %s:\n%s", want, c)
				}
			}
		})
	}
}

// THE STAGE REACHES THE BINARY, and with it exactly the tools that stage's atoms
// use: the orbit lane gets the contracts and the sidecar reader and no opa; the
// pull path gets the contracts and no sidecar reader.
func TestAtomsVectorMountsWhatTheStageUses(t *testing.T) {
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	for _, tc := range []struct {
		stage   string
		argv    string
		mounted []string
		absent  []string
	}{
		{"orbit", `"-stage","orbit","-dies","/dies"`, []string{`path:"/usr/local/bin/orbitparse"`}, []string{`path:"/usr/local/bin/opa"`}},
		{"prepush", `"-stage","prepush","-dies","/dies"`, []string{`path:"/usr/local/bin/opa"`}, []string{`path:"/usr/local/bin/orbitparse"`}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			engine.reset()
			engine.withTree(map[string]string{"go.mod": "module x\n"})
			engine.stdout(`"/usr/local/bin/atoms"`, "[]")
			if _, err := m.atomsVector(context.Background(), tc.stage, "abc"); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(tc.argv)
			if c == "" {
				t.Fatalf("no run chain carried %s; the engine saw:\n%s", tc.argv, strings.Join(engine.chains(), "\n"))
			}
			// The mount, not the flag that names it.
			for _, want := range append(tc.mounted, `path:"/dies"`) {
				if !strings.Contains(c, want) {
					t.Errorf("the run chain lacks %s:\n%s", want, c)
				}
			}
			for _, no := range tc.absent {
				if strings.Contains(c, no) {
					t.Errorf("the run chain carries %s, which no atom of the stage uses:\n%s", no, c)
				}
			}
		})
	}
}

// The gate does not wait out a shadow that hangs: its wall time is the vector's
// plus at most the grace. The vector takes 200ms here; the shadow never answers.
func TestAHangingShadowCostsTheGateAtMostTheGrace(t *testing.T) {
	origRun, origGrace, origOut := shadowRun, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowGrace, shadowOut = origRun, origGrace, origOut })
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	shadowRun = func(context.Context, *FoundryTools, string, string) string { <-stuck; return "late" }
	shadowGrace = 100 * time.Millisecond
	var buf bytes.Buffer
	shadowOut = func() io.Writer { return &buf }

	const vectorTime = 200 * time.Millisecond
	m := gateOn(t, cleanVector)
	slow := gateVector
	gateVector = func(ctx context.Context, m *FoundryTools, s, b string) (string, error) {
		time.Sleep(vectorTime)
		return slow(ctx, m, s, b)
	}
	start := time.Now()
	if _, err := m.Gate(context.Background(), fakeTree, gatePin, "base-sha", ""); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < vectorTime {
		t.Fatalf("the gate returned in %v, before its own vector (%v)", elapsed, vectorTime)
	}
	if limit := vectorTime + shadowGrace + 2*time.Second; elapsed > limit {
		t.Errorf("the gate took %v with a hung shadow; vector %v + grace %v allows %v", elapsed, vectorTime, shadowGrace, limit)
	}
	if !strings.Contains(buf.String(), "no answer within") {
		t.Errorf("stderr %q", buf.String())
	}
}

// A shadow that answers before the gate settles costs the gate nothing.
func TestAShadowThatFinishesFirstIsReportedWithoutWaiting(t *testing.T) {
	origRun, origGrace, origOut := shadowRun, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowGrace, shadowOut = origRun, origGrace, origOut })
	shadowRun = func(context.Context, *FoundryTools, string, string) string { return "report" }
	shadowGrace = time.Hour
	var buf bytes.Buffer
	shadowOut = func() io.Writer { return &buf }
	h := (&FoundryTools{}).startShadow(context.Background(), "", "b")
	start := time.Now()
	h.finish()
	if time.Since(start) > 30*time.Second || buf.String() != "report\n" {
		t.Errorf("waited %v, stderr %q", time.Since(start), buf.String())
	}
	var nilHandle *shadowHandle
	nilHandle.finish() // a lane with no shadow
}

// The shadow's own wall time rides on its headline, and an unmeasured one is not printed.
func TestRenderShadowReportsItsWallTime(t *testing.T) {
	yaml := checks.AtomByID("fleet:check-yaml")
	vec := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	raw, _ := json.Marshal(vec)
	if got := renderShadow(vec, nil, string(raw), nil, 1234*time.Millisecond+400*time.Microsecond); !strings.Contains(got, "missing from the chains, took 1.234s\n") {
		t.Errorf("report %q", got)
	}
	if got := renderShadow(vec, nil, string(raw), nil, 0); strings.Contains(got, "took") {
		t.Errorf("an unmeasured run printed a time: %q", got)
	}
}

// THE BINARY'S EXEC IS KEYED AFRESH ON EVERY CALL. Its inputs are the same on
// every run of one tree, so without CA_REASK the engine would serve the first
// answer for the atoms that read the network. Two calls never share a value.
func TestAtomsVectorCarriesAPerCallReask(t *testing.T) {
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	re := regexp.MustCompile(`CA_REASK\D+(\d+)`)
	var seen []string
	for range 2 {
		engine.reset()
		engine.withTree(map[string]string{"go.mod": "module x\n"})
		engine.stdout(`"/usr/local/bin/atoms"`, "[]")
		if _, err := m.atomsVector(context.Background(), "", "abc"); err != nil {
			t.Fatal(err)
		}
		c := engine.chain(`"/usr/local/bin/atoms"`)
		match := re.FindStringSubmatch(c)
		if match == nil {
			t.Fatalf("the binary's exec carries no CA_REASK:\n%s", c)
		}
		seen = append(seen, match[1])
	}
	if seen[0] == seen[1] {
		t.Errorf("two calls shared the reask value %s", seen[0])
	}
}

// foundry-dies is mounted only for a stage with an atom that reads it.
func TestAtomsVectorMountsDiesOnlyWhereAnAtomReadsIt(t *testing.T) {
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	engine.stdout(`"/usr/local/bin/atoms"`, "[]")
	if _, err := m.atomsVector(context.Background(), checks.StagePrecommit, "abc"); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"-stage","precommit"`)
	if c == "" || strings.Contains(c, `path:"/dies"`) || strings.Contains(c, `"-dies"`) {
		t.Errorf("a stage whose atoms never read foundry-dies mounted it (or ran no chain):\n%s", c)
	}
}
