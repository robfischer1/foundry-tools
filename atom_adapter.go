package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE DAGGER ADAPTER (lld.md FN-087). An atom that lives in its own file
// (atoms/<ns>/<name>/<name>.go, or atoms/<ns>/<name>.toml) is run by this file
// and by the in-pod executor, and by nothing else: the chain it replaced is
// deleted, so no atom exists twice (R35, R36).
//
//   - A LOGIC atom is compiled from its file with the stub (atomkit.Stub), in the
//     go toolchain container, from the module's source; the engine caches the
//     build by that source. It then runs in the container its chain ran in (the
//     atom's ENVIRONMENT, atom_env.go) with ATOM_OUT naming where it writes its
//     verdict, and the verdict is read back from there.
//   - A COMMAND atom is its manifest's one command, run per module directory by
//     atomkit.RunCommand, after the toolchain's preflight from tools.toml.
//
// The atoms of toolchain none also vote in the atoms binary's one process
// (catalogue.Atoms); the adapter is their re-ask when that binary could not run
// them, and the way `dagger check <atom>` and the local hook reach them.

func init() {
	for _, e := range catalogue.Entries() {
		register(e.ID, adapterFor(e.ID))
	}
}

// adapterFor is the runner registered for a ported atom's id.
func adapterFor(id string) atomFn {
	return func(ctx context.Context, r *run) checks.Verdict {
		e, ok := catalogue.Lookup(id)
		if !ok {
			return atomkit.CannotRun(id, "", checks.LaneAny, "atom "+id+" is not in the generated registry")
		}
		return runAtomOnDagger(ctx, r, e)
	}
}

// runAtomOnDagger runs one ported atom over the run's tree and answers its
// verdict. It never returns an error: an atom that could not run answers
// state 2 and says why.
func runAtomOnDagger(ctx context.Context, r *run, e catalogue.Entry) checks.Verdict {
	if e.Manifest != nil {
		return runManifest(ctx, r, e)
	}
	return runLogic(ctx, r, e)
}

// atomStubInclude is the source one atom is compiled from: go.mod, go.sum, the
// atom's own directory, the module's packages every stub imports and the ones
// this atom's family imports besides (atomLibraries). An edit to another atom
// does not key this build. TestAtomStubSourceCoversImports holds the list to the
// import closure in both directions.
func atomStubInclude(importPath string) []string {
	rel := strings.TrimPrefix(importPath, "dagger/foundry-tools/")
	own := rel + "/**"
	id := strings.Replace(strings.TrimPrefix(rel, "atoms/"), "/", ":", 1)
	return append(append([]string{"go.mod", "go.sum", own}, atomRuntimeDirs...), atomLibraries[id]...)
}

// atomRuntimeDirs are the module's packages every stub imports besides the
// atom's own: atomkit and what it and internal/atoms import in turn.
var atomRuntimeDirs = []string{
	"internal/atomkit/**",
	"internal/atoms/**",
	"internal/checks/**",
	"internal/execmem/**",
	"internal/pgroupps/**",
	"internal/govlaw/**",
	"internal/unitkey/**",
	"internal/retiredverbs/**",
}

// The packages the atoms of a family share (internal/atomlib).
var (
	libraryDirs = []string{"internal/atomlib/**"}
	// witnessDirs: fleet:witness asks as the pod's SVID in its own process.
	witnessDirs = []string{"internal/hadescall/**", "internal/witnesscall/**"}
	// witreplayDirs: rust:wit-compose and dev:rust-replay are one body.
	witreplayDirs = []string{"internal/witreplay/**"}
	// releaseDirs are what the release atoms share: the release build's one
	// implementation and the Dockerfile's reading of release/; the Rust compile
	// adds the copy that carries a binary out of its target.
	releaseDirs     = []string{"internal/releasekit/**", "internal/buildlane/**"}
	rustReleaseDirs = []string{"internal/releasekit/**", "internal/buildlane/**", "internal/copyout/**"}
	// castReleaseDirs are what the cast lane's two compile atoms share: the
	// compile itself, the cast's decisions about the tree, the release
	// directory's resolution and the exit vocabulary, and the copy that carries
	// a binary out of its target.
	castReleaseDirs = []string{"internal/castrelease/**", "internal/castlane/**", "internal/releasekit/**", "internal/buildlane/**", "internal/copyout/**"}
	// publishPackDirs are what the publish lane's two build atoms share: the
	// builds, the lane's decisions (artifact names, package selection), the
	// release directory's resolution and the exit vocabulary.
	publishPackDirs = []string{"internal/publishpack/**", "internal/publishlane/**", "internal/releasekit/**", "internal/buildlane/**"}
	// bundleDirs are what the bundle lane's atoms share: the build and its
	// gates, the lane's decisions about the dies and the KMS re-sign's byte work.
	bundleDirs = []string{"internal/bundlekit/**", "internal/bundlelane/**", "internal/bundleresign/**"}
)

// atomLibraries are the packages an atom imports that not every atom does, by
// atom id. An atom that is not here imports nothing beyond atomRuntimeDirs.
var atomLibraries = map[string][]string{
	"go:mutation":                {"internal/gomut/**"},
	"python:mutation":            {"internal/pymut/**"},
	"rust:mutation":              {"internal/rustmut/**"},
	"ts:mutation":                {"internal/tsmut/**"},
	"compose:config":             libraryDirs,
	"fleet:witness":              witnessDirs,
	"compose:no-tracked-secrets": libraryDirs,
	"compose:third-party-pins":   libraryDirs,
	"ops:ansible":                libraryDirs,
	"ops:chezmoi":                libraryDirs,
	"ops:declaration":            libraryDirs,
	"ops:dup":                    libraryDirs,
	"ops:flux":                   libraryDirs,
	"ops:metrics":                libraryDirs,
	"ops:record-lanes":           libraryDirs,
	"ops:shell":                  libraryDirs,
	"ops:specs":                  libraryDirs,
	"ops:star-sa":                libraryDirs,
	"ops:world-patches":          libraryDirs,
	"ops:yaml":                   libraryDirs,
	"go:release":                 releaseDirs,
	"bundle:policy":              bundleDirs,
	"bundle:fleet":               bundleDirs,
	"bundle:resign":              bundleDirs,
	"go:cast-release":            castReleaseDirs,
	"python:artifacts":           publishPackDirs,
	"ts:pack":                    publishPackDirs,
	"rust:cast-release":          castReleaseDirs,
	"python:release":             releaseDirs,
	"rust:release":               rustReleaseDirs,
	"ts:release":                 releaseDirs,
	"bases:release":              releaseDirs,
	"bases:chairman":             {"internal/chairmanfixed/**"},
	"bases:go-release":           releaseDirs,
	"template:render-matrix":     libraryDirs,
	"ts:regen":                   {"internal/regenlane/**", "internal/regenkit/**"},
	"dev:ts-regen":               {"internal/regenlane/**", "internal/regenkit/**"},
	"dev:go-regen":               {"internal/goregen/**", "internal/regenkit/**"},
	"rust:wit-compose":           witreplayDirs,
	"dev:rust-replay":            witreplayDirs,
	"ts:visual":                  {"internal/visuallane/**", "internal/buildlane/**"},
	"wit:validate":               libraryDirs,
}

// atomStubDir is where the stub sits in the module, the place daedalus'
// atombuild puts it too, so both runners build the same main package path and
// -trimpath makes the bytes the same. The leading underscore keeps it out of ./...
func atomStubDir(id string) string { return "_atoms/" + strings.Replace(id, ":", "/", 1) }

// atomCompile builds a logic atom's binary: the stub in _atoms/<ns>/<name>,
// compiled with the reproducible flags (-trimpath, no VCS stamp, no build id)
// and the environment the pod's builder uses, so the bytes are those it makes.
// The exec carries expect:ANY: a compile failure is the atom's could-not-run,
// read off the exit code.
func atomCompile(e catalogue.Entry) *dagger.Container {
	dir := atomStubDir(e.ID)
	src := dag.CurrentModule().Source().
		Filter(dagger.DirectoryFilterOpts{Include: atomStubInclude(e.ImportPath), Exclude: []string{"**/*_test.go"}}).
		WithNewFile(dir+"/main.go", string(atomkit.Stub(e.ImportPath)))
	return goToolchain().
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", "amd64").
		WithEnvVariable("GOFLAGS", "-mod=mod").
		WithMountedDirectory("/m", src).
		WithWorkdir("/m").
		WithExec([]string{"go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", "-o", "/out/atom", "./" + dir}, anyExit)
}

// runLogic compiles a logic atom and runs it in its environment.
func runLogic(ctx context.Context, r *run, e catalogue.Entry) checks.Verdict {
	ran, out, v := logicRan(ctx, r, e)
	if v != nil {
		return *v
	}
	return readVerdict(ctx, checks.AtomByID(e.ID), ran, out)
}

// logicRan compiles a logic atom and starts it in its environment, over /src and
// the run's arguments (a dev atom's; none for the gate's). It answers the
// container after the atom, which holds the tree the atom wrote, and the path of
// its verdict; or, for an atom that did not compile, the verdict that says so.
func logicRan(ctx context.Context, r *run, e catalogue.Entry) (*dagger.Container, string, *checks.Verdict) {
	a := checks.AtomByID(e.ID)
	built := atomCompile(e)
	code, err := exitCodeOf(ctx, built)
	if err != nil {
		v := cannotRun(a, "atom "+e.ID+" did not compile: "+err.Error())
		return nil, "", &v
	}
	if code != 0 {
		stderr, _ := built.Stderr(ctx)
		v := cannotRun(a, "atom "+e.ID+" did not compile: "+lastLines(strings.TrimSpace(stderr), 10))
		return nil, "", &v
	}

	out := "/work/out/" + atomkit.EscapeID(e.ID) + ".json"
	ran := r.atomEnvironment(ctx, e).
		WithFile("/atom", built.File("/out/atom"), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEnvVariable("ATOM_OUT", out).
		WithExec(append([]string{"/atom", "/src"}, r.args...), anyExit)
	return ran, out, nil
}

// readVerdict reads the verdict an atom wrote at out. An atom that wrote none
// could not judge: the exit it left and the last line it said are the reason.
func readVerdict(ctx context.Context, a checks.AtomDef, ran *dagger.Container, out string) checks.Verdict {
	code, err := exitCodeOf(ctx, ran)
	if err != nil {
		return cannotRun(a, "the atom never ran: "+err.Error())
	}
	raw, err := ran.File(out).Contents(ctx)
	if err != nil {
		stderr, _ := ran.Stderr(ctx)
		return cannotRun(a, fmt.Sprintf("exit %d: %s", code, lastLine(stderr)))
	}
	var v checks.Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return cannotRun(a, "the atom's verdict is not atom-verdict@1: "+err.Error())
	}
	return v
}

// cannotRun is an atom's could-not-run verdict, worded as the pod's executor
// words it (E6): one line, the identity from the catalogue row.
func cannotRun(a checks.AtomDef, reason string) checks.Verdict {
	return atomkit.CannotRun(a.ID, a.Stage, a.Lane, reason)
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
