package catalogue

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// root is the module root, two directories above this package.
const root = "../.."

// TestRegistryIsGenerated holds registry_gen.go to the tree. When it is stale
// the failure carries the regenerated file: write it over registry_gen.go.
func TestRegistryIsGenerated(t *testing.T) {
	want, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("registry_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(have, want) {
		t.Fatalf("registry_gen.go is stale. Replace it with:\n%s", want)
	}
}

// writeTree lays files (path -> body) under a temp root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGenerateSortsByIDAndReadsThePackageClause(t *testing.T) {
	files := map[string]string{}
	files["atoms/go/zed/zed.go"] = "package gozed\n"
	files["atoms/fleet/abc/abc.go"] = "package fleetabc\n"
	files["atoms/go/vet.toml"] = "a = 1\n"
	files["atoms/go/build.toml"] = "b = \"x\"\n"
	files["atoms/main.go"] = "package main\n"
	dir := writeTree(t, files)
	got, err := generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	a, b := strings.Index(s, "&fleetabc.Def"), strings.Index(s, "&gozed.Def")
	if a < 0 || b < a {
		t.Errorf("defs out of order:\n%s", s)
	}
	if bi, vi := strings.Index(s, `atoms/go/build.toml`), strings.Index(s, `atoms/go/vet.toml`); bi < 0 || vi < bi {
		t.Errorf("manifests out of order:\n%s", s)
	}
}

func TestGenerateRefusesADirectoryThatIsNoAtom(t *testing.T) {
	dir := writeTree(t, map[string]string{"atoms/go/stray/helper.go": "package stray\n"})
	if _, err := generate(dir); err == nil || !strings.Contains(err.Error(), "atoms/go/stray holds no stray.go") {
		t.Fatalf("got %v", err)
	}
}

func TestGenerateErrorsOnATreeWithoutAtoms(t *testing.T) {
	if _, err := generate(t.TempDir()); err == nil {
		t.Fatal("no atoms/ directory should be an error")
	}
	dir := writeTree(t, map[string]string{"atoms/go/bad/bad.go": "package (\n"})
	if _, err := generate(dir); err == nil {
		t.Fatal("a file with no package clause should be an error")
	}
}

// TestUT_484_EachAtomDirectoryHoldsOneFileAndOneDef: every atoms/<ns>/<name>/
// holds <name>.go, whose package is <ns><name without ->, and the registry
// carries exactly one Def per such directory.
func TestUT_484_EachAtomDirectoryHoldsOneFileAndOneDef(t *testing.T) {
	dirs, manifests, err := scan(filepath.Join(root, "atoms"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != len(Defs) || len(manifests) != len(ManifestSources) {
		t.Fatalf("%d dirs, %d defs; %d manifests, %d sources", len(dirs), len(Defs), len(manifests), len(ManifestSources))
	}
	for i, d := range dirs {
		ns, name, _ := strings.Cut(d, "/")
		pkg, err := packageOf(filepath.Join(root, "atoms", d, name+".go"))
		if err != nil {
			t.Fatal(err)
		}
		if want := ns + strings.ReplaceAll(name, "-", ""); pkg != want {
			t.Errorf("%s: package %s, want %s", d, pkg, want)
		}
		if Defs[i].ID != ns+":"+name {
			t.Errorf("%s declares %s", d, Defs[i].ID)
		}
		if err := atomkit.Validate(*Defs[i]); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}

// TestIT_285_OneFilePerIDAcrossBothKinds: no id twice among the Defs and the
// manifests, and every one is in the catalogue the rest of the module reads.
func TestIT_285_OneFilePerIDAcrossBothKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Entries() {
		if seen[e.ID] {
			t.Errorf("%s twice", e.ID)
		}
		seen[e.ID] = true
		if (e.Def == nil) == (e.Manifest == nil) {
			t.Errorf("%s: want exactly one of Def and Manifest", e.ID)
		}
		if !checks.AtomExists(e.ID) {
			t.Errorf("%s is not in the catalogue", e.ID)
		}
		if !Ported(e.ID) {
			t.Errorf("%s not found by Lookup", e.ID)
		}
	}
	if _, ok := Lookup("no:such"); ok || Ported("no:such") {
		t.Error("an unknown id was found")
	}
	if !slices.IsSortedFunc(Entries(), func(a, b Entry) int { return strings.Compare(a.ID, b.ID) }) {
		t.Error("entries are not sorted by id")
	}
}

// TestUT_490_ThePortedAtomsAreResolvedByTheCatalogue: AtomByID answers a ported
// atom with the row its file declares, and a table row still resolves.
func TestUT_490_ThePortedAtomsAreResolvedByTheCatalogue(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want checks.AtomDef
	}{
		{"fleet:check-merge-conflict", checks.AtomDef{Stage: "precommit", Lane: checks.LaneAny, Image: checks.ImageFleet}},
		{"go:gofmt", checks.AtomDef{Stage: "precommit", Lane: checks.LaneGo, Image: checks.ImageGo, NotOnForks: checks.ForkFleetStyle}},
		{"go:staticcheck", checks.AtomDef{Stage: "prepush", Lane: checks.LaneGo, Image: checks.ImageGo, NotOnForks: checks.ForkFleetStyle}},
		{"python:pip-audit", checks.AtomDef{Stage: "prepush", Lane: checks.LanePython, Image: checks.ImagePython, NeedsManifest: true}},
		{"python:ruff-check", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython, NotOnForks: checks.ForkFleetStyle}},
		{"python:ruff-format", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython, NotOnForks: checks.ForkFleetStyle}},
		{"python:forge-testkit-assertion-free", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython, NotOnForks: checks.ForkFleetStyle}},
		{"python:forge-testkit-fake-placement", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython, NotOnForks: checks.ForkFleetStyle}},
		{"python:forge-testkit-schema-budget", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython, NotOnForks: checks.ForkFleetStyle}},
		{"python:mypy", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython, NotOnForks: checks.ForkFleetStyle, NeedsManifest: true}},
		{"python:pytest", checks.AtomDef{Stage: "precommit", Lane: checks.LanePython, Image: checks.ImagePython}},
		{"ts:bun-gate", checks.AtomDef{Stage: "precommit", Lane: checks.LaneTS, Image: checks.ImageTS}},
		{"rust:cargo-fmt", checks.AtomDef{Stage: "precommit", Lane: checks.LaneRust, Image: checks.ImageRust}},
		{"ts:bun-audit", checks.AtomDef{Stage: "prepush", Lane: checks.LaneTS, Image: checks.ImageTS}},
	} {
		got := checks.AtomByID(tc.id)
		tc.want.ID, tc.want.Desc = tc.id, got.Desc
		if got != tc.want || got.Desc == "" {
			t.Errorf("%s: got %+v, want %+v", tc.id, got, tc.want)
		}
	}
	if !checks.AtomExists("fleet:check-yaml") {
		t.Error("a row still in the table no longer resolves")
	}
}

// TestDEF_B3_009_PipAuditAbsentWordingIsTheManifestSentence: the surface the
// manifest declares makes the row NeedsManifest, so the absent sentence is the
// one gate run 2386 reported (R20).
func TestDEF_B3_009_PipAuditAbsentWordingIsTheManifestSentence(t *testing.T) {
	got := checks.AbsentVerdict(checks.AtomByID("python:pip-audit")).Reason
	want := "python:pip-audit: ABSENT — no pyproject.toml at the repository root, so this repo declares no python project for it to read. The lane's lint and tests still run. Nothing was checked here and nothing needed to be."
	if got != want {
		t.Errorf("got %q", got)
	}
	// python:mypy is the other python atom whose subject is the manifest, and
	// says so; the lint atoms read files and say that. ts:bun-gate's lane is
	// declared by its root manifest alone.
	got = checks.AbsentVerdict(checks.AtomByID("python:mypy")).Reason
	if want := strings.Replace(want, "python:pip-audit", "python:mypy", 1); got != want {
		t.Errorf("python:mypy: %q", got)
	}
	got = checks.AbsentVerdict(checks.AtomByID("python:ruff-check")).Reason
	if !strings.Contains(got, "no .py file anywhere in the tree") {
		t.Errorf("python:ruff-check: %q", got)
	}
	got = checks.AbsentVerdict(checks.AtomByID("ts:bun-gate")).Reason
	if !strings.Contains(got, "no package.json at the repository root, so this repo does not build the ts lane") {
		t.Errorf("ts:bun-gate: %q", got)
	}
	got = checks.AbsentVerdict(checks.AtomByID("rust:cargo-fmt")).Reason
	if !strings.Contains(got, "so this repo does not build the rust lane") {
		t.Errorf("cargo-fmt: %q", got)
	}
}

// TestTheBinaryCarriesTheNoneToolchainAtoms: the in-process registry holds the
// toolchain-none Defs and no other, and a Check's identity gaps are filled.
func TestTheBinaryCarriesTheNoneToolchainAtoms(t *testing.T) {
	var ids []string
	for _, a := range Atoms() {
		ids = append(ids, a.ID)
	}
	var want []string
	for _, d := range Defs {
		if d.Stage != checks.StageBundle && d.Stage != checks.StageBases && (d.Toolchain == "none" || votesInBinaryByHistory[d.ID]) {
			want = append(want, d.ID)
		}
	}
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(ids, want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	if !slices.Contains(atoms.StageIDs("precommit"), "fleet:check-merge-conflict") {
		t.Error("the stage does not list the ported in-process atom")
	}
	reg, err := atoms.NewRegistry(Atoms()...)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.IDs()) != len(want) {
		t.Errorf("ids %v", reg.IDs())
	}
}

// Only fleet:witness carries a deadline that grows with the input: its worst case
// grows with the change set, and every other atom's does not.
func TestOnlyFleetWitnessCarriesADeadlineThatGrows(t *testing.T) {
	large := atoms.Input{}
	for range 50 {
		large.Changed = append(large.Changed, "a.go")
	}
	for _, a := range Atoms() {
		if a.Deadline == nil {
			continue
		}
		if grows := a.Deadline(large) > a.Deadline(atoms.Input{}); grows != (a.ID == "fleet:witness") {
			t.Errorf("%s: its deadline grows with the change set: %v", a.ID, grows)
		}
	}
}

// A Def's Deadline raises the atom's deadline over a large input and never
// lowers it below the Def's Timeout.
func TestInProcessDeadlineIsTheLongerOfTheDeadlineAndTheTimeout(t *testing.T) {
	check := func(context.Context, atomkit.In) checks.Verdict { return checks.Verdict{} }
	long := func(atomkit.In) time.Duration { return 5 * time.Minute }
	short := func(atomkit.In) time.Duration { return time.Second }
	if got := inProcess(atomkit.Def{ID: "st:x", Deadline: long, Check: check}).Deadline(atoms.Input{}); got != 5*time.Minute {
		t.Errorf("no timeout: %v", got)
	}
	if got := inProcess(atomkit.Def{ID: "st:x", Timeout: time.Minute, Deadline: long, Check: check}).Deadline(atoms.Input{}); got != 5*time.Minute {
		t.Errorf("a longer deadline: %v", got)
	}
	if got := inProcess(atomkit.Def{ID: "st:x", Timeout: time.Minute, Deadline: short, Check: check}).Deadline(atoms.Input{}); got != time.Minute {
		t.Errorf("a shorter deadline lowered the timeout: %v", got)
	}
}

func TestInProcessFillsIdentityAndKeepsTheDeadline(t *testing.T) {
	d := atomkit.Def{ID: "st:x", Stage: "precommit", Timeout: 90 * time.Second, Check: func(context.Context, atomkit.In) checks.Verdict {
		return checks.Verdict{State: 0, Result: "pass"}
	}}
	a := inProcess(d)
	v := a.Run(context.Background(), checks.AtomDef{ID: "st:x", Stage: "precommit", Lane: checks.LaneGo}, atoms.Input{})
	if v.Atom != "st:x" || v.Stage != "precommit" || v.Lane != "go" {
		t.Errorf("%+v", v)
	}
	if a.Deadline == nil || a.Deadline(atoms.Input{}) != 90*time.Second {
		t.Error("the Def's timeout is not the atom's deadline")
	}
	kept := inProcess(atomkit.Def{ID: "st:y", Check: func(context.Context, atomkit.In) checks.Verdict {
		return checks.Verdict{Atom: "a", Stage: "b", Lane: "c"}
	}})
	if v := kept.Run(context.Background(), checks.AtomDef{ID: "st:y"}, atoms.Input{}); v.Atom != "a" || v.Stage != "b" || v.Lane != "c" {
		t.Errorf("a verdict that names itself is changed: %+v", v)
	}
	if kept.Deadline != nil {
		t.Error("a Def with no timeout sets a deadline")
	}
}

func TestBuildRefusesABadManifestAndADuplicate(t *testing.T) {
	defs, sources := Defs, ManifestSources
	t.Cleanup(func() { Defs, ManifestSources = defs, sources })
	ManifestSources = []ManifestSource{{Path: "atoms/x/y.toml", Body: "schema = \"command-atom@2\"\n"}}
	if msg := panicOf(func() { build() }); !strings.Contains(msg, "manifest atoms/x/y.toml") {
		t.Errorf("a bad manifest: %q", msg)
	}
	ManifestSources = []ManifestSource{sources[0], sources[0]}
	if msg := panicOf(func() { build() }); !strings.Contains(msg, "declared twice") {
		t.Errorf("a duplicate: %q", msg)
	}
}

// A CYCLE OF after_any EDGES IS AN AUTHORING ERROR AT LOAD, like a duplicate:
// every atom on it would wait for the others to the pod's deadline. The live
// catalogue closes none.
func TestBuildRefusesAnAfterAnyCycle(t *testing.T) {
	defs, sources := Defs, ManifestSources
	t.Cleanup(func() { Defs, ManifestSources = defs, sources })
	if c := atomkit.AfterAnyCycle(defs); c != "" {
		t.Fatalf("the live catalogue closes a cycle: %s", c)
	}
	ManifestSources = nil
	Defs = []*atomkit.Def{{ID: "st:a", AfterAny: []string{"st:b"}}, {ID: "st:b", AfterAny: []string{"st:a"}}}
	if msg := panicOf(func() { build() }); msg != "catalogue: the after_any edges close a cycle: st:a -> st:b -> st:a" {
		t.Errorf("a cycle: %q", msg)
	}
}

// EVERY after_any EDGE OF THE LIVE CATALOGUE NAMES AN ATOM IT CARRIES, in the
// same stage: the planner drops an edge to an atom it does not run, so an edge
// to a misspelt id would silently order nothing.
func TestEveryAfterAnyEdgeNamesACatalogueAtomOfItsStage(t *testing.T) {
	stage := map[string]string{}
	for _, e := range Entries() {
		if e.Def != nil {
			stage[e.ID] = e.Def.Stage
		}
	}
	edges := 0
	for _, d := range Defs {
		for _, a := range d.AfterAny {
			edges++
			if s, ok := stage[a]; !ok || s != d.Stage {
				t.Errorf("%s follows %s, which is not a logic atom of stage %s", d.ID, a, d.Stage)
			}
		}
	}
	if edges == 0 {
		t.Error("no atom follows another: go:diff-coverage follows go:test-race")
	}
}

func panicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = r.(string)
		}
	}()
	f()
	return ""
}

func TestImageOfDefaultsToTheFleetLane(t *testing.T) {
	if imageOf("none") != checks.ImageFleet || imageOf("bun") != checks.ImageTS || imageOf("go") != checks.ImageGo {
		t.Error("toolchain images")
	}
}

// The fork lane (NotOnForks) of the atoms that moved into files is the one their
// table rows had: the fleet's lint set and style stand down on a fork, and the
// build, the audits and the merge-conflict scan do not.
func TestTheForkLaneOfThePortedAtomsIsUnchanged(t *testing.T) {
	for _, id := range []string{"go:gofmt", "go:vet", "go:staticcheck", "python:ruff-check", "python:ruff-format", "python:mypy",
		"python:forge-testkit-assertion-free", "python:forge-testkit-fake-placement", "python:forge-testkit-schema-budget",
		"fleet:stop-justifications", "fleet:witness"} {
		if got := checks.AtomByID(id).NotOnForks; got != checks.ForkFleetStyle {
			t.Errorf("%s: NotOnForks %q", id, got)
		}
	}
	// the mutation lane's four grade the change against the fleet's own bar
	for _, id := range []string{"go:mutation", "python:mutation", "rust:mutation", "ts:mutation"} {
		if got := checks.AtomByID(id).NotOnForks; got != checks.ForkChangeBar {
			t.Errorf("%s: NotOnForks %q", id, got)
		}
	}
	for _, id := range []string{"go:build", "rust:cargo-fmt", "rust:cargo-audit", "python:pip-audit", "python:pytest", "ts:bun-audit", "ts:bun-gate", "fleet:check-merge-conflict", "fleet:manifest-one-command", "fleet:opengrep-sast", "fleet:check-added-large-files"} {
		if got := checks.AtomByID(id).NotOnForks; got != "" {
			t.Errorf("%s stands down on a fork (%q); a fork keeps it", id, got)
		}
	}
}

// This is the catalogue as the whole module sees it, ported atoms included, so
// the stages are pinned here and not in internal/checks (which sees the table alone).
// THE STAGES, PINNED. Rob, 2026-09-17: the commit runs the basic checks and the
// language checks — format, lint, tests; the push runs the complex checks —
// staticcheck, vulncheck, build — beside mutation. A change to this table is a
// change to what a commit costs and what a push proves.
func TestTheCommitAndPushStagesHoldTheirAtoms(t *testing.T) {
	ids := func(stage string) string {
		var out []string
		for _, a := range checks.AtomsForStage(stage) {
			out = append(out, a.ID)
		}
		return strings.Join(out, ",")
	}
	commit := "fleet:check-yaml,fleet:check-added-large-files,fleet:check-merge-conflict,fleet:manifest-one-command,fleet:stop-justifications," +
		"fleet:sast-ruleset-lanes,fleet:copier-answers-intact,fleet:speckit-render-current,fleet:ourea-config-retired-keys,fleet:wit-topics,fleet:daedalus-jobs-config,fleet:lane-sa-check,fleet:pdp-wired,fleet:bg-parity,fleet:renovate-config,fleet:retired-verbs,fleet:opengrep-sast,fleet:kafka-flows,fleet:hadolint," +
		"ops:shell,ops:chezmoi,ops:yaml,ops:dup,ops:declaration,ops:specs,ops:metrics,ops:star-sa,ops:world-patches,ops:record-lanes,ops:ansible,ops:flux,ops:kube-linter," +
		"go:gofmt,go:vet,go:test," +
		"python:ruff-check,python:ruff-format,python:forge-testkit-assertion-free,python:forge-testkit-fake-placement,python:forge-testkit-schema-budget,python:mypy,python:pytest," +
		"rust:cargo-fmt,rust:cargo-clippy,rust:cargo-test," +
		"ts:bun-gate," +
		"compose:config,compose:no-tracked-secrets,compose:third-party-pins," +
		"dies:opa-test,dies:contracts,dies:contract-copies,dies:schema,dies:findings,dies:schemas,dies:wit-regenerated,dies:schema-rendered,dies:canonical,dies:refusal-codes,dies:wire-grammar,dies:resource-referents,dies:capabilities-served,dies:tool-schemas-served,law:lint,law:verb-liveness,law:budget"
	push := "fleet:renovate-lockstep,fleet:node-kinds-declared,fleet:consumed-events-emitted,ops:immutable,template:render-matrix,wit:validate,go:staticcheck,go:govulncheck,go:build,go:release,go:test-race,go:diff-coverage,python:pip-audit,python:release,rust:release,rust:cargo-audit,rust:wit-guest,rust:wit-compose,ts:bun-audit,ts:regen,ts:release," +
		"dies:admission-dogfood,dies:data-keys,dies:canary-visibility,fleet:witness,dies:grants-stamp,dies:render-author,fleet:l7-render,dies:authz-grid"
	if got := ids(checks.StagePrecommit); got != commit {
		t.Errorf("commit stage\n got %s\nwant %s", got, commit)
	}
	if got := ids(checks.StagePrepush); got != push {
		t.Errorf("push stage\n got %s\nwant %s", got, push)
	}
}

// The declared set names Defs that exist and are not already toolchain none.
func TestTheDeclaredBinaryVotersAreRealPythonAtoms(t *testing.T) {
	for id := range votesInBinaryByHistory {
		e, ok := Lookup(id)
		if !ok || e.Def == nil || e.Def.Toolchain != "python" {
			t.Errorf("%s: %+v", id, e.Def)
		}
	}
	none := atomkit.Def{ID: "st:none", Toolchain: "none"}
	py := atomkit.Def{ID: "st:py", Toolchain: "python"}
	if !votesInBinary(&none) || votesInBinary(&py) {
		t.Error("only toolchain none, or a declared id, votes in the binary")
	}
	// The bundle lane's atoms are the executor's alone.
	if votesInBinary(&atomkit.Def{ID: "bundle:policy", Stage: checks.StageBundle, Toolchain: "none"}) {
		t.Error("a bundle-stage atom votes in the atoms binary")
	}
}

// unported is the no atom that stay Dagger chains with a table row in
// checks.atomTable (DEF-B3-035, DEF-B3-044): release for each language, and the
// ones whose chain is a multi-container pipeline.
var unported = []string{}

// TestIT_285_EveryCatalogueIDIsOneFileOrTheUnportedSet (lld FN-090 step 3): the
// catalogue ids that no file declares are exactly the unported set, and every
// other id is declared by exactly one file under atoms/, a .go Def or a .toml
// manifest.
func TestIT_285_EveryCatalogueIDIsOneFileOrTheUnportedSet(t *testing.T) {
	dirs, manifests, err := scan(filepath.Join(root, "atoms"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]int{}
	for _, d := range dirs {
		files[strings.Replace(d, "/", ":", 1)]++
	}
	for _, m := range manifests {
		ns, name, _ := strings.Cut(strings.TrimPrefix(m, "atoms/"), "/")
		files[ns+":"+strings.TrimSuffix(name, ".toml")]++
	}
	var rowOnly []string
	catalogued := map[string]bool{}
	for _, a := range checks.Atoms() {
		catalogued[a.ID] = true
		switch n := files[a.ID]; {
		case n == 0:
			rowOnly = append(rowOnly, a.ID)
		case n > 1:
			t.Errorf("%s is declared by %d files", a.ID, n)
		}
	}
	for id := range files {
		if !catalogued[id] {
			t.Errorf("a file declares %s, which the catalogue does not carry", id)
		}
	}
	slices.Sort(rowOnly)
	if !slices.Equal(rowOnly, unported) {
		t.Errorf("ids with no file under atoms/ = %v, want the unported set %v", rowOnly, unported)
	}
}

// The mutation stage is four atoms, one per lane, and it is asked for BY NAME:
// the default vector (a pull's gate) must never carry one, and neither may the
// sweep. (It lived in internal/checks while the rows did; the rows are the
// atoms' own files now, so it is pinned where the whole catalogue is seen.)
func TestTheMutationStageIsFourLanesAskedForByName(t *testing.T) {
	got := checks.MutationAtoms()
	want := map[string]checks.Lane{"go:mutation": checks.LaneGo, "python:mutation": checks.LanePython, "rust:mutation": checks.LaneRust, "ts:mutation": checks.LaneTS}
	if len(got) != len(want) {
		t.Fatalf("mutation stage has %d atoms, want %d", len(got), len(want))
	}
	for _, a := range got {
		lane, ok := want[a.ID]
		if !ok || a.Lane != lane {
			t.Errorf("%s: lane %q, want a mutation atom per lane", a.ID, a.Lane)
		}
	}
	for _, a := range checks.PullPathAtoms() {
		if a.Stage == checks.StageMutation {
			t.Errorf("%s: a mutation atom in the default vector — the gate would run it", a.ID)
		}
	}
}

// pyOpsAtoms are the eight python ops and template atoms that vote in the atoms
// binary on Dagger, with the tools container's prebuilt venv, and ran on the
// executor in a bare interpreter (DEF-B3-047): pyyaml, copier and the ansible
// collections were not there.
var pyOpsAtoms = []string{
	"ops:ansible", "ops:declaration", "ops:dup", "ops:metrics", "ops:specs",
	"ops:star-sa", "fleet:lane-sa-check", "ops:world-patches", "template:render-matrix",
}

// TestDEF_B3_047_ThePythonOpsAtomsNeedThePytoolsVenv: each of the eight asks for
// the venv the executor builds from pytools/requirements.txt, and runs in lead
// (toolchain none: git there, python from the image tool) and votes in the binary.
func TestDEF_B3_047_ThePythonOpsAtomsNeedThePytoolsVenv(t *testing.T) {
	for _, id := range pyOpsAtoms {
		e, ok := Lookup(id)
		if !ok || e.Def == nil || e.Def.Toolchain != "none" || !slices.Contains(e.Def.Needs, "pytools") || !votesInBinary(e.Def) {
			t.Errorf("%s: %+v", id, e.Def)
		}
	}
}

// TestDEF_B4_WitValidateNeedsThePytoolsVenv: wit:validate runs the repository's
// own `just validate`, and a recipe that shells out to python3 (stellar-core's
// `tapes` wants jsonschema) found a bare interpreter on the executor.
func TestDEF_B4_WitValidateNeedsThePytoolsVenv(t *testing.T) {
	e, ok := Lookup("wit:validate")
	if !ok || e.Def == nil || !slices.Contains(e.Def.Needs, "pytools") {
		t.Errorf("wit:validate: %+v", e.Def)
	}
}

// TestDEF_B3_047_NoBinaryVotingPythonAtomRunsOnABareInterpreter: an atom that
// votes in the binary by history and runs python on the executor names where its
// packages come from: the venv (pytools) or uv (`uv run --with`).
func TestDEF_B3_047_NoBinaryVotingPythonAtomRunsOnABareInterpreter(t *testing.T) {
	for id := range votesInBinaryByHistory {
		e, ok := Lookup(id)
		if !ok || e.Def == nil {
			t.Errorf("%s: no Def", id)
			continue
		}
		if !slices.Contains(e.Def.Needs, "pytools") && !slices.Contains(e.Def.Needs, "tool:uv") {
			t.Errorf("%s votes in the binary and needs neither pytools nor tool:uv: %v", id, e.Def.Needs)
		}
	}
}
