package main

import (
	"context"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE GO LANE, AS CHAINS. go:vet and go:test-race are the exemplar every other
// lane's port follows; read runtime.go's rules first, then these two, then the
// rest of the lane below them.

func init() {
	register("go:gofmt", goGofmt)
	register("go:vet", goVet)
	register("go:build", goBuild)
	register("go:test-race", goTestRace)
	register("go:staticcheck", goStaticcheck)
	register("go:govulncheck", goGovulncheck)
	register("go:mutation", goMutation)
}

// goModules is the go lane's provisioned base: the lane container with the
// module graph downloaded. IT IS ONE EXEC, ON ITS OWN, so the engine caches it
// as a layer keyed on the tree — and with the module cache a volume, a tree
// whose go.sum did not move downloads nothing.
//
// The download is its own step under the default Expect for the reason the
// old script guarded it: `go vet` and `go build` exit 1 for a module the
// proxy would not serve exactly as they exit 1 for a finding, and an atom
// that reads exit 1 as FINDINGS turns a proxy's bad hour into a terminal red
// the sweep will not re-ask (measured 2026-09-11 17:26Z, foundry-tools#29:
// `proxy.golang.org …: 502 Bad Gateway` filed as a finding). Here a failed
// download is a failed exec — state 2, could not run, re-asked.
func (r *run) goModules() *dagger.Container {
	return r.lane(checks.ImageGo).
		WithExec([]string{"go", "mod", "download"})
}

// go vet ./... reports nothing.
func goVet(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:vet"),
		r.goModules().WithExec([]string{"go", "vet", "./..."}, anyExit))
}

// go test -race ./... passes, and there is something for it to pass.
//
// NO TESTS IS A FINDING. `go test ./...` prints "[no test files]" per package
// and exits 0, so a module with no test anywhere read green. Rob, 2026-09-11:
// nothing is built without tests. The count is go's own (TestGoFiles +
// XTestGoFiles per package), read in Go rather than in shell; a module where
// every package answers 0 is red before the suite runs.
//
// THE FLEET RECORD TREE RIDES ALONG. hephaestus's internal/slag goldens grade
// every record committed in foundry-dies; a lane checks out one repo, so
// without the mount they resolved nothing and SKIPPED, which `go test` prints
// as ok (#2453, #8118). A repo with no such test reads FOUNDRY_DIES and does
// nothing with it.
func goTestRace(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:test-race")
	mods := r.withDies(r.goModules())

	counts, code, err := output(ctx, mods.WithExec([]string{
		"go", "list", "-f", "{{len .TestGoFiles}}{{len .XTestGoFiles}}", "./...",
	}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		// A module that will not even list is a red about the module, and
		// the old atom filed it as FINDINGS on purpose: the tests cannot be
		// counted, so they cannot be shown to exist.
		return checks.VerdictOf(a, 1, "go:test-race: FINDINGS - go list ./... failed, so the tests cannot be counted: "+lastLine(counts))
	}
	if !checks.GoHasTestFiles(counts) {
		return checks.VerdictOf(a, 1, "go:test-race: FINDINGS - no test file in any package; nothing is built without tests")
	}
	return verdict(ctx, a, mods.WithExec([]string{"go", "test", "-race", "./..."}, anyExit))
}

// Every Go file is gofmt-clean.
//
// THE POPULATION IS THE GATE'S, NOT A find(1) WALK. The old script listed
// `find . -path ./vendor -prune -o -name '*.go' -print`, which sweeps in every
// generated and gitignored .go a developer happens to have on disk — the same
// shape that turned fleet:check-added-large-files into 40+ findings under
// target/ on tongs (2026-09-09). r.population is the engine's own gitignore
// filter with the fleet exclude applied in Go, and that exclude already carries
// `(^|/)vendor/`, so the prune this script spelled by hand is the FLEET's rule
// here rather than this atom's private one.
//
// NO `go mod download`. gofmt parses and prints; it never resolves an import.
// So this atom starts from the bare lane rather than from goModules(), and a
// proxy's bad hour cannot reach it at all.
//
// GOFMT'S EXIT CODE IS NOT THE VERDICT — ITS STDOUT IS. `gofmt -l` exits 0
// whether it listed every file in the tree or none of them, so the answer has
// to be read off the listing. DELIBERATELY CHANGED: the old script read the
// listing and threw away BOTH the exit code and stderr (`2>/dev/null`), so a
// file gofmt could not parse listed nothing, said nothing, and rendered as
// "go:gofmt: clean". A file that was not examined is not a file that is
// formatted, so a non-zero gofmt is a 2 here.
func goGofmt(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:gofmt")

	files, err := r.population(ctx, "**/*.go")
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, "go:gofmt: no Go files")
	}

	ctr := r.lane(checks.ImageGo)
	args := append([]string{"gofmt", "-l"}, files...)
	if checks.NeedsArgFile(files) {
		// A list longer than one argv may carry goes in as a NUL-joined file
		// and xargs re-splits it. That is still one typed exec of one program;
		// it is not a shell, and rule 7 holds.
		ctr = ctr.WithNewFile(gofmtArgFile, strings.Join(files, "\x00"))
		args = []string{"xargs", "-0", "-a", gofmtArgFile, "gofmt", "-l"}
	}

	listed, code, err := output(ctx, ctr.WithExec(args, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - gofmt exited "+strconv.Itoa(code)+
			"; a file it could not parse is a file it did not check.\n"+listed)
	}
	if listed != "" {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - gofmt needed on:\n"+listed)
	}
	return checks.VerdictOf(a, 0, a.ID+": clean")
}

// gofmtArgFile is where the population goes when it will not fit in an argv.
const gofmtArgFile = "/tmp/gofmt-files0"

// go build ./... succeeds.
//
// THE MODULES ARE PROVISIONED BEFORE THE TOOLCHAIN SPEAKS, for the reason
// goModules states: `go build` exits 1 for a module the proxy would not serve
// exactly as it exits 1 for a compile error, and an atom that reads exit 1 as
// FINDINGS turns a proxy's bad hour into a terminal red the sweep will not
// re-ask (measured 2026-09-11 17:26Z, foundry-tools#29).
func goBuild(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:build"),
		r.goModules().WithExec([]string{"go", "build", "./..."}, anyExit))
}

// staticcheck ./... reports nothing.
//
// THE BINARY IS BAKED INTO go-ci, at /go/bin and on PATH (measured inside the
// engine 2026-09-09, with opengrep and govulncheck beside it). The old script
// ran `go install honnef.co/go/tools/cmd/staticcheck@latest` first — a compile
// of the whole analysis suite, on every atom, on every gate, for a binary the
// image already carries — and `@latest` meant the gate's check set was whatever
// honnef published that morning rather than what the pin declared. The version
// probe below replaces it: its own exec under the DEFAULT Expect, so an image
// that lost the binary is state 2. The engine ends the "CANNOT RUN when not
// installed locally" branch this check has carried since it was a pre-push
// hook — the toolchain is the module's now — but it does not end it by letting
// an absent tool answer "fine".
//
// THE CHECK SET IS NAMED ON THE COMMAND LINE. staticcheck reads a
// staticcheck.conf from the tree when there is one, and -checks overrides it;
// naming the fleet's set here means a conf a repo adds later changes nothing in
// the gate. Measured 2026-09-11: no repo carries one today.
//
// THE DEFAULT IS EIGHT EXCLUSIONS, NOT SEVEN. The first cut listed -ST1000 …
// -ST1022 and left out -ST1023 (redundant type in a declaration), which
// staticcheck's own default config also disables — so the first Go pull through
// it (ourea #127) went red on a pre-existing `var fs billy.Filesystem = …` in a
// test file. What is spelled below is staticcheck's shipped default, verbatim;
// a stricter fleet set is a decision to make on purpose, not by omission.
func goStaticcheck(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:staticcheck"),
		r.goModules().
			WithExec([]string{"staticcheck", "-version"}).
			WithExec([]string{"staticcheck", "-checks", staticcheckChecks, "./..."}, anyExit))
}

// staticcheckChecks is staticcheck's shipped default set, spelled out so the
// gate's answer does not depend on a file any repository could add. Eight
// exclusions — see goStaticcheck for why -ST1023 is the eighth.
const staticcheckChecks = "all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023"

// govulncheck ./... reports no known vulnerability.
//
// BAKED, LIKE staticcheck. The old script `go install`ed
// golang.org/x/vuln/cmd/govulncheck@latest on every run for a binary go-ci
// already carries in /go/bin; the version probe is the provisioning step in its
// place, its own exec under the default Expect.
//
// THE MODULES ARE DOWNLOADED FIRST, which the old script did NOT do — it
// guarded the install and nothing else. govulncheck loads the package graph
// before it scans, so a proxy that is briefly gone surfaces as a govulncheck
// failure, and at exit 1 that reads as a vulnerability finding. MEASURED
// 2026-09-10T02:58Z, gate-hades-c099a35: govulncheck red at "loading packages"
// while the door rolled onto 9a5313fb. A door that is briefly gone is not a
// verdict on the code, so the fetch is its own step and its failure is a 2.
func goGovulncheck(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:govulncheck"),
		r.goModules().
			WithExec([]string{"govulncheck", "-version"}).
			WithExec([]string{"govulncheck", "./..."}, anyExit))
}

// Every mutant gremlins makes of this pull's changed Go is killed by the tests.
//
// ONE SHAPE, FOUR LANGUAGES. This runs the canonical script at its one home
// (/stocks/ci/lib/mutation/go.sh) phase by phase, in DIFF mode against
// GATE_BASE — the pull's merge base as the door names it — and answers with the
// verdict the score phase wrote: 0 clean, 1 survivors, 2 could not measure. The
// phases themselves never exit non-zero (reaching a verdict is the score
// phase's job), so a phase that does is a broken script, said as CANNOT RUN and
// named by phase. That is why each phase is its own exec under anyExit and is
// asked for its code before the next is built, rather than the six being
// chained under the default Expect: a Dagger error would file state 2 with the
// engine's text and lose which phase broke.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
// repository whole and checks the head out, so `git cat-file -e <base>` answers
// and the diff is real. A local pre-push run hands the engine a linked
// worktree, which gitReady turns into a throwaway repository with no history:
// the resolve phase then stands down 0 with "no usable PR base sha", printed,
// and the door's Job is the one that measures.
//
// GO NEEDS NO critical_modules. The python, rust and ts mutation atoms read
// that declaration off .copier-answers.yml; gremlins scopes to the diff itself,
// exactly as the retired mutation-go did.
//
// A REPO HAS NO SAY. The first cut of these atoms sourced a repo-root
// ci/mutation.env — MUT_* knobs standing in for the retired workflow's inputs —
// and Rob asked why a repo should have a say in anything (2026-09-11). It
// should not: the scripts honour MUT_GATE=false, so that file was a one-line
// switch to turn a fleet gate off, the exact shape stop-justifications exists
// to refuse (Rule #2: one canonical gate, always the latest, a red is the
// committer's to fix). It is gone, and nothing below reads the tree for a knob.
//
// GENERATED GO IS EXCLUDED BY DEFAULT, FLEET-WIDE, NOT PER REPO. MEASURED on
// this stage's first live run (2026-09-11, foundry-tools' own diff): of 14
// survivors, one was in dagger.gen.go — dagger's codegen, which no test of ours
// covers and none should. The retired mutation-go.yml carried the same
// exclusion per repo; here it is the atom's default, and it is the same for
// every repo.
//
// NeedsDies FOR THE SAME REASON go:test-race CARRIES IT: gremlins gathers
// coverage by running `go test`, and hephaestus's internal/slag goldens are
// armed on CI=true — in a container with no /dies they refuse (FATAL, exit 1),
// gremlins' coverage run dies, and the lane answers could-not-run on every
// hephaestus pull. MEASURED 2026-09-11, hephaestus #53: "failed to gather
// coverage: impossible to executeCoverage coverage: exit status 1". Any atom
// that runs a repo's `go test` runs its armed goldens, and needs the tree they
// read.
func goMutation(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:mutation")

	// The script IS the tool (rule 6), read at its one home rather than
	// vendored — and its absence is decided in Go off the mounted tree, before
	// any container runs (rule 3).
	if _, err := r.stocks.File(goMutationScript).Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - /stocks/"+goMutationScript+
			" is absent; foundry-stocks did not mount at its one home.")
	}

	ctr := r.gitReady(ctx, r.withBase(r.withDies(r.withStocks(r.goModules())))).
		WithExec([]string{"bash", "--version"}).
		WithEnvVariable("MUT_DIR", mutationDir).
		WithEnvVariable("MUT_MODE", "diff").
		WithEnvVariable("MUT_BASE", r.base).
		WithEnvVariable("MUT_EXCLUDE", goMutationExclude)

	for _, phase := range []string{"resolve", "setup", "cover", "mutate", "teardown", "score"} {
		ctr = ctr.WithExec([]string{"bash", "/stocks/" + goMutationScript, phase}, anyExit)
		out, code, err := output(ctx, ctr)
		if err != nil {
			return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
		}
		if code != 0 {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - phase "+phase+
				" exited non-zero; the phases never do on their own\n"+out)
		}
	}

	// `cat 2>/dev/null` in the old body; here the two files are read off the
	// scored container and a read that fails is the empty string, which
	// MutationVerdict refuses as a verdict.
	verdictText, _ := ctr.File(mutationDir + "/verdict").Contents(ctx)
	reasonText, _ := ctr.File(mutationDir + "/reason").Contents(ctx)
	state, reason, err := checks.MutationVerdict(verdictText, reasonText)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	return checks.VerdictOf(a, state, a.ID+": "+reason)
}

const (
	// goMutationScript is the canonical script's path INSIDE foundry-stocks;
	// withStocks mounts that tree at /stocks, so the same string spells both
	// the Directory lookup and the exec's argument.
	goMutationScript = "ci/lib/mutation/go.sh"
	// mutationDir is where the phases write their state and their verdict.
	mutationDir = "/tmp/mutation"
	// goMutationExclude keeps generated Go out of the mutant population —
	// vendored code, dagger's codegen, protobuf stubs and kubebuilder's
	// zz_generated. See goMutation for the measurement.
	goMutationExclude = `^vendor/|(^|/)dagger\.gen\.go$|\.pb\.go$|(^|/)zz_generated`
)

// lastLine is the tail of a tool's output — the line a human reads first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
