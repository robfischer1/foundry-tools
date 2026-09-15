package main

import (
	"context"
	"fmt"
	"strings"

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
	register("fleet:detect-secrets", fleetDetectSecrets)
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

// No new secret against the repository's .secrets.baseline.
//
// THE TREE HAS TO BE A REPOSITORY git CAN READ. detect-secrets-hook shells out
// to git while it decides whether the baseline is current, and on a linked
// worktree that answered "fatal: not a git repository" and exited 1 — a
// FINDING, for a scan that never happened (measured on a foundry-stocks
// worktree 2026-09-09). r.gitReady is what the worktreeRepo prelude was: a
// linked worktree's `.git` is a FILE holding an absolute host path that does not
// exist inside the container, so the tree is given a throwaway repository and
// origin is rebuilt from that path.
//
// AND THE POPULATION IS KEYED THE WAY THE BASELINE IS. The baseline records a
// path AS THE SCANNER WAS GIVEN IT, and the fleet's baselines are written by
// pre-commit, which passes git-relative paths. The old `find . -print` handed
// detect-secrets "./bases/x.yaml", which matches no key in a baseline holding
// "bases/x.yaml", so EVERY excused finding came back as a new secret — 200+ of
// them on foundry-stocks, all already in its baseline. The walk also scanned
// gitignored build junk, which cannot be committed and so cannot be a finding
// about this repository. checks.SecretsPopulation drops the fixture directories
// on top of that.
//
// NO BASELINE IS A CANNOT RUN, NEVER A PASS. Refusing to report success without
// scanning is the whole contract.
func fleetDetectSecrets(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:detect-secrets")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, ".secrets.baseline") {
		return checks.VerdictOf(a, 2, "fleet:detect-secrets: CANNOT RUN - no .secrets.baseline at the repository root. Refusing to report success without scanning.")
	}

	files, err := r.population(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	files = checks.SecretsPopulation(files)
	if len(files) == 0 {
		return checks.VerdictOf(a, 2, "fleet:detect-secrets: CANNOT RUN - the repository has no tracked file to scan")
	}

	out, code, err := output(ctx, withFileList(r.gitReady(ctx, r.lane(checks.ImageFleet)), files).
		WithExec([]string{"uvx", "--from", "detect-secrets", "detect-secrets-hook", "--help"}).
		WithExec(xargsExec("uvx", "--from", "detect-secrets", "detect-secrets-hook",
			"--baseline", ".secrets.baseline"), anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	return checks.VerdictOf(a, checks.ToolThroughXargsState(code), out)
}

// No silent suppression of any gate — a suppression carries a tool-conflict line.
//
// THE SCRIPT IS READ AT ITS ONE HOME, not vendored: a copy would be the defect
// the script exists to catch (on 2026-08-16 a sweep found 533 noqa on one rule,
// 368 of them eight decisions replicated into 46 repos by a scaffold pour). A
// source that did not mount is exit 2, never 0 — the same contract the pre-commit
// hook states, for the same reason: pre-commit hides a passing hook's output, so
// a silent skip is indistinguishable from a clean scan.
//
// TWO THINGS ABOUT git, BOTH MEASURED (foundry-tools#7626, 2026-09-09).
//
// FIRST, git ABSENT IS A CANNOT RUN, not a finding. The canonical script
// enumerates the tree with subprocess.run(["git", …]), which raises
// FileNotFoundError when git is not on PATH — an uncaught traceback, so python
// exited 1 and the old `|| exit 1` filed it as FINDINGS. A check that could not
// find its tool has not found anything wrong; it has not looked.
//
// SECOND, A LINKED WORKTREE'S `.git` IS A FILE AND IT DANGLES IN HERE, so
// `git ls-files` answered "fatal: not a git repository" and the script reported
// CANNOT RUN (measured against a tartarus worktree in the engine). Not an edge
// case: this fleet works in linked worktrees. r.gitReady rebuilds the
// repository AND reconstructs origin from the gitdir path, because repo_name()
// reads it — the DIRECTORY_EXEMPT rows are keyed on the repository, and an
// exemption Rob granted must not evaporate because the push came from a
// worktree.
//
// THE SCRIPT'S OWN EXIT CODE IS THE VERDICT, 0/1/2 straight through: its own
// exit 2 ("refusing to report success without scanning") survives rather than
// being flattened to a finding.
func fleetStopJustifications(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:stop-justifications")

	// The engine's own error rides on the reason: foundry-tools#69's gate
	// (2026-09-15) settled on this line with nothing after it, while the file
	// was on foundry-stocks main and the door answered the same URL minutes
	// later — a cause the verdict discarded is one nobody can chase.
	if _, err := r.stocks.File("ci/lib/stop_justifications.py").Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("fleet:stop-justifications: CANNOT RUN - canonical source not reachable through the door: %v", err))
	}

	return verdict(ctx, a, r.gitReady(ctx, r.withStocks(r.lane(checks.ImageFleet))).
		WithExec([]string{"python3", "--version"}).
		WithExec([]string{"python3", "/stocks/ci/lib/stop_justifications.py", "."}, anyExit))
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
		return checks.VerdictOf(a, 0, "fleet:sast-ruleset-lanes: ABSENT - no rules/sast in this tree")
	}
	rulesEntries, err := r.src.Directory("rules").Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return checks.VerdictOf(a, 0, "fleet:sast-ruleset-lanes: ABSENT - no rules/sast in this tree")
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
		return checks.VerdictOf(a, 0, "fleet:opengrep-sast: ABSENT - no rules/sast in this tree")
	}
	rulesEntries, err := r.src.Directory("rules").Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return checks.VerdictOf(a, 0, "fleet:opengrep-sast: ABSENT - no rules/sast in this tree")
	}

	scan := r.lane(checks.ImageFleet).
		WithEnvVariable("LANG", "C.UTF-8").
		WithEnvVariable("LC_ALL", "C.UTF-8").
		WithExec([]string{"opengrep", "--version"}).
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

// witnessDir is where the witness script leaves the reason it reached its
// verdict. The script's own contract; the atom only names it and reads it back.
const witnessDir = "/tmp/witness"

// Every changed .py/.go file is shown to the code witness (narcissus).
//
// THE PRE-GATE SOCKET, BACK AS AN ATOM. Born as a Tekton Task beside the gate
// (The Thesis Project F8) that reached narcissus through hades over mTLS with an
// identity minted for that Task alone; Tekton left on 2026-09-09 and the socket
// went with it. The identity was that pipeline's contrivance, not the witness's
// requirement: narcissus's plaintext MCP port answers any in-cluster caller,
// MEASURED 2026-09-10 from inside a container on the fleet's dagger engine. So
// the atom speaks to narcissus directly and needs nothing the other atoms lack.
//
// THE SCRIPT LIVES IN foundry-stocks (ci/lib/gate/witness.py, tested offline by
// witness.test.sh) and is READ AT ITS ONE HOME through the /stocks mount — the
// same rule stop_justifications follows. This atom only provisions and points:
// python3, a git-readable tree, the script.
//
// WHAT IT NEEDS THAT OTHER ATOMS DO NOT: the change set. GATE_BASE is the pull's
// merge base, handed in by Verdicts' `base` argument (the door passes
// CA_GATE_BASE); empty means the tip against its parent, which is also what a
// local run gets. r.withBase is called HERE and by almost nothing else — rule 8
// — so every other atom's cache key stays a function of the tree rather than of
// the pull. And the in-cluster port: a dev box that cannot reach narcissus lands
// on 2, could-not-consult, and says so — never a pass.
//
// THE REASON FILE IS READ OUT OF THE CONTAINER, which is why this does not call
// verdict(). The script writes WHY it decided what it decided to
// $WITNESS_DIR/reason, and the shell `cat`-ed it into the same stdout stream.
// Here it is a Directory read off the finished container, so an absent file is
// an error to ignore rather than a `[ -f ]` test, and the reason is prepended to
// the tool's own output — the human reads the why before the what.
func fleetWitness(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:witness")

	if _, err := r.stocks.File("ci/lib/gate/witness.py").Sync(ctx); err != nil {
		return checks.VerdictOf(a, 2, "fleet:witness: CANNOT RUN - /stocks/ci/lib/gate/witness.py is absent; foundry-stocks did not mount at its one home.")
	}

	ctr := r.gitReady(ctx, r.withBase(r.withStocks(r.lane(checks.ImageFleet)))).
		WithEnvVariable("WITNESS_DIR", witnessDir).
		WithExec([]string{"python3", "--version"}).
		WithExec([]string{"python3", "/stocks/ci/lib/gate/witness.py"}, anyExit)

	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return neverRan(a, err)
	}
	stdout, _ := ctr.Stdout(ctx)
	stderr, _ := ctr.Stderr(ctx)
	out := stdout + stderr
	// An absent reason file is the ordinary case for a clean run; the error is
	// the answer to "was there one", not a failure of the atom.
	if reason, err := ctr.File(witnessDir + "/reason").Contents(ctx); err == nil && strings.TrimSpace(reason) != "" {
		out = strings.TrimRight(reason, "\n") + "\n" + out
	}
	return checks.VerdictOf(a, code, out)
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
	f, err := fetchTool(ctx, checks.HadolintMirror, checks.HadolintURL)
	if err != nil {
		return nil, fmt.Errorf("hadolint %s could not be fetched from the mirror or from upstream: %w", checks.HadolintVersion, err)
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
