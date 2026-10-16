package atomkit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// landedLanes parses every lanes/*.toml of the repository, by file base name.
func landedLanes(t *testing.T) map[string]LaneDef {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot, "lanes", "*.toml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no lane files: %v", err)
	}
	out := map[string]LaneDef{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := ParseLane(raw)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".toml")] = d
	}
	return out
}

func TestUT_612_TheFourteenLaneFilesParse(t *testing.T) {
	lanes := landedLanes(t)
	if len(lanes) != len(KnownLanes) {
		t.Fatalf("%d lane files, want %d", len(lanes), len(KnownLanes))
	}
	for _, l := range KnownLanes {
		if d, ok := lanes[l]; !ok || d.Lane != l {
			t.Errorf("lane %s: %+v", l, d)
		}
	}
}

func TestUT_614_BundlePhases(t *testing.T) {
	d := landedLanes(t)["bundle"]
	if len(d.Phases) != 3 {
		t.Fatalf("%+v", d.Phases)
	}
	sign, push := d.Phases[1], d.Phases[2]
	if sign.Name != "sign" || sign.Capability != "kms-sign" || !slices.Equal(sign.After, []string{"build"}) {
		t.Errorf("sign: %+v", sign)
	}
	if push.Name != "push" || push.Capability != "push-bundle" || !slices.Equal(push.After, []string{"sign"}) {
		t.Errorf("push: %+v", push)
	}
}

// TestUT_613_ReleasePhasesNameTheFourReleaseAtoms: the atoms of the release
// phases of build, bases and publish are real catalogue ids (the release
// stage does not exist, DEF-8-019, so they are named, not staged).
func TestUT_613_ReleasePhasesNameTheFourReleaseAtoms(t *testing.T) {
	want := []string{"go:release", "python:release", "rust:release", "ts:release"}
	for _, l := range []string{"build", "bases", "publish"} {
		if got := landedLanes(t)[l].Phases[0].Atoms; !slices.Equal(got, want) {
			t.Errorf("%s release: %v", l, got)
		}
	}
	if got := landedLanes(t)["cast"].Phases[0].Atoms; !slices.Equal(got, []string{"rust:release"}) {
		t.Errorf("cast release: %v", got)
	}
}

// TestLaneAtomsAreCatalogueAtoms: every atom a phase names is a row of the
// catalogue, and every stage a lane or a stage:<s> names is a stage one has.
// govern:resolve-tags is the one the catalogue does not hold yet (DEF-B3-002).
func TestLaneAtomsAreCatalogueAtoms(t *testing.T) {
	have := map[string]bool{"govern:resolve-tags": true}
	for _, a := range checks.Atoms {
		have[a.ID] = true
	}
	for name, d := range landedLanes(t) {
		for _, p := range ExpandPhases(d) {
			for _, a := range p.Atoms {
				if s, ok := strings.CutPrefix(a, "stage:"); ok {
					if !slices.Contains(knownStages, s) {
						t.Errorf("%s: phase %s names stage %s", name, p.Name, s)
					}
				} else if !have[a] {
					t.Errorf("%s: phase %s names %s, which is no atom", name, p.Name, a)
				}
			}
		}
	}
}

func TestEachSecretIsNamedOnce(t *testing.T) {
	want := map[string]string{
		"build": "publish:ca-build-registry", "bases": "publish:ca-build-registry", "bundle": "push:ca-bundle-lane",
		"publish": "publish:ca-publish-lane", "cast": "cast:ca-cast-lane", "govern": "cast:ca-cast-lane",
	}
	for name, d := range landedLanes(t) {
		var got []string
		for _, p := range d.Phases {
			for _, s := range p.Secrets {
				got = append(got, p.Name+":"+s)
			}
		}
		if (want[name] == "") != (len(got) == 0) || (len(got) > 0 && !slices.Equal(got, []string{want[name]})) {
			t.Errorf("%s: secrets %v, want %q", name, got, want[name])
		}
	}
}

func TestLaneShapes(t *testing.T) {
	lanes := landedLanes(t)
	if g := lanes["gate"]; !slices.Equal(g.Stages, []string{"precommit", "prepush"}) || g.Rider != "orbit" || len(g.Phases) != 0 {
		t.Errorf("gate: %+v", g)
	}
	for l, stage := range map[string]string{"check": "precommit", "orbit": "orbit", "visual": "visual", "mutation": "mutation", "mutation-bg": "mutation"} {
		if d := lanes[l]; !slices.Equal(d.Stages, []string{stage}) || len(d.Phases) != 0 || d.Rider != "" {
			t.Errorf("%s: %+v", l, d)
		}
	}
	if a := lanes["atoms"]; len(a.Phases) != 1 || a.Phases[0].Capability != "build-atoms" {
		t.Errorf("atoms: %+v", a)
	}
	if d := lanes["dev"]; len(d.Phases) != 2 || d.Phases[0].Atoms[0] != "stage:dev" || d.Phases[1].Capability != "dev-output" {
		t.Errorf("dev: %+v", d)
	}
	if g := lanes["govern"]; len(g.Phases) != 2 || g.Phases[0].Atoms[0] != "govern:resolve-tags" || g.Phases[1].Capability != "layer-cast" {
		t.Errorf("govern: %+v", g)
	}
}
