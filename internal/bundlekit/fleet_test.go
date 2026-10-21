package bundlekit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/bundlelane"
)

func TestFleetStagesTheRosterAndBuildsItFromTheStageDirectory(t *testing.T) {
	w := newWorld(t, nil)
	if f := Fleet(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	// The stage: fleet/ less the retired slag, the star rows and the catalog entries,
	// plus the three injected documents and the hand-written manifest.
	for rel, want := range map[string]bool{
		"fleet/data.json": true, "fleet/stars/old.slag": false, "fleet/repos/chaos.json": false,
		"fleet/declared/data.json": true, "fleet/flux/data.json": true, "fleet/flux_source/data.json": true, ".manifest": true,
	} {
		_, err := os.Stat(filepath.Join(w.stage, rel))
		if (err == nil) != want {
			t.Errorf("%s staged = %v, want %v", rel, err == nil, want)
		}
	}
	src, _ := os.ReadFile(filepath.Join(w.stage, "fleet/flux_source/data.json"))
	if !strings.Contains(string(src), fluxSha) {
		t.Errorf("flux_source %s", src)
	}
	// opa names a rego file by the path it was built from: the stage's own.
	want := "opa build -b " + w.stage + " -o " + filepath.Join(w.out, "fleet-bundle.tar.gz") + " --revision " + sha + "-ffedcba9"
	if w.ran(want) != 1 || w.ran("--signing") != 0 {
		t.Errorf("builds: %v", w.calls)
	}
	m, err := ReadMeta("fleet")
	if err != nil {
		t.Fatal(err)
	}
	if m.Die != bundlelane.FleetDie || m.Pin != "g0123456-ffedcba9" || m.Revision != sha+"-ffedcba9" || !m.Publish || !m.PublishIfMissing ||
		len(m.Artifacts) != 1 || m.Artifacts[0] != (Artifact{"fleet-bundle.tar.gz", "", "stable"}) {
		t.Errorf("meta %+v", m)
	}
	if w.ran("--revision gate") != 1 || w.ran("data.admission.deny") != 1 || w.ran("--format raw data") != 1 {
		t.Errorf("gates: %v", w.calls)
	}
	if entries, _ := filepath.Glob(filepath.Join(w.out, "*signed*")); len(entries) != 0 {
		t.Errorf("signed leftovers: %v", entries)
	}
}

func TestFleetPublishesWhatTheLandingTouchedAndAsksForTheRest(t *testing.T) {
	w := newWorld(t, nil)
	w.diff = "policy/admission/a.rego\n" // policy only: the roster is not owed a push by the paths
	if f := Fleet(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	if m, _ := ReadMeta("fleet"); m.Publish || !m.PublishIfMissing {
		t.Errorf("%+v", m)
	}
}

func TestFleetRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    map[string]string
		edit     func(*world)
		findings bool
		needles  []string
	}{
		{"no tier data", map[string]string{"fleet/data.json": ""}, nil, true, []string{"fleet/data.json is absent"}},
		{"tier data is not JSON", map[string]string{"fleet/data.json": "x"}, nil, true, []string{"not JSON"}},
		{"a catalog entry the map lacks", map[string]string{"fleet/repos/ghost.json": "{}"}, nil, true, []string{"catalog entries with no fleet/data.json map entry: ghost"}},
		{"git cannot read history", nil, func(w *world) { w.gitDead = true }, false, []string{"the history could not be read"}},
		{"no flux checkout", nil, func(w *world) { os.Remove(filepath.Join(w.flux, FluxShaFile)) }, false, []string{"foundry/flux main could not be resolved to a commit"}},
		{"flux names no star", nil, func(w *world) { os.Remove(filepath.Join(w.flux, "prime/star-chaos.yaml")) }, true, []string{"foundry/flux answered no prime/star-*.yaml"}},
		{"flux sha is not a sha", nil, func(w *world) { os.WriteFile(filepath.Join(w.flux, FluxShaFile), []byte("main"), 0o644) }, true, []string{"is not a full sha"}},
		{"an injected key is committed", map[string]string{bundlelane.FluxPath: "{}"}, nil, true, []string{"is committed, and the lane writes it"}},
		{"opa build fails", nil, func(w *world) { w.buildCode = 1 }, true, []string{"opa build over the staged roster failed"}},
		{"the roster does not evaluate", nil, func(w *world) { w.failEval["--format raw data"] = 1 }, true, []string{"the built roster does not evaluate"}},
		{"the roster is empty", nil, func(w *world) { w.roster = `{"fleet":{}}` }, true, []string{"empty data.fleet"}},
		{"the roster is not JSON", nil, func(w *world) { w.roster = "x" }, true, []string{"not JSON"}},
		{"the roster does not carry the injection", nil, func(w *world) {
			w.roster = strings.Replace(w.roster, fluxSha, "ab"+fluxSha[2:], 1)
		}, true, []string{"does not name flux"}},
		{"the manifest roots moved", nil, func(w *world) { w.fleetManifest = `{"roots":["x"]}` }, true, []string{"no longer declares exactly [fleet]"}},
		{"the composition does not load", nil, func(w *world) { w.failEval["data.admission.deny"] = 1 }, true, []string{"does not compose with the policy bundle"}},
		{"the composition answers an unreadable shape", nil, func(w *world) { w.deny = "x" }, false, []string{"shape this lane cannot read"}},
		{"the guards stopped firing", nil, func(w *world) { w.deny = `{"result":[{"expressions":[{"value":["fleet roster: a"]}]}]}` }, true, []string{"1/2 roster denials"}},
	} {
		w := newWorld(t, tc.files)
		if tc.edit != nil {
			tc.edit(w)
		}
		expect(t, Fleet(context.Background(), w.run()), tc.findings, tc.needles...)
	}
}

func TestFleetCompositionBuildFailure(t *testing.T) {
	w := newWorld(t, nil)
	n := 0
	inner := w.in.Exec
	w.in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) {
		if len(c.Args) > 1 && c.Args[0] == "build" && valueAfter(c.Args, "--revision") == "gate" {
			n++
			return "no policy", 1
		}
		return inner(ctx, c)
	}
	expect(t, Fleet(context.Background(), w.run()), true, "opa build over policy/ for the composition gate failed")
	if n != 1 {
		t.Errorf("composition builds %d", n)
	}
}

func TestFleetFilesystemFaults(t *testing.T) {
	w := newWorld(t, nil)
	bad := filepath.Join(t.TempDir(), "file")
	os.WriteFile(bad, []byte("x"), 0o644)
	t.Setenv(OutEnv, filepath.Join(bad, "sub"))
	expect(t, Fleet(context.Background(), w.run()), false, "bundle directory could not be made")
	// A stage that cannot be cleared or laid.
	w = newWorld(t, nil)
	t.Setenv(StageEnv, filepath.Join(bad, "stage"))
	expect(t, Fleet(context.Background(), w.run()), false, "could not be cleared")
	// A tree with no fleet/ to stage cannot have passed the tier gate; with no
	// map entry it is a finding there. A short commit is the lane's.
	w = newWorld(t, nil)
	t.Setenv("ATOM_SHA", "abc")
	expect(t, Fleet(context.Background(), w.run()), false, "too short for a pin")
}

func TestReadFluxNamesTheCommit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FluxShaFile), []byte(" "+fluxSha+"\n"), 0o644)
	f, bad := ReadFlux(dir)
	if bad != nil || f.SHA != fluxSha {
		t.Errorf("%v %v", f, bad)
	}
}

func TestFleetExcludedIsTheDaggerLanesExclude(t *testing.T) {
	for rel, want := range map[string]bool{
		"a/b/c.slag": true, "x.slag": true, "stars": true, "stars/a/data.json": true, "repos/a.json": true, "repos/sub/a.json": false,
		"repos/README.md": false, "data.json": false, "topics/data.json": false, "stars.json": false,
	} {
		if FleetExcluded(rel) != want {
			t.Errorf("%s excluded = %v", rel, !want)
		}
	}
}

func TestFleetWriteFaults(t *testing.T) {
	for _, tc := range []struct{ needle, want string }{
		{".manifest", ".manifest could not be staged"},
		{"stage/fleet/data.json", "fleet/ could not be staged"},
		{"fleet/declared/data.json", "could not be staged"},
		{"planted-seams.json", "composition probe could not be written"},
		{"fleet" + MetaSuffix, "fleet.meta.json"},
	} {
		w := newWorld(t, nil)
		restore := failWrite(t, tc.needle)
		expect(t, Fleet(context.Background(), w.run()), false, tc.want)
		restore()
	}
	w := newWorld(t, nil)
	t.Setenv("ATOM_WORK", filepath.Join(t.TempDir(), "gone"))
	expect(t, Fleet(context.Background(), w.run()), false, "scratch directory could not be made")
	// An entry the flux read cannot open is the lane's.
	w = newWorld(t, nil)
	os.Remove(filepath.Join(w.flux, "prime/star-chaos.yaml"))
	os.Symlink("/nonexistent/star", filepath.Join(w.flux, "prime/star-chaos.yaml"))
	expect(t, Fleet(context.Background(), w.run()), false, "could not be read")
}
