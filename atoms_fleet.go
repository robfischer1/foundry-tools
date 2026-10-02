package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/scripts"
	"dagger/foundry-tools/internal/dagger"
)

// THE FLEET LANE, AS TYPED CHAINS. These ten atoms run in every repository in
// custody, whatever it is written in, so they are the ones a shell string cost
// the most: eight of them opened with the same forty-line prelude — a
// provisioning guard, a throwaway git repository, a `population()` shell
// function — re-parsed by `sh` on every atom of every gate. The preludes are
// gone. `worktreeRepo` is `r.gitReady`, `gatePopulation` is `r.population`, and
// `provisionGuard` is the default Expect on a probe exec. Read runtime.go's
// eight rules and atoms_go.go first; everything below follows them.

func init() {
	register("fleet:check-yaml", fleetCheckYAML)
	register("fleet:check-added-large-files", fleetCheckAddedLargeFiles)
	register("fleet:check-merge-conflict", fleetCheckMergeConflict)
	register("fleet:stop-justifications", fleetStopJustifications)
	register("fleet:sast-ruleset-lanes", fleetSastRulesetLanes)
	register("fleet:orbit-drift", fleetOrbitDrift)
	register("fleet:opengrep-sast", fleetOpengrepSast)
	register("fleet:witness", fleetWitness)
	register("fleet:hadolint", fleetHadolint)
}

// cannotEnumerate is the verdict when the TREE ITSELF would not answer. It is a
// could-not-run about the repository rather than a finding about it: the atom
// never got as far as having an opinion.
func cannotEnumerate(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the tree would not enumerate: %v", a.ID, err))
}

// neverRan is the verdict when the ENGINE would not answer — a mount that did
// not evaluate, an image that would not pull, a provisioning exec that failed
// under the default Expect. It is verdict()'s own wording, because it is
// verdict()'s own case: output() separates the engine's error from the tool's
// exit code precisely so an atom that reads the code itself still files the
// error the way every other atom does.
func neverRan(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
}

// Every YAML file in the tree parses.
//
// MULTI-DOCUMENT YAML IS VALID YAML HERE, AND SYNTAX IS THE QUESTION.
// pre-commit's check-yaml refuses a second document by default and LOADS the
// file, so a custom tag (Home Assistant's !include, an ansible vault) is "could
// not determine a constructor" — neither is a fact about the YAML. MEASURED
// 2026-09-10T01:2xZ on infra: "expected a single document … but found another
// document" on a k3s manifest; and 2026-09-11 16:0xZ, the first gate after the
// atoms stopped reading a repo's hook args, infra red on
// hass01/homeassistant/configuration.yaml's !include, which its own config had
// passed with --unsafe. --unsafe parses instead of loading, which is the check
// the fleet means: every document is well-formed YAML. Both flags are the
// fleet's, unconditionally.
//
// THE POPULATION IS THE ENGINE'S OWN, no git involved (r.population says why),
// and it goes to the tool through xargs because it is not small: infra tracks
// YAML in the thousands.
func fleetCheckYAML(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:check-yaml")

	files, err := r.population(ctx, "**/*.yml", "**/*.yaml")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, "fleet:check-yaml: no YAML in this repository")
	}

	out, code, err := output(ctx, withFileList(r.lane(checks.ImageFleet), files).
		WithExec([]string{"uvx", "--from", "pre-commit-hooks", "check-yaml", "--help"}).
		WithExec(xargsExec("uvx", "--from", "pre-commit-hooks", "check-yaml",
			"--allow-multiple-documents", "--unsafe"), anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	// checks.ToolThroughXargsState, not StateFor: xargs answers 123 for a hook
	// that exited 1, and 123 straight into StateFor would file every finding as
	// a could-not-run.
	return checks.VerdictOf(a, checks.ToolThroughXargsState(code), out)
}

// largeFileLimitKB is the fleet's ceiling, and it is measured against the one
// file that ever crossed it — see checks.FilesOver for the cert-manager
// measurement.
const largeFileLimitKB = 2048

// No file in the tree exceeds 2048 KB.
//
// THE POPULATION IS WHAT THE REPOSITORY WOULD COMMIT, NOT A TREE WALK. The atom
// walked the mounted directory until 2026-09-09, when tongs answered 40+
// findings, every one a file under target/ that git ignores and no commit could
// carry. A gitignored file cannot be committed, so it cannot be a finding ABOUT
// THE REPOSITORY — and a check that refuses every rust and node developer's push
// over their own build directory is a check nobody leaves switched on.
// r.population is that population without the throwaway repository the shell
// prelude had to build to ask git for it.
//
// ONE EXEC ASKS THE SIZES AND GO DOES THE ARITHMETIC. `stat -c '%s %n'` through
// xargs replaces the `tr | xargs | awk` pipeline; the comparison is
// checks.FilesOver, where a table test can reach it.
//
// A PARTIAL MEASUREMENT IS NOT A CLEAN ONE. The shell swallowed stat's failures
// (`2>/dev/null`) and reported nothing over the limit; a size never read is not
// a size under the limit, so a non-zero stat pass with no finding is a
// could-not-run here. A finding still outranks it — a file measured over the
// ceiling is over it however the rest of the pass went.
func fleetCheckAddedLargeFiles(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:check-added-large-files")

	files, err := r.population(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, fmt.Sprintf("fleet:check-added-large-files: nothing over %d KB", largeFileLimitKB))
	}

	out, code, err := output(ctx, withFileList(r.lane(checks.ImageFleet), files).
		WithExec(xargsExec("stat", "-c", "%s %n"), anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	if big := checks.FilesOver(out, largeFileLimitKB*1024); len(big) > 0 {
		return checks.VerdictOf(a, 1, fmt.Sprintf("files over %d KB:\n%s", largeFileLimitKB, strings.Join(big, "\n")))
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - stat did not measure every file in the population (exit %d), and a size that was never read is not a size under the limit.\n%s",
			a.ID, code, out))
	}
	return checks.VerdictOf(a, 0, fmt.Sprintf("fleet:check-added-large-files: nothing over %d KB", largeFileLimitKB))
}

// conflictMarkers matches ONLY THE TWO ANCHORED MARKERS, not the bare row of
// equals signs: that is a setext heading in Markdown and a table rule in
// reStructuredText, and matching it turns every docs repo red for a reason
// nobody can act on.
const conflictMarkers = `^(<<<<<<< |>>>>>>> )`

// No conflict markers were committed.
//
// OVER THE COMMITTABLE FILES, not the directory. A conflict marker inside a
// gitignored build artifact was never committed, and no --exclude-dir list can
// name every generator (target/, .venv/, dist/, …).
//
// grep IS THREE-VALUED AND xargs COLLAPSES TWO OF THE THREE, which is the one
// piece of judgement in this atom and now the one tested piece:
// checks.GrepThroughXargsState carries the mapping and the reasoning. The shell
// it replaces had `|| true`, which read a grep that died exactly as it read a
// grep that found nothing.
//
// WHAT output() HANDS THE MAPPING is stdout, plus stderr when the exec exited
// non-zero. A grep diagnostic therefore arrives as if it were a hit and lands
// as a FINDING rather than a could-not-run — visible, carrying the diagnostic
// text, and never a pass, which is the direction that matters.
func fleetCheckMergeConflict(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:check-merge-conflict")

	files, err := r.population(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, "fleet:check-merge-conflict: no conflict markers")
	}

	out, code, err := output(ctx, withFileList(r.lane(checks.ImageFleet), files).
		WithExec(xargsExec("grep", "-In", "-E", conflictMarkers), anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	switch state := checks.GrepThroughXargsState(code, out); state {
	case int(checks.StatePass):
		return checks.VerdictOf(a, 0, "fleet:check-merge-conflict: no conflict markers")
	case int(checks.StateFindings):
		return checks.VerdictOf(a, 1, out)
	default:
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - the conflict-marker scan did not complete (xargs exit %d) and printed nothing. A scan that did not run is not a tree with no markers.",
			a.ID, code))
	}
}

// No silent suppression of any gate — a suppression carries a tool-conflict line.
//
// THE SCAN IS GO, checks.StopJustifications, carried from foundry-stocks
// ci/lib/stop_justifications.py and measured against it: byte-identical output
// and exit on all 77 repositories in Forge/Outputs and every linked worktree,
// and identical region masks on all 1,078 tracked python files. git answers the
// two questions only the repository can — which files are tracked, and what
// origin names it — and the engine reads the files. Nothing runs in python, and
// nothing is read from foundry-stocks.
//
// A LINKED WORKTREE'S `.git` IS A FILE AND IT DANGLES IN HERE (measured against a
// tartarus worktree, foundry-tools#7626), so `git ls-files` would answer "fatal:
// not a git repository". r.gitReady rebuilds the repository AND reconstructs
// origin from the gitdir path — DirectoryExempt is keyed on the repository, and
// an exemption Rob granted must not evaporate because the push came from a
// worktree.
//
// A TREE THAT WILL NOT LIST, OR A FILE THAT WILL NOT READ, IS A CANNOT RUN:
// pre-commit hides a passing hook's output, so a silent skip is
// indistinguishable from a clean scan.
func fleetStopJustifications(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:stop-justifications")

	ctr := r.gitReady(ctx, r.lane(checks.ImageFleet))
	// NO `-z`, AND THE REASON IS THE LOG. NUL-separated output is ONE line, so
	// this atom put a whole repository's file list into a single log entry —
	// 65,496 B on ourea, 65,534 B on mnemosyne — which Loki cut mid-path at its
	// 64KB max_line_size without saying so (infra #10719). dagger echoes an
	// exec's stdout into progress whatever else is done with it (RedirectStdout
	// was measured against the live engine and still echoes), so the list stops
	// filling the log only by ceasing to be one line.
	//
	// THE TRACKED SET IS UNCHANGED, which is the point: this is still
	// `git ls-files`, still the repository's own answer to which files are
	// tracked. A file on disk that git does not track stays out of scope, as
	// checks.SplitGitPaths' own tests and this atom's hold it to.
	ls, code, err := output(ctx, ctr.WithExec([]string{"git", "ls-files"}, anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, "stop-justifications: CANNOT RUN — git ls-files failed: "+ls+"\nstop-justifications: refusing to report success without scanning.")
	}
	// No origin is a repository that names no exemption, not a refusal: the
	// script's fallback named the checkout directory, which in the lane is
	// /src and matches no row.
	origin, code, err := r.originURL(ctx, ctr)
	if err != nil {
		return neverRan(a, err)
	}
	repo := ""
	if code == 0 {
		repo = checks.RepoFromOrigin(origin)
	}

	// A LISTING THAT WILL NOT PARSE IS A CANNOT RUN, not an empty scan: a path
	// this parser cannot read is a file it would then silently not look at.
	tracked, err := checks.SplitGitPaths(ls)
	if err != nil {
		return checks.VerdictOf(a, 2, "stop-justifications: CANNOT RUN — "+err.Error()+"\nstop-justifications: refusing to report success without scanning.")
	}

	bodies := readFiles(ctx, r.src, checks.SJReads(tracked))
	state, out := checks.StopJustifications(checks.SJInput{
		Tracked: tracked,
		// checks.SJReads names every file the scan reads, and a test holds it to that.
		Read: func(rel string) (string, error) {
			b := bodies[rel]
			return b.body, b.err
		},
		Repo:  repo,
		Today: time.Now().UTC().Format(time.DateOnly),
	})
	return checks.VerdictOf(a, state, out)
}

// fileRead is one file's contents, or why it would not read.
type fileRead struct {
	body string
	err  error
}

// readFiles reads every path under src at once, sixteen at a time — one query
// each, and a tree of six hundred scanned files is theia's. A read that fails
// is kept, not raised: which file failed is the scan's to report.
func readFiles(ctx context.Context, src *dagger.Directory, paths []string) map[string]fileRead {
	out := make(map[string]fileRead, len(paths))
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(16)
	for _, p := range paths {
		g.Go(func() error {
			body, err := src.File(p).Contents(ctx)
			mu.Lock()
			out[p] = fileRead{body: body, err: err}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// The SAST ruleset declares every lane this repository actually builds.
//
// NO CONTAINER RUNS. The question is entirely about two things the engine can
// answer from the Directory — what rules/sast declares, and which root
// manifests are present — so rule 3 takes the whole atom rather than only its
// absence. The shell was six pipelines and a `for` loop; the judgement is
// checks.SastLanesMissing, where a table test mirrors each stage of it.
//
// IT CLOSES THE CASE THE ZERO-FILE REFUSAL STRUCTURALLY CANNOT SEE. A ruleset
// that matches SOMETHING and misses the rest still exits 0 —
// foundry-stocks#4949: chaos scanned 28 of 2636 files, themis 26 of 2221, both
// green. Ported from the go_B pre-commit hook.
//
// A MISSING LANE IS EXIT 2 BY DESIGN, not a finding. A lane the ruleset never
// names is a lane nothing examined, which is a check that did not run rather
// than a check with something to say — the same argument State makes one shelf
// up.
func fleetSastRulesetLanes(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:sast-ruleset-lanes")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, "rules") {
		return absentRuleset(ctx, r, a)
	}
	rulesEntries, err := r.src.Directory("rules").Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return absentRuleset(ctx, r, a)
	}

	paths, err := r.src.Glob(ctx, "rules/sast/*.yml")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	bodies := make([]string, 0, len(paths))
	for _, p := range paths {
		body, err := r.src.File(p).Contents(ctx)
		if err != nil {
			// A ruleset file the atom could not read is a ruleset it did not
			// examine, and guessing its languages would be the partial scan
			// this atom exists to catch.
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		bodies = append(bodies, body)
	}

	declared, missing := checks.SastLanesMissing(bodies, entries)
	if len(missing) == 0 {
		return checks.VerdictOf(a, 0, "fleet:sast-ruleset-lanes: ruleset declares every lane this repo builds")
	}
	return checks.VerdictOf(a, 2, fmt.Sprintf(
		"sast-ruleset-lanes: rules/sast declares [%s] but this repo also builds: %s\n"+
			"A ruleset that never names a lane never examines it, and opengrep still exits 0 whenever some OTHER language matched - the partial-scan case the zero-file refusal cannot see (foundry-stocks#4949).",
		strings.Join(declared, " "), strings.Join(missing, " ")))
}

// This repo's declared seams agree with the canonical contracts in
// foundry-dies/orbits.
//
// THE SEAM HALF OF BLAST RADIUS. A star's orbit.toml says which edges it takes
// part in, so a session standing in the repo can see who it breaks without
// cloning anything; this atom asks whether that declaration still matches the
// canonical contract.
//
// IT COMPARES A DIGEST, NOT A VERSION INTEGER, and that is deliberate. Two
// hand-bumped integers, in two repos, raised in two separate commits, will skew
// — and skew across a seam is precisely the contract-drift condition these files
// exist to detect. A check whose own failure mode is the thing it detects is not
// a check. Bytes either hash to what the star recorded or they do not.
//
// THE DOOR IS THE SAME ONE dies:contracts READS, anonymously, over the raw API.
// It is still forgejo, which is being sunset; when that read moves, it moves for
// both atoms together rather than one of them drifting off alone.
//
// An edge that declares no digest is a FINDING, not a pass. It means the repo
// named a seam and pinned nothing, so this atom compared nothing — and "nothing
// to check" is not "checked and clean", which is this module's founding
// argument.
//
// THE PYTHON IS THE TOOL (rule 6) and it is now a FILE. It ran as a heredoc
// inside a shell string, which is a program nothing could lint; it is unchanged,
// byte for byte, at internal/checks/scripts/fleet_orbit_drift.py, embedded and
// mounted. It carries its own 0/1/2 and they reach the verdict untouched.
//
// uv RUNS IT, UNCONDITIONALLY. The script wants tomllib and falls back to tomli;
// the lane image's system python3 is not promised to be ≥3.11, and the shell's
// probe-then-choose dance existed only to find that out. `uv run --with
// 'tomli>=2.0'` answers for both interpreters, and the uv cache is a volume, so
// the resolve is paid once per engine rather than once per gate.
func fleetOrbitDrift(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:orbit-drift")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, "orbit.toml") {
		return checks.VerdictOf(a, 0, "fleet:orbit-drift: ABSENT - no orbit.toml in this tree, so this repo declares no seams")
	}

	body := scripts.FleetOrbitDrift

	return verdict(ctx, a, r.lane(checks.ImageFleet).
		WithNewFile("/tmp/orbit-drift.py", string(body)).
		WithExec([]string{"uv", "--version"}).
		WithExec([]string{"uv", "run", "--no-project", "--quiet", "--with", "tomli>=2.0",
			"python3", "/tmp/orbit-drift.py"}, anyExit))
}

// opengrepRefusal is the zero-file refusal, kept as written.
const opengrepRefusal = "opengrep: REFUSING a zero-file scan. The ruleset matched no file in this repo, so 0 findings means NOTHING WAS EXAMINED - not that the code is clean. Check that rules/sast declares the language(s) this repo is actually written in."

// SAST scan that refuses a zero-file scan.
//
// THE REFUSAL IS THE POINT. "Ran N rules on 0 files: 0 findings" exits 0 and
// renders as Passed, which is indistinguishable from a clean scan —
// go-repo-template and rust-repo-template shipped the PYTHON ruleset, so every
// Go and Rust star's security gate had never examined a single file
// (foundry-stocks#4415). checks.OpengrepZeroFiles reads the summary line.
//
// THE BINARY IS BAKED INTO THE LANE IMAGE and the installer is gone.
// stellar_core:*-ci carries opengrep 1.25.0 — measured inside the engine
// 2026-09-09, all four CI images. The shell kept a curl-the-installer fallback,
// which was itself the first defect foundry-tools#7626 records: on the old uv
// base there was no curl at all, so this atom was cannot-run in every repository
// in the fleet and no push could go green anywhere. Rule 5 sends a fetched tool
// through dag.HTTP, and there is nothing here to fetch — `opengrep --version`
// under the default Expect IS the provisioning probe, and an image that lost the
// binary is a failed exec, state 2, rather than a silent second path.
//
// LANG/LC_ALL ARE THE TOOL'S, not the lane's: opengrep decodes source files by
// the locale, and a lane image with no locale set reads UTF-8 bytes as ASCII.
func fleetOpengrepSast(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:opengrep-sast")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, "rules") {
		return absentRuleset(ctx, r, a)
	}
	rulesEntries, err := r.src.Directory("rules").Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return absentRuleset(ctx, r, a)
	}

	scan := r.lane(checks.ImageFleet).
		WithEnvVariable("LANG", "C.UTF-8").
		WithEnvVariable("LC_ALL", "C.UTF-8").
		// NO `opengrep --version` HERE: provision runs that probe in the
		// image's own layer, under the default Expect, so a binary that does
		// not run is already a provisioning failure. Repeated here it sat
		// after the tree mount and ran once per tree (52 times in 63 sampled
		// gates, 162 exec-seconds — the CI pipeline audit, lever F).
		WithExec([]string{"opengrep", "scan", "--config", "rules/sast", "--error", "."}, anyExit)

	out, code, err := output(ctx, scan)
	if err != nil {
		return neverRan(a, err)
	}
	// output() folds stderr in only for a NON-ZERO exit, and a zero-file scan
	// exits ZERO — that is the entire defect. The shell captured `2>&1`
	// unconditionally, so the refusal below saw the summary line wherever
	// opengrep chose to print it; reading only stdout on a clean exit would
	// hand the zero-file case back its green. Both streams, always.
	if code == 0 {
		if errOut, e := scan.Stderr(ctx); e == nil && errOut != "" {
			out = strings.TrimSpace(out + "\n" + errOut)
		}
	}
	if checks.OpengrepZeroFiles(out) {
		return checks.VerdictOf(a, 2, out+"\n"+opengrepRefusal)
	}
	return checks.VerdictOf(a, code, out)
}

// Every changed .py/.go file is shown to the code witness (narcissus).
//
// THE PRE-GATE SOCKET, BACK AS AN ATOM, AND NOW NO SCRIPT. Born as a Tekton
// Task beside the gate (The Thesis Project F8) that reached narcissus through
// hades over mTLS with an identity minted for that Task alone; Tekton left on
// 2026-09-09 and the socket went with it. The identity was that pipeline's
// contrivance, not the witness's requirement: narcissus's plaintext MCP port
// answers any in-cluster caller. foundry-stocks' ci/lib/gate/witness.py then
// ran here under python3; the change set is now read with git execs, each file
// is asked about from Go, and checks.ClassifyWitness and checks.AggregateWitness
// settle it.
//
// WHAT IT NEEDS THAT OTHER ATOMS DO NOT: the change set. GATE_BASE is the
// pull's merge base; empty means the tip against its parent, which is also what
// a local run gets. r.withBase is called HERE and by almost nothing else — rule
// 8. And the in-cluster port: a dev box that cannot reach narcissus lands on 2,
// could-not-consult, and says so — never a pass.
//
// A LINKED-WORKTREE SNAPSHOT HAS NO CHANGE SET. gitReady rebuilds a worktree as
// a throwaway repository with no commits and marks it ca.snapshot; the witness
// has nothing to read at that stage, and saying so is the verdict. The
// landing's Job clones whole and witnesses the real commits
// (foundry-tools#8736).
func fleetWitness(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:witness")
	settle := func(state int, reason string, rows []checks.WitnessRow) checks.Verdict {
		return checks.VerdictOf(a, state, "narcissus — fleet:witness: "+reason+"\n\n"+checks.WitnessSummary(rows))
	}
	ctr := r.gitReady(ctx, r.withBase(r.lane(checks.ImageFleet))).
		// Provisioning, under the default Expect.
		WithExec([]string{"git", "--version"})
	git := func(args ...string) (string, int, error) {
		return output(ctx, ctr.WithExec(append([]string{"git"}, args...), anyExit))
	}

	snapshot, code, err := git("config", "--get", "ca.snapshot")
	if err != nil {
		return neverRan(a, err)
	}
	if code == 0 && snapshot != "" {
		return settle(0, "no change set to witness at pre-push: the source is a "+snapshot+
			" snapshot with no commits; the landing's Job witnesses the real change set.", nil)
	}

	// THE CHANGE SET: added, modified and renamed paths, in git's order, from
	// the merge base of the door's base (run.changeBase): main's tip moves
	// under an open pull, and a two-tree diff would carry every landing since
	// as this pull's own change.
	var files string
	if r.base != "" {
		// Assigned, not declared: `files, code, err =` below must reach the
		// err the read after this block checks, not a shadow of it.
		var since string
		since, err = r.changeBase(ctx, ctr)
		if err != nil {
			return settle(2, "CANNOT RUN - could not read the change set: "+err.Error(), nil)
		}
		if since == "" {
			return settle(2, "CANNOT RUN - could not read the change set: the base "+r.base+" is not in this history", nil)
		}
		files, code, err = git("diff", "--name-only", "--diff-filter=AMR", since+"..HEAD")
	} else if _, parent, perr := git("rev-parse", "--verify", "--quiet", "HEAD^"); perr != nil {
		return neverRan(a, perr)
	} else if parent == 0 {
		files, code, err = git("diff", "--name-only", "--diff-filter=AMR", "HEAD^..HEAD")
	} else {
		// A root commit has no parent: every file it carries is the change.
		files, code, err = git("show", "--pretty=", "--name-only", "--diff-filter=AMR", "HEAD")
	}
	if err != nil {
		return neverRan(a, err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - could not read the change set: "+files, nil)
	}
	sources, skipped, vendored := checks.WitnessChangeSet(strings.Fields(files))
	if len(sources) == 0 {
		state, reason := checks.AggregateWitness(nil, skipped, vendored)
		return settle(state, reason, nil)
	}

	origin, code, err := r.originURL(ctx, ctr)
	if err != nil {
		return neverRan(a, err)
	}
	star := "unknown"
	if code == 0 {
		star = checks.StarName(origin)
	}

	// A FEW AT ONCE. The rows keep git's order, whatever order the answers
	// arrive in, and one file's failure is that file's row, not the run's.
	rows := make([]checks.WitnessRow, len(sources))
	g := new(errgroup.Group)
	g.SetLimit(checks.WitnessWorkers)
	for i, p := range sources {
		g.Go(func() error {
			query, err := r.src.File(p).Contents(ctx)
			if err != nil {
				rows[i] = checks.WitnessRow{Path: p, Class: "could-not-consult", Reason: "could not ask: " + err.Error()}
				return nil
			}
			body := checks.WitnessRequest(i+1, query, checks.WitnessLanguage(p), "ci:gate:"+star+"@HEAD", p)
			status, contentType, answer, err := askWitness(ctx, body)
			if err != nil {
				rows[i] = checks.WitnessRow{Path: p, Class: "could-not-consult", Reason: "could not ask the witness: " + err.Error()}
				return nil
			}
			status, result := checks.WitnessEnvelope(status, contentType, answer)
			rows[i] = checks.ClassifyWitness(p, status, result)
			return nil
		})
	}
	_ = g.Wait() // every ask files its own row; none returns an error
	state, reason := checks.AggregateWitness(rows, skipped, vendored)
	return settle(state, reason, rows)
}

// askWitness posts one JSON-RPC request to narcissus's MCP port
// (checks.PostWitness). A variable, so the tests can answer for narcissus the
// way witness.py's WITNESS_ANSWER seam did.
var askWitness = func(ctx context.Context, body string) (int, string, string, error) {
	post := func(ctx context.Context, body string) (int, string, string, error) {
		return checks.PostWitness(ctx, checks.WitnessURL, body)
	}
	return checks.AskWitnessRetried(ctx, post, body, checks.WitnessAttempts, checks.WitnessRetryPause, checks.SleepContext)
}

// hadolintClient provisions the Dockerfile linter, PINNED, and proves it.
//
// THE LANE IMAGES CARRY NO hadolint — it is a Haskell binary no language
// toolchain ships — so it is fetched the way opa and compose are: the Nexus
// mirror first, upstream second, and a failure of both is the caller's state
// 2 rather than a fallthrough. The version probe runs under the DEFAULT
// Expect (a binary that downloaded but does not run is a provisioning
// failure) and its one line is read back and matched against the pin, because
// the rule set is a property of the binary (checks.HadolintVersionOK).
//
// THE FLEET'S RULESET IS WRITTEN INTO THE CONTAINER, outside /src, and named
// by --config. hadolint's default is to read `.hadolint.yaml` from the working
// directory, and an explicit --config replaces that lookup rather than merging
// with it — measured, checks.HadolintConfigPath has the numbers. That
// replacement is what makes the ruleset the fleet's: the repository's file
// serves its local hook and the gate never opens it.
func (r *run) hadolintClient(ctx context.Context) (*dagger.Container, error) {
	f, err := fetchTool(ctx, checks.HadolintURL)
	if err != nil {
		return nil, fmt.Errorf("hadolint %s could not be fetched: %w", checks.HadolintVersion, err)
	}
	ctr := r.lane(checks.ImageFleet).
		WithFile("/usr/local/bin/hadolint", f, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithNewFile(checks.HadolintConfigPath, checks.HadolintConfig).
		WithExec([]string{"hadolint", "--version"})
	out, err := ctr.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("the hadolint version probe never ran: %w", err)
	}
	if !checks.HadolintVersionOK(out, checks.HadolintVersion) {
		return nil, fmt.Errorf("the binary on disk does not answer %q, so the rules are not the pinned ones: %s", "Haskell Dockerfile Linter "+checks.HadolintVersion, strings.TrimSpace(out))
	}
	return ctr, nil
}

// Every Dockerfile in the tree passes hadolint under the fleet's ruleset.
//
// THE POPULATION IS WHAT THE REPOSITORY WOULD COMMIT, filtered in Go by ONE
// predicate (checks.DockerfilePopulation) rather than by a second spelling of
// it as engine globs. The fleet exclude has already dropped vendor/ — four Go
// stars vendor go.opentelemetry.io's dependencies.Dockerfile, and a vendored
// Dockerfile is not one this repository ships.
//
// NO DOCKERFILE IS ABSENT, not a pass. A repository that ships no image has
// nothing for this atom to say, and it says so in the shape AnnouncedAbsence
// reads; most of the fleet's config repos land here, and since its tf-runner
// image retired (infra#490, 2026-09-13) so does infra.
//
// THE TOOL'S OWN EXIT CODE IS THE VERDICT, 0/1 straight through StateFor: 0
// when nothing reached the threshold (info and style findings are printed and
// do not fail), 1 for a finding at warning or above — and 1 too for a
// Dockerfile that does not PARSE, which hadolint reports as a finding on the
// line, which is right: an unparseable Dockerfile is a finding about the
// repository. Anything else is a could-not-run. The whole population goes in
// one argv behind `--`: Dockerfiles number in the ones per repository, not
// the thousands, so this is rule 4 with no xargs.
//
// INLINE PRAGMAS ARE HONOURED. `# hadolint ignore=DL3003` beside a reason is
// the fleet's sanctioned shape for a line the rule gets wrong, as `noqa` is
// under stop-justifications; --disable-ignore-pragma is deliberately not
// passed.
func fleetHadolint(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:hadolint")

	files, err := r.population(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	dockerfiles := checks.DockerfilePopulation(files)
	if len(dockerfiles) == 0 {
		return checks.VerdictOf(a, 0, "fleet:hadolint: ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image for hadolint to read")
	}

	ctr, err := r.hadolintClient(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. A Dockerfile that was never linted is not a Dockerfile that passed.", a.ID, err))
	}

	args := append([]string{"hadolint", "--no-color", "--config", checks.HadolintConfigPath, "--"}, dockerfiles...)
	return verdict(ctx, a, ctr.WithExec(args, anyExit))
}
