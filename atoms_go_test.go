package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE EXEMPLAR TESTS for a lane, against the paper engine (engine_fake_test.go).
// Each atom gets: its happy path, the decision(s) it makes about the tool's
// answer, and the engine-error path. Read the chain back to hold the module's
// own rules — the image, the caches, which exec carries expect:ANY.

func TestGoLaneIsAbsentWithoutAGoMod(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": ""})
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != "absent" || !strings.Contains(v.Reason, "no go.mod") {
		t.Errorf("want absent without go.mod, got %+v", v)
	}
	if len(engine.chains()) != 1 {
		t.Errorf("an absent lane must cost one entries read, not a container: %d queries\n%s", len(engine.chains()), strings.Join(engine.chains(), "\n"))
	}
}

// Rule 8: GATE_BASE reaches only the atoms that judge the change — the
// witness, dies:grants-stamp (its checker compares the rendered documents with
// the merge base's), the mutation lane, go:diff-coverage, whose diff is asked
// on a container of its own, and ops:immutable, which renders the tree at the
// pull's base (its environment hands it the base). Every other atom's cache key
// is a function of the tree alone.
func TestOnlyWitnessAndMutationReadGateBase(t *testing.T) {
	for _, a := range checks.Atoms() {
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, a.ID, "base-sha")
		reads := false
		for _, q := range engine.chains() {
			if strings.Contains(q, `GATE_BASE`) {
				reads = true
			}
		}
		want := a.ID == "fleet:witness" || a.ID == "dies:grants-stamp" || a.ID == "go:diff-coverage" || a.ID == "ops:immutable" || a.Stage == checks.StageMutation
		if reads != want {
			t.Errorf("%s reads GATE_BASE=%v, want %v", a.ID, reads, want)
		}
	}
}

// Every atom answers a verdict for its own id against a tree that declares
// every lane, and never a panic — the paper engine answers exit 0 for every
// exec, so this is each atom's happy path end to end.
func TestEveryAtomAnswersForItsOwnID(t *testing.T) {
	for _, a := range checks.Atoms() {
		engine.reset()
		engine.withTree(everyLaneTree)
		v := runAtom(t, a.ID, "")
		if v.Atom != a.ID {
			t.Errorf("%s answered as %s", a.ID, v.Atom)
		}
		if v.Stage != a.Stage || v.Lane != string(a.Lane) {
			t.Errorf("%s: verdict carries stage %q lane %q", a.ID, v.Stage, v.Lane)
		}
	}
}

// Rule 7, on the wire: no exec anywhere runs a shell.
func TestNoAtomExecsAShell(t *testing.T) {
	for _, a := range checks.Atoms() {
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, a.ID, "")
		for _, q := range engine.chains() {
			if strings.Contains(q, `args:["sh","-c"`) || strings.Contains(q, `args:["bash","-c"`) {
				t.Errorf("%s execs a shell:\n%s", a.ID, q)
			}
		}
	}
}

// ── every module, not only the root's (2026-09-16, Rob: fleet wide) ─────────

// nestedTree is foundry-stocks' shape: no root go.mod, a module one directory
// down, and a vendored go.mod that must not count.
var nestedTree = map[string]string{
	"tools/forge/go.mod":          "module forge\n\ngo 1.26\n",
	"tools/forge/main.go":         "package main\n",
	"tools/forge/main_test.go":    "package main\n",
	"tools/forge/vendor/x/go.mod": "module x\n",
	"bases/x/testdata/go.mod":     "module fixture\n",
	"README.md":                   "",
}

func TestGoLaneRunsInANestedModuleWithNoRootGoMod(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	wantState(t, v, 0, "go:vet: PASS in 1 Go module (tools/forge)")
	c := engine.chain(`"go","vet"`, "exitCode")
	wantCalls(t, c,
		[]string{"withWorkdir", `path:"/src/tools/forge"`},
		[]string{"withExec", `expect:ANY`, `args:["go","vet","./..."]`},
	)
	// the toolchain's preflight runs in the same module directory
	wantCalls(t, engine.chain(`"go","mod","download"`, "exitCode"), []string{"withWorkdir", `path:"/src/tools/forge"`})
	if strings.Contains(c, "vendor") || strings.Contains(c, "testdata") {
		t.Errorf("a vendored or testdata go.mod is not a module:\n%s", c)
	}
	globs := 0
	for _, q := range engine.chains() {
		if strings.Contains(q, "glob(") {
			globs++
		}
	}
	if globs != 1 {
		t.Errorf("the lane check and the atom must share one read of the module list, got %d globs", globs)
	}
}

func TestGoLaneIsAbsentWhenEveryGoModIsOneGoWouldNotBuild(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": "", "vendor/x/go.mod": "", "a/testdata/go.mod": "", "_old/go.mod": "", ".tools/go.mod": ""})
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:build")
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != "absent" || !strings.Contains(v.Reason, "no go.mod anywhere in the tree") {
		t.Errorf("want absent, got %+v", v)
	}
	if engine.chain(`"go","build"`) != "" {
		t.Error("an absent lane builds no container")
	}
}

func TestTheGoLaneCannotRunWhenTheTreeCannotBeEnumerated(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	engine.fail("glob(", "engine went away")
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	wantState(t, v, 2, "could not enumerate the tree's Go modules", "engine went away")
	wantState(t, runAtom(t, "go:build", ""), 2, "could not enumerate the tree's Go modules", "engine went away")
}

// Called without the lane check (a +check reaches the runner through
// verdictFor, but the runner does not rely on it), an atom on a tree with no
// module still stands down rather than building a container.
func TestAGoAtomOnATreeWithNoModuleIsAbsent(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": ""})
	v := runAtom(t, "go:vet", "")
	if v.Result != "absent" || engine.chain(`"go","vet"`) != "" {
		t.Errorf("want absent and no container, got %+v", v)
	}
}

func TestLanesNamesTheNestedModulesTheGateRuns(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	engine.withTree(map[string]string{"package.json": "{}"})
	out, err := (&FoundryTools{Source: dag.Directory()}).Lanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if out != "go (go.mod in tools/forge)\nts (package.json)\n" {
		t.Errorf("Lanes = %q", out)
	}
	engine.reset()
	engine.withTree(map[string]string{"README.md": ""})
	out, _ = (&FoundryTools{Source: dag.Directory()}).Lanes(t.Context())
	if !strings.Contains(out, "declares no lane") {
		t.Errorf("Lanes on a tree with no lane = %q", out)
	}
}

// cmdMain is the main package a COPY release/<name> is built from.
func cmdMain(names ...string) map[string]string {
	tree := map[string]string{}
	for _, n := range names {
		tree["cmd/"+n+"/main.go"] = "package main\n"
	}
	return tree
}

// copiesHades is the three-line shape: a Dockerfile that asks for the Gate's
// artifact by copying it from release/ (buildlane.CopiesRelease).
const copiesHades = "FROM x\nCOPY release/hades /hades\n"

// THE RELEASE BUILD IS THE IMAGE'S COMPILE, derived: the binaries the
// Dockerfile copies out of release/, each from ./cmd/<name>, with the fleet's
// flags.
func TestGoReleaseBuildsWhatTheImageWillCarry(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n"})
	engine.withTree(cmdMain("hades"))
	wantState(t, runAtom(t, "go:release", ""), 0, "release build: hades", "the Dockerfile's COPY lines")
	wantCalls(t, engine.chain(`"go","build","-trimpath"`, "exitCode"),
		[]string{"withEnvVariable", `name:"CGO_ENABLED"`, `value:"0"`},
		[]string{"withExec", `expect:ANY`, `args:["go","build","-trimpath","-ldflags=-s -w","-o","/out/hades","./cmd/hades"]`},
	)

	// VENDOR IS READ OFF THE TREE, not declared: a tracked vendor/ is the flag.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/ourea /ourea\n",
		".copier-answers.yml": "service_name: ourea\n",
		"vendor/modules.txt":  "# x\n",
	})
	engine.withTree(cmdMain("ourea"))
	wantState(t, runAtom(t, "go:release", ""), 0, "-mod=vendor")
	wantCalls(t, engine.chain(`"-o","/out/ourea"`, "exitCode"),
		[]string{"withExec", `args:["go","build","-mod=vendor","-trimpath","-ldflags=-s -w","-o","/out/ourea","./cmd/ourea"]`})
	// AND NOTHING IS DOWNLOADED: a vendored release build reaches no proxy and
	// no door — ourea's image must build with the door down, and the door is
	// where GONOPROXY sends its first-party module (ourea
	// TestImageBuildsWithoutReachingTheDoor, which now guards the release path).
	if strings.Contains(engine.chain(`"-o","/out/ourea"`, "exitCode"), `"go","mod","download"`) {
		t.Errorf("a vendored release build ran go mod download:\n%s", engine.chain(`"-o","/out/ourea"`, "exitCode"))
	}
	// An unvendored module is still provisioned by the download.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n"})
	engine.withTree(cmdMain("hades"))
	wantState(t, runAtom(t, "go:release", ""), 0, "release build: hades")
	if !strings.Contains(engine.chain(`"-o","/out/hades"`, "exitCode"), `"go","mod","download"`) {
		t.Errorf("an unvendored release build skipped go mod download:\n%s", engine.chain(`"-o","/out/hades"`, "exitCode"))
	}

	// The Dockerfile copies more than one, and each gets its own exec.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/blade-runner /blade-runner\nCOPY release/blade-controller /blade-controller\n",
		".copier-answers.yml": "service_name: blade-runner\n",
	})
	engine.withTree(cmdMain("blade-runner", "blade-controller"))
	wantState(t, runAtom(t, "go:release", ""), 0, "blade-controller, blade-runner", "the Dockerfile's COPY lines")
	for _, b := range []string{"blade-controller", "blade-runner"} {
		if engine.chain(`"-o","/out/`+b+`"`) == "" {
			t.Errorf("no exec builds %s:\n%v", b, engine.chains())
		}
	}
}

// A REPOSITORY WITH DOCKERFILES AND NO STAR IS NOT A STAR IMAGE — ABSENT, never
// a could-not-run. foundry-stocks measured it (Wonka17, 2026-09-17, nine gate
// runs on #205): it ships the CI bases and forge-tools, is not copier-templated,
// and the first cut wedged every landing there over "names no star".
func TestGoReleaseIsAbsentForARepoThatNamesNoStar(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	v := runAtom(t, "go:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "names no star") || !strings.Contains(v.Reason, "not a star image") {
		t.Errorf("want an absent 0 saying it is not a star image, got %+v", v)
	}
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("nothing compiles for a repo that is not a star:\n%v", engine.chains())
	}
	// Its Dockerfile did not copy from release/ either — and the reason is
	// still "names no star": the star is read first, so a non-star says so.
	if strings.Contains(v.Reason, "compiles itself") {
		t.Errorf("a non-star was told it compiles itself: %s", v.Reason)
	}
	// A .copier-answers.yml that names nothing is the same absence.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n", ".copier-answers.yml": "project: x\n"})
	if v := runAtom(t, "go:release", ""); v.Result != "absent" || !strings.Contains(v.Reason, "no service_name") {
		t.Errorf("%+v", v)
	}
}

// A DOCKERFILE THAT COMPILES ITSELF ASKS FOR NO RELEASE BUILD — ABSENT, the
// build lane's own reading (buildlane.CopiesRelease, build.go stageRelease:
// "built exactly as before"). narcissus is the case (foundry-tools #10307,
// narrowed 2026-09-18): tree-sitter through cgo, compiled statically in its
// own build stage, so the CGO_ENABLED=0 compile here would red a binary its
// image never carries.
func TestGoReleaseIsAbsentForADockerfileThatCompilesItself(t *testing.T) {
	self := "FROM docker.notusmi.com/library/golang:1.27 AS build\nRUN CGO_ENABLED=1 go build -o /out/hades ./cmd/hades\nFROM x\nCOPY --from=build /out/hades /hades\n"
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": self, ".copier-answers.yml": "service_name: hades\n"})
	v := runAtom(t, "go:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "compiles itself") || !strings.Contains(v.Reason, "release/") {
		t.Errorf("want an absent 0 saying the image compiles itself, got %+v", v)
	}
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("nothing compiles for a Dockerfile that compiles itself:\n%v", engine.chains())
	}

	// A second Dockerfile that does copy from release/ asks on the repo's
	// behalf: the compile runs.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": self, "docker/Dockerfile.hades": copiesHades,
		".copier-answers.yml": "service_name: hades\n",
	})
	engine.withTree(cmdMain("hades"))
	wantState(t, runAtom(t, "go:release", ""), 0, "release build: hades")
	if engine.chain(`"go","build","-trimpath"`) == "" {
		t.Errorf("a Dockerfile that copies from release/ asked and nothing compiled:\n%v", engine.chains())
	}

	// A Dockerfile that cannot be read is a could-not-run that names it, never
	// an absence read off a fault — and the compile does not run on top of it.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": self, ".copier-answers.yml": "service_name: hades\n"})
	engine.failLeaf(`file(path:"Dockerfile")`, "contents", "the file went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "Dockerfile could not be read", "the file went away")
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("a Dockerfile that could not be read still compiled:\n%v", engine.chains())
	}
}

// A REPO THAT SHIPS NO IMAGE IS ABSENT, NEVER A COULD-NOT-RUN. foundry-tools
// is the case that measured it: no Dockerfile, no image, no release build —
// and the first cut reded its own gate over a build nobody asked for.
func TestGoReleaseIsAbsentWhereThereIsNoImage(t *testing.T) {
	engine.reset()
	// A Go module and no Dockerfile: foundry-tools' own shape.
	engine.withTree(map[string]string{"go.mod": "module x\n", "main.go": "package main\n"})
	v := runAtom(t, "go:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "tracks no Dockerfile") {
		t.Errorf("want an absent 0 naming the missing Dockerfile, got %+v", v)
	}
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("nothing compiles for a repo that ships no image:\n%v", engine.chains())
	}
}

// EVERY WAY THE RELEASE BUILD CANNOT ANSWER IS A COULD-NOT-RUN THAT SAYS WHY,
// and none of them is a pass: a tree it cannot read, a module walk it cannot
// do, a repo with no root module, and a record whose binaries are unusable.
func TestGoReleaseSaysWhyItCouldNotRun(t *testing.T) {
	image := map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n", "cmd/hades/main.go": "package main\n"}

	// The tree itself could not be read: the population is the first thing it asks for.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(image)
	engine.fail("glob", "the tree went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "the tree could not be read", "the tree went away")

	// The module walk failed, so nothing knows whether there is a root module.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(image)
	engine.failLeaf("**/go.mod", "glob", "the module walk went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "Go modules")

	// A module somewhere, but not at the root: no star binary to build.
	engine.reset()
	engine.withTree(map[string]string{
		"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n",
		"tools/go.mod": "module x\n", "cmd/hades/main.go": "package main\n",
	})
	wantState(t, runAtom(t, "go:release", ""), 2, "no go.mod at the repository root")

	// The Dockerfile copies something that is not a binary name.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": "FROM x\nCOPY release/-flag /hades\n", ".copier-answers.yml": "service_name: hades\n",
	})
	wantState(t, runAtom(t, "go:release", ""), 2, "is not a binary name")

	// A COPY with no main package behind it ships a half image: named, not left
	// to the compiler.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": "FROM x\nCOPY release/hades /hades\nCOPY release/ghost /ghost\n", ".copier-answers.yml": "service_name: hades\n",
	})
	engine.withTree(cmdMain("hades"))
	wantState(t, runAtom(t, "go:release", ""), 2, "copies release/ghost", "no cmd/ghost")

	// The tree cannot be probed for the main package a COPY names.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n"})
	engine.withTree(cmdMain("hades"))
	engine.failLeaf("DIRECTORY_TYPE", "exists", "the tree went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "the tree could not be read for cmd/hades", "the tree went away")

	// A Dockerfile that asks for release/ and copies nothing out of it.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": "FROM x\nCOPY release/ /app\n", ".copier-answers.yml": "service_name: hades\n",
	})
	wantState(t, runAtom(t, "go:release", ""), 2, "copies no file out of it")
}

// A REPOSITORY WITH NO ANSWERS FILE IS NAMED BY ITS RECORD (CA F17, Rob
// 2026-09-19). hephaestus is not copier-templated and carries no
// .copier-answers.yml, but foundry-dies holds fleet/repos/hephaestus.json
// saying repo rob/hephaestus — the record the door routes its lanes by. The
// release build reads the same name off the same record, keyed by the clone
// URL the door (--repo) or the hook (--origin) named; a run given neither, or
// a repo no record names, is still "not a star".
func TestGoReleaseNamesAStarWithNoAnswersFileByItsRecord(t *testing.T) {
	// everyLaneTree carries an answers file (critical_modules, no
	// service_name); this repository carries none at all.
	noAnswers := map[string]string{}
	for k, v := range everyLaneTree {
		if k != ".copier-answers.yml" {
			noAnswers[k] = v
		}
	}
	dies := map[string]string{
		"/dies/fleet/repos/hephaestus.json":  `{"name":"hephaestus","repo":"rob/hephaestus"}`,
		"/dies/fleet/repos/tron.json":        `{"name":"tron","repo":"rob/tron"}`,
		"/dies/fleet/repos/base-images.json": `{"name":"base-images","repo":"foundry/base-images"}`,
	}
	// The door's Job: the tree was fetched from --repo.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(cmdMain("hephaestus", "base-images"))
	engine.withTree(dies)
	v := registry["go:release"](context.Background(), newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", ""))
	wantState(t, v, 0, "release build: hephaestus", "the Dockerfile's COPY lines")
	if engine.chain(`"-o","/out/hephaestus"`) == "" {
		t.Errorf("the record's name did not reach the compile:\n%v", engine.chains())
	}

	// The hook: the tree is a snapshot, and --origin names the clone.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(cmdMain("hephaestus"))
	engine.withTree(dies)
	r := newRun(dag.Directory(), "", "").fromOrigin("http://ourea.notusmi.com:8215/hephaestus.git")
	wantState(t, registry["go:release"](context.Background(), r), 0, "release build: hephaestus")

	// An owner-qualified clone matches an owner-qualified record.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/base-images /base-images\n"})
	engine.withTree(cmdMain("base-images"))
	engine.withTree(dies)
	r = newRun(dag.Directory(), "https://git.notusmi.com/foundry/base-images.git", "")
	wantState(t, registry["go:release"](context.Background(), r), 0, "release build: base-images")

	// No record names this repo: not a star, an absence that says which key it looked for.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	engine.withTree(dies)
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/nobody.git", "")
	v = registry["go:release"](context.Background(), r)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no record in foundry-dies names repo rob/nobody") {
		t.Errorf("%+v", v)
	}

	// No clone URL at all: nothing to look a record up by.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	engine.withTree(dies)
	v = runAtom(t, "go:release", "")
	if v.Result != "absent" || !strings.Contains(v.Reason, "no clone URL to find a record by") {
		t.Errorf("%+v", v)
	}

	// The records could not be listed or read: could-not-run, never "no star".
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	engine.fail(`glob(pattern:"fleet/repos/*.json")`, "the dies went away")
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", "")
	wantState(t, registry["go:release"](context.Background(), r), 2, "could not be listed", "the dies went away")
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	engine.failLeaf(`file(path:"fleet/repos/base-images.json")`, "contents", "the record went away")
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", "")
	wantState(t, registry["go:release"](context.Background(), r), 2, "could not be read", "the record went away")

	// The Rust atom reads the same name the same way, and files the same
	// could-not-run when the records cannot be listed.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	engine.fail(`glob(pattern:"fleet/repos/*.json")`, "the dies went away")
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", "")
	wantState(t, registry["rust:release"](context.Background(), r), 2, "rust:release: CANNOT RUN - the fleet's records could not be listed", "the dies went away")
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/tron /tron\n"})
	engine.withTree(dies)
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/tron.git", "")
	wantState(t, registry["rust:release"](context.Background(), r), 0, "release build: tron")
}
