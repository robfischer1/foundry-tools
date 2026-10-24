package fleetdaggerlockstep

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
}

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

var pass = checks.StatePass.String()

var findings = checks.StateFindings.String()

var cannot = checks.StateCannotRun.String()

const absent = "absent"

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

// doorOf answers for the git door: "<repo> <path>" to the body it serves; any
// other file is a 404, which is what the door says for a path it does not hold.
func doorOf(t *testing.T, answers map[string]string) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := answers[r.URL.Query().Get("repo")+" "+r.URL.Query().Get("path")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return checks.Door{Base: srv.URL}
}

// deadDoor is a door nothing answers at: connection refused.
func deadDoor(t *testing.T) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return checks.Door{Base: base, NoRetry: true}
}

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

var errBoom = errors.New("boom")

const engineRef = "registry.dagger.io/engine:v0.20.0@sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestDaggerLockstep(t *testing.T) {
	flux := map[string]string{"foundry/flux forge/dagger-engine-helm.yaml": "image: " + engineRef + "\n"}
	module := func(v string) map[string]string {
		return map[string]string{"dagger.json": `{"name":"x","sdk":{"source":"go"},"engineVersion":"` + v + `"}`}
	}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"a tree that pins no dagger is absent", map[string]string{"a": "x"}, false, 0, absent, []string{"nothing to hold in lockstep"}},
		{"a module at the engine's version agrees", module("v0.20.0"), false, 0, pass, []string{"1 pin(s) agree with the engine v0.20.0"}},
		{"a module newer than the engine is out of lockstep", module("v0.21.0"), false, 1, findings, []string{"is newer than the engine v0.20.0"}},
		{"an engine that could not be read is a 2", module("v0.20.0"), true, 2, cannot, []string{"the door is unreachable"}},
		{"a manifest that is not JSON is a 2", map[string]string{"dagger.json": "{"}, false, 2, cannot, []string{"dagger.json did not parse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, flux)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("the pin files outside dagger.json are read, and the CLI follows the engine", func(t *testing.T) {
		engine := map[string]string{"forge/dagger-engine-helm.yaml": "image: " + engineRef + "\n"}
		for name, tc := range map[string]struct {
			cli    string
			state  int
			result string
		}{
			"a CLI at the engine's version agrees": {"v0.20.0", 0, pass},
			"a CLI behind the engine is a finding": {"v0.19.0", 1, findings},
		} {
			in := treeIn(t, map[string]string{
				"forge/dagger-engine-helm.yaml":     engine["forge/dagger-engine-helm.yaml"],
				"bases/layer-dagger-cli/Dockerfile": "FROM x\nARG DAGGER_VERSION=" + tc.cli + "\n",
			})
			in.Door = deadDoor(t) // the tree holds the engine itself; the door is not asked
			expect(t, run(t, in), stateOf(tc.state), tc.result)
			_ = name
		}
	})
	t.Run("a pin file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "dagger.json") // listed, and not on disk
		expect(t, run(t, in), stateOf(2), cannot, "dagger.json would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// A tree that IS the engine source is judged on itself; the door is not asked.
// Its gate Jobs carry no dagger image (flux b2f34e41), and the jobs file is
// not read, so the chart and the image are the two pins.
func TestDaggerLockstepJudgesFluxOnItsOwnTree(t *testing.T) {
	ref := "registry.dagger.io/engine:v0.21.9@sha256:" + strings.Repeat("c", 64)
	in := treeIn(t, map[string]string{
		"forge/dagger-engine-helm.yaml": "chart: dagger-helm\n  version: 0.21.9\nref: " + ref + "\n",
		"prime/daedalus-jobs.yaml":      "data:\n  GATE_JOB_ENGINE_NAMESPACE: \"dagger\"\n",
	})
	in.Door = deadDoor(t)
	expect(t, run(t, in), stateOf(0), pass, "lockstep: 2 pin(s) agree with the engine v0.21.9")
}

// An engine image moved alone in flux's own tree is a finding.
func TestDaggerLockstepFindsAnEngineMovedAloneInFlux(t *testing.T) {
	ref := "registry.dagger.io/engine:v0.21.10@sha256:" + strings.Repeat("c", 64)
	in := treeIn(t, map[string]string{"forge/dagger-engine-helm.yaml": "chart: dagger-helm\n  version: 0.21.9\nref: " + ref + "\n"})
	in.Door = deadDoor(t)
	expect(t, run(t, in), stateOf(1), findings, "forge/dagger-engine-helm.yaml: dagger-helm chart v0.21.9, but the engine is v0.21.10")
}

// THE CLI IS HELD TO FLUX MAIN'S ENGINE, read from the door by name.
func TestDaggerLockstepHoldsTheCLIToFluxMain(t *testing.T) {
	in := treeIn(t, map[string]string{"bases/layer-dagger-cli/Dockerfile": "ARG DAGGER_VERSION=v0.21.9\n"})
	in.Door = doorOf(t, map[string]string{"foundry/flux forge/dagger-engine-helm.yaml": "ref: registry.dagger.io/engine:v0.21.10@sha256:" + strings.Repeat("b", 64) + "\n"})
	expect(t, run(t, in), stateOf(1), findings,
		"bases/layer-dagger-cli/Dockerfile: CLI (ARG DAGGER_VERSION) v0.21.9, but the engine is v0.21.10 (foundry/flux main forge/dagger-engine-helm.yaml)")
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "The dagger CLI, engine and module pins in this tree agree with the engine foundry/flux runs — CLI and engine move in lockstep." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
