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

func TestUT_612_TheLaneFilesParse(t *testing.T) {
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
	var names []string
	for _, p := range d.Phases {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"build", "policy", "fleet", "push"}) {
		t.Fatalf("%v", names)
	}
	by := map[string]PhaseDef{}
	for _, p := range d.Phases {
		by[p.Name] = p
	}
	// The gates the Dagger lane re-runs, the three atoms that build, and the
	// capability that touches the registry.
	if !slices.Equal(by["build"].Atoms, []string{"dies:opa-test", "dies:admission-dogfood"}) ||
		!slices.Equal(by["policy"].Atoms, []string{"bundle:policy"}) || !slices.Equal(by["fleet"].Atoms, []string{"bundle:fleet"}) {
		t.Errorf("atoms: %+v", d.Phases)
	}
	// Nothing signs: the bundles are unsigned (Rob, 2026-10-20), so no phase names a KMS.
	if _, ok := by["sign"]; ok {
		t.Errorf("the bundle lane still has a sign phase")
	}
	for _, p := range d.Phases {
		if p.Capability == "kms-sign" {
			t.Errorf("phase %s still names kms-sign", p.Name)
		}
	}
	if by["push"].Capability != "push-bundle" || !slices.Equal(by["push"].After, []string{"policy", "fleet"}) {
		t.Errorf("push: %+v", by["push"])
	}
}

// TestUT_613_ReleasePhasesNameTheFourReleaseAtoms: the atoms of the release
// phases of build and publish are real catalogue ids (the release
// stage does not exist, DEF-8-019, so they are named, not staged).
func TestUT_613_ReleasePhasesNameTheFourReleaseAtoms(t *testing.T) {
	want := []string{"go:release", "python:release", "rust:release", "ts:release"}
	// The build lane adds the per-base release of the layers that carry
	// their own: the door asks only build for a repository of bases
	// (DEF-B6-035), and each of the three atoms is absent where the tree has no
	// bases/ (its surface), so no other repository's build changes.
	withBases := append(slices.Clone(want), "bases:release", "bases:go-release")
	for l, want := range map[string][]string{"build": withBases} {
		if got := landedLanes(t)[l].Phases[0].Atoms; !slices.Equal(got, want) {
			t.Errorf("%s release: %v", l, got)
		}
	}
	// The publish lane uploads packages, not images: it builds the wheels and
	// the npm tarballs the capability uploads (python:artifacts, ts:pack).
	if got := landedLanes(t)["publish"].Phases[0].Atoms; !slices.Equal(got, []string{"python:artifacts", "ts:pack"}) {
		t.Errorf("publish release: %v", got)
	}
	// The cast is not an image's compile: it builds every binary a tree declares
	// (rust:cast-release, go:cast-release), whatever a Dockerfile copies.
	if got := landedLanes(t)["cast"].Phases[0].Atoms; !slices.Equal(got, []string{"rust:cast-release", "go:cast-release"}) {
		t.Errorf("cast release: %v", got)
	}
}

// TestLaneAtomsAreCatalogueAtoms: every atom a phase names is a row of the
// catalogue, and every stage a lane or a stage:<s> names is a stage one has.
func TestLaneAtomsAreCatalogueAtoms(t *testing.T) {
	have := portedIDs(t)
	for _, a := range checks.Atoms() {
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
		"build": "publish:ca-build-registry", "bundle": "push:ca-bundle-lane",
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
	if g := lanes["gate"]; !slices.Equal(g.Stages, []string{"precommit", "prepush"}) || g.Rider != "" || len(g.Phases) != 0 {
		t.Errorf("gate: %+v", g)
	}
	for l, stage := range map[string]string{"check": "precommit", "mutation": "mutation"} {
		if d := lanes[l]; !slices.Equal(d.Stages, []string{stage}) || len(d.Phases) != 0 || d.Rider != "" {
			t.Errorf("%s: %+v", l, d)
		}
	}
	if v := lanes["visual"]; !slices.Equal(v.Stages, []string{"visual"}) || len(v.Phases) != 2 || v.Phases[0].Atoms[0] != "stage:visual" ||
		v.Phases[1].Capability != "push-visual" || !slices.Equal(v.Phases[1].After, []string{"visual"}) || len(v.Phases[1].Secrets) != 0 || v.Rider != "" {
		t.Errorf("visual: %+v", v)
	}
	if a := lanes["atoms"]; len(a.Phases) != 1 || a.Phases[0].Capability != "build-atoms" {
		t.Errorf("atoms: %+v", a)
	}
	if d := lanes["dev"]; len(d.Phases) != 2 || d.Phases[0].Atoms[0] != "stage:dev" || d.Phases[1].Capability != "dev-output" {
		t.Errorf("dev: %+v", d)
	}
	if g := lanes["govern"]; len(g.Phases) != 1 || g.Phases[0].Name != "cast" || g.Phases[0].Capability != "layer-cast" || len(g.Phases[0].Atoms) != 0 || len(g.Phases[0].After) != 0 {
		t.Errorf("govern: %+v", g)
	}
}

// The build lane validates between assemble and scan: bases:chairman holds the
// assembled chairman layout to its fixed-file table before anything is
// scanned. The door asks the build lane for a repository of bases (DEF-B6-035).
func TestBasesPhasesValidateBetweenAssembleAndScan(t *testing.T) {
	lanes := landedLanes(t)
	for _, l := range []string{"build"} {
		d := lanes[l]
		var names []string
		by := map[string]PhaseDef{}
		for _, p := range d.Phases {
			names = append(names, p.Name)
			by[p.Name] = p
		}
		if !slices.Equal(names, []string{"release", "assemble", "validate", "scan", "publish"}) {
			t.Fatalf("%s: %v", l, names)
		}
		if !slices.Equal(by["validate"].Atoms, []string{"bases:chairman", "bases:blade"}) || !slices.Equal(by["validate"].After, []string{"assemble"}) || !slices.Equal(by["scan"].After, []string{"assemble", "validate"}) {
			t.Errorf("%s: %+v", l, d.Phases)
		}
		// DEF-B6-048: on a tree with no bases/ the validate phase's only atom is
		// ABSENT, the phase is empty and counts as done at once, so scan must name
		// assemble itself or it runs before /work/image exists; publish follows scan.
		if !slices.Contains(by["scan"].After, "assemble") || !slices.Contains(by["publish"].After, "scan") {
			t.Errorf("%s: scan must wait for assemble and publish for scan: scan.After=%v publish.After=%v", l, by["scan"].After, by["publish"].After)
		}
	}
}

func TestSplitLaneFilesNameTheirOwnLanguage(t *testing.T) {
	lanes := landedLanes(t)
	if g := lanes["gate"]; len(g.AtomLanes) != 0 {
		t.Errorf("gate narrows by atom_lanes %v; the base gate is narrowed by the marker, not its file", g.AtomLanes)
	}
	for _, lang := range []string{"go", "rust", "ts"} {
		d, ok := lanes["gate-"+lang]
		if !ok {
			t.Fatalf("no lanes/gate-%s.toml", lang)
		}
		if !slices.Equal(d.Stages, []string{"precommit", "prepush"}) || !slices.Equal(d.AtomLanes, []string{lang}) || len(d.Phases) != 0 || d.Rider != "" {
			t.Errorf("gate-%s: %+v", lang, d)
		}
	}
}
