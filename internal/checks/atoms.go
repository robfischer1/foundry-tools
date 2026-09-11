package checks

import (
	"fmt"
	"strings"
)

// Stage names where in a pull's life an atom fires. The catalogue carries the
// same values (nereus.antibody_store.stage), so a row and its code agree by
// construction.
const (
	StagePrecommit = "precommit"
	StagePrepush   = "prepush"
	// StageSweep is repo cadence, and it is NOT a pull stage. An atom
	// carrying it answers a question about the repository rather than about
	// the change, so it runs on a clock (CA F9's ca-sweep CronJob) and never
	// in a pull's path. PullPathStages is the enforcement; the vocabulary
	// matches nereus.antibody_store.stage, which admits exactly these four.
	StageSweep = "sweep"
	// StageMutation is the mutation gate: a pull's change set, mutated, with
	// the pull's own tests asked to notice. It IS about the change and it
	// DOES block a pull's green — but it is not in PullPathStages, because
	// the gate that asks for "the vector" runs inside one cap slot and a
	// mutation run is minutes on top of it. The door dispatches this stage
	// as its own lane (`mutation`, beside gate and build), by name, so the
	// two settle independently and the gate stays as short as it was.
	//
	// WHAT IT REPLACES. Tekton's mutation-* Pipelines, retired with the
	// engine on 2026-09-09; from then until this stage landed nothing in the
	// fleet mutated anything, while ca-sweep's header, the templates'
	// `critical_modules` question and eight comments in ourea all described
	// a gate that no longer ran (ourea#8319). The scripts survived in
	// foundry-stocks ci/lib/mutation/ and are what these atoms run.
	StageMutation = "mutation"
)

// AtomDef is one catalogued check, as code.
//
// The table below is the ONE definition. Every `// +check` function is a
// three-line call into it, and the verdict vector iterates the same rows — so
// `dagger check -l` and the vector cannot drift apart, which is the two-surface
// defect the census counted 31 times.
type AtomDef struct {
	// ID is the namespaced atom id, e.g. "go:staticcheck".
	ID string
	// Stage is precommit or prepush.
	Stage string
	// Lane is the language whose manifest must be present for this atom to
	// have a surface. LaneAny runs everywhere.
	Lane Lane
	// Image is the lane container the atom runs in.
	Image string
	// Desc is the one-line description the catalogue and `-l` both carry.
	Desc string
	// Script is the atom's body. It MUST exit 0 clean, 1 findings, 2 could
	// not run — every provisioning failure is a 2, never a 0.
	Script string
	// NeedsStocks asks the caller to mount foundry-stocks at /stocks, so the
	// atom can read the canonical script at its ONE home rather than carry a
	// vendored second copy.
	NeedsStocks bool
	// NeedsDies asks the caller to mount foundry-dies at /dies and name it in
	// the environment, for the atoms whose subject is the fleet's record tree
	// rather than the repo under test. A gate lane checks out ONE repository,
	// so an atom that grades the fleet has no other way to see it.
	NeedsDies bool
}

// provisionGuard is the shape every provisioning probe takes: a tool that is
// not there is a CANNOT RUN, and a check that cannot run must not answer
// "fine". staticcheck and govulncheck have carried this since before there was
// an engine; here it is the rule rather than the exception.
const provisionGuard = `guard() { "$@" >/dev/null 2>&1 || { echo "CANNOT RUN - could not provision: $*" >&2; exit 2; }; }
`

// worktreeRepo makes the mounted tree readable BY git, and refuses when git is
// absent. Every atom that asks the REPOSITORY what its files are begins here.
//
// THE POPULATION IS git's, NOT A TREE WALK, and that is the second half of what
// this prelude buys. The engine receives a directory, so the fleet atoms used to
// enumerate it by walking one — which sweeps in every build artifact a developer
// happens to have on disk. Measured 2026-09-09 on tongs, a rust star:
// fleet:check-added-large-files answered 40+ findings, every one a file under
// target/ that git ignores and no commit could ever carry. A gitignored file
// cannot be committed, so it cannot be a finding ABOUT THE REPOSITORY — and a
// check that refuses every rust and node developer's push over their own build
// directory is a check nobody will leave switched on. With the prelude below
// there is always an index to ask.
//
// A LINKED WORKTREE'S `.git` IS A FILE, AND IT DANGLES IN HERE. It holds
// `gitdir: <primary>/.git/worktrees/<name>`, an absolute host path that does not
// exist inside the container, so every git command against the tree answers
// "fatal: not a git repository". Measured 2026-09-09 on two different atoms:
// fleet:stop-justifications reported CANNOT RUN (its `git ls-files` walk), and
// fleet:detect-secrets exited 1 having printed nothing but that line. That is
// not an edge case — the pre-push gate hook hands the engine `--source="$PWD"`
// and this fleet works in linked worktrees, so it is the COMMON case.
//
// The tree is given a throwaway repository of its own and its index filled from
// the non-ignored files: the same population every other atom already walks with
// `find .`, and the tree the push is actually carrying. origin is reconstructed
// from the gitdir path because stop_justifications' repo_name() reads it —
// DIRECTORY_EXEMPT is keyed on the repository, and an exemption Rob granted must
// not evaporate because the push came from a worktree.
//
// A PRIMARY CHECKOUT IS UNTOUCHED: `.git` is a directory there, the index rides
// along with it, and this is a no-op.
const worktreeRepo = `command -v git >/dev/null 2>&1 || { echo "CANNOT RUN - git is not on PATH in this lane image, and this check reads the repository through it. Nothing was scanned." >&2; exit 2; }
git config --global --add safe.directory '*' >/dev/null 2>&1 || true
if [ -f .git ]; then
  primary=$(sed -n 's|^gitdir: \(.*\)/\.git/worktrees/.*$|\1|p' .git)
  rm -f .git
  git init -q . || { echo "CANNOT RUN - the tree came from a linked worktree and could not be given a readable repository." >&2; exit 2; }
  if [ -n "$primary" ]; then git remote add origin "${primary}.git" >/dev/null 2>&1 || true; fi
  git ls-files -o --exclude-standard -z | git update-index -z --add --stdin || { echo "CANNOT RUN - could not index the worktree's files." >&2; exit 2; }
fi
`

// gatePopulation is THE GATE'S OWN POPULATION: the repository's tracked
// files, minus what the repository's own pre-commit config excludes. The
// three fleet atoms below are ports of pre-commit hooks, and pre-commit
// applies the config's top-level `exclude:` to every hook's file list —
// so a port that reads a bare `git ls-files` grades a different population
// than the hook it replaced. MEASURED 2026-09-10T01:13Z, the runner's first
// gate on ourea: check-yaml, check-added-large-files and detect-secrets all
// red, every finding under vendor/ — a tree the repo's exclude
// (`(^|/)(vendor|node_modules)/`) keeps out of the hooks and the Tekton gate
// running pre-commit itself had never seen. stop_justifications.py already
// reads the same line (foundry-stocks ci/lib, gate_exclusions); this is the
// shell form of it for the atoms that take a file list.
//
// The regex is pre-commit's (Python re) fed to grep -E; the shapes the fleet
// writes — anchors, alternation, character classes, escaped dots — mean the
// same in both. A config with no exclude admits everything, as pre-commit does.
const gatePopulation = `EXCL="$(sed -n 's/^exclude:[[:space:]]*//p' .pre-commit-config.yaml 2>/dev/null | head -1 | sed -e "s/^'//" -e "s/'$//" -e 's/^"//' -e 's/"$//')"
population() { if [ -n "$EXCL" ]; then git ls-files "$@" | grep -v -E "$EXCL" || true; else git ls-files "$@"; fi; }
hookmeta() { python3 - "$1" "$2" <<'PYHOOK' 2>/dev/null
import re, sys
hook, want = sys.argv[1], sys.argv[2]
try:
    lines = open(".pre-commit-config.yaml", encoding="utf-8").read().splitlines()
except OSError:
    sys.exit(0)
block, inside, depth = [], False, None
for line in lines:
    stripped = line.strip()
    if re.match(r"-\s*id:\s*" + re.escape(hook) + r"\s*$", stripped):
        inside, depth = True, len(line) - len(line.lstrip())
        continue
    if inside:
        ind = len(line) - len(line.lstrip())
        if stripped.startswith("- ") and ind <= depth:
            break
        if stripped and ind <= depth and not stripped.startswith("#"):
            break
        block.append(stripped)
def unq(v):
    v = v.strip()
    return v[1:-1] if len(v) >= 2 and v[0] == v[-1] and v[0] in "'\"" else v
if want == "exclude":
    for b in block:
        if b.startswith("exclude:"):
            print(unq(b.split(":", 1)[1])); break
    sys.exit(0)
args = []
i = 0
while i < len(block):
    b = block[i]
    if b.startswith("args:"):
        rest = b.split(":", 1)[1].strip()
        if rest.startswith("["):
            args += [unq(x) for x in re.split(r",\s*", rest.strip("[]")) if x.strip()]
        else:
            i += 1
            while i < len(block) and block[i].startswith("- "):
                args.append(unq(block[i][2:])); i += 1
            continue
    i += 1
print(" ".join(args))
PYHOOK
}
hookpopulation() { ex="$(hookmeta "$1" exclude)"; shift; if [ -n "$ex" ]; then population "$@" | grep -v -E "$ex" || true; else population "$@"; fi; }
`

// fetchBinary is the mirror-then-upstream download the sweep's oras
// provisioning already spells out, lifted into a function because two more
// atoms now need it.
//
// BOTH SOURCES FAILING IS A 2, NEVER A FALLTHROUGH. A compose spec that was
// never parsed and a policy suite that never ran are not a clean tree; they are
// the same shape as the zero-file scan this module exists to delete, arriving
// through the provisioning door instead of the ruleset one.
const fetchBinary = `fetch_binary() {
  dest=$1; shift
  command -v python3 >/dev/null 2>&1 || return 1
  python3 - "$dest" "$@" <<'PYFETCH'
import sys, urllib.request
dest = sys.argv[1]
for url in sys.argv[2:]:
    try:
        urllib.request.urlretrieve(url, dest)
        sys.exit(0)
    except Exception as exc:
        print("could not fetch %s: %s" % (url, exc), file=sys.stderr)
sys.exit(1)
PYFETCH
}
`

// composeSurface is the condition every compose: atom shares — does this
// repository track a compose spec at all?
//
// THE SURFACE IS TRACKED FILES, NOT A TREE WALK, for the reason worktreeRepo
// states: a compose file sitting in a gitignored scratch directory is not a
// spec this repository ships, and no repo's gate should turn on one.
//
// AND THE SCAN'S OWN EXIT CODE IS READ, because "no compose file" and "the scan
// broke" are the same empty string. That conflation is the exact defect the
// ported workflows had to fix twice in their own bodies (nas01-stacks
// validate.yml:63-79, llm01-stacks validate.yml:56-70) and the one that turned
// 57 repos into false cannot-runs on ca-sweep-manual-1788973171. An ABSENT read
// off a broken scan is an absence this repository never declared.
func composeSurface(id string) string {
	return `git ls-files > /tmp/tracked-files 2>/dev/null || { echo "` + id + `: CANNOT RUN - git ls-files could not enumerate the repository, so its compose surface is unknown rather than empty." >&2; exit 2; }
surface_rc=0
specs=$(grep -E '(^|/)(docker-)?compose\.ya?ml$' /tmp/tracked-files) || surface_rc=$?
if [ "$surface_rc" -gt 1 ]; then echo "` + id + `: CANNOT RUN - the compose-surface scan itself failed (grep exit $surface_rc). An empty surface read off a broken scan is an absence this repository never declared." >&2; exit 2; fi
if [ -z "$specs" ]; then echo "` + id + `: ABSENT - this repository tracks no compose.yaml/compose.yml, so it declares no compose spec. Most of the fleet is Kubernetes YAML, which this says nothing about."; exit 0; fi
`
}

// composeClient provisions the parser, PINNED.
//
// The lane images carry no docker and no compose plugin — they are CI images
// for language toolchains — so the client is fetched the way oras is: the Nexus
// mirror first, upstream second, and a failure of both is a 2. An existing
// `docker compose` or `docker-compose` is preferred when an image happens to
// carry one, because parsing with the client already present beats a 75 MB
// download for the same answer.
//
// NO DAEMON IS INVOLVED. `config` is a pure client-side parse — it reads the
// file, resolves extends/include and validates the schema — so nothing here
// needs a docker socket, which is what lets the atom run in a container that
// has no access to one.
const composeClient = `if docker compose version >/dev/null 2>&1; then
  compose_client() { docker compose "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
  compose_client() { docker-compose "$@"; }
else
  fetch_binary /usr/local/bin/docker-compose "$COMPOSE_MIRROR" "$COMPOSE_URL" || { echo "compose:config: CANNOT RUN - the pinned docker/compose client (v${COMPOSE_VERSION}) could not be fetched from the mirror or from upstream. Refusing to report a parsed tree that was never parsed." >&2; exit 2; }
  chmod +x /usr/local/bin/docker-compose || { echo "compose:config: CANNOT RUN - the compose client downloaded but could not be made executable." >&2; exit 2; }
  compose_client() { /usr/local/bin/docker-compose "$@"; }
fi
compose_client version >/dev/null 2>&1 || { echo "compose:config: CANNOT RUN - a compose client is present but does not run, so no spec was parsed." >&2; exit 2; }
`

// diesShape is the condition every dies: atom shares.
//
// TWO MARKERS, BOTH REQUIRED, because either alone is ambiguous: policy/ turns
// up in more than one repo in this fleet, and fleet/stars/ is a name a fleet
// inventory could reasonably take. Together they name foundry-dies — the repo
// that owns the policy bundle AND the star roster the bundle is built from —
// and nothing else in custody carries both. Everywhere else these atoms report
// ABSENT and say why, rather than going quiet.
func diesShape(id string) string {
	return `if [ ! -f policy/.manifest ] || [ ! -d fleet/stars ]; then echo "` + id + `: ABSENT - this tree is not the policy die's source. It needs both policy/.manifest and fleet/stars/, and this one does not carry both."; exit 0; fi
`
}

// opaClient provisions opa AT THE PINNED VERSION, and the pin is load-bearing.
//
// The rego language version is a property of the binary: a suite written for v1
// semantics graded by a different major answers a different question, and "the
// policy suite passed" would then be a true statement about the wrong language.
// The workflow this ports pinned 1.18.0 by hand (foundry-dies
// .forgejo/workflows/ci.yml:47-53); the pin moves here with it.
//
// The version is CHECKED rather than assumed, so an image that already ships
// some opa cannot silently supply it — and so the download does not repeat when
// what is on disk is already the right one.
func opaClient(id string) string {
	return `OPA=/usr/local/bin/opa
if ! "$OPA" version 2>/dev/null | grep -qx "Version: ${OPA_VERSION}"; then
  fetch_binary "$OPA" "$OPA_MIRROR" "$OPA_URL" || { echo "` + id + `: CANNOT RUN - opa ${OPA_VERSION} could not be fetched from the mirror or from upstream. A policy suite that never ran is not a policy suite that passed." >&2; exit 2; }
  chmod +x "$OPA" || { echo "` + id + `: CANNOT RUN - the opa binary downloaded but could not be made executable." >&2; exit 2; }
fi
"$OPA" version >/dev/null 2>&1 || { echo "` + id + `: CANNOT RUN - opa is on disk but does not run." >&2; exit 2; }
`
}

// diesBundle builds the artifact and takes its data document out, because THE
// SOURCE TREE IS NOT A PROXY FOR THE ARTIFACT.
//
// That is the whole reason ci.yml's third gate exists, and it is worth keeping
// verbatim: `opa test policy/` passes on a tree whose BUILT BUNDLE is empty.
// Directory (--data) mode loads every *.json and merges by top-level key;
// bundle mode reads ONLY files literally named data.json. A tree using
// arbitrary JSON names tests green and builds an artifact with data.json == {},
// which makes star_only undefined, which makes the visibility comprehension
// collect nothing, which makes EVERY verb visible to EVERY principal.
// Fail-open, silent, and green the whole way down.
//
// A BUILD THAT DID NOT COMPLETE IS A 2; A BUILD THAT PRODUCED A BUNDLE WITH NO
// data.json IS A 1. The first is provisioning — opa could not do its job. The
// second IS the defect above, arriving exactly as described, and calling it
// "could not run" would file the finding as an absence.
func diesBundle(id string) string {
	return `"$OPA" build -b policy/ -o /tmp/dies-bundle.tar.gz --revision "$(git rev-parse HEAD 2>/dev/null || echo unknown)" --ignore '*_test.rego' > /tmp/dies-build.out 2>&1 || { echo "` + id + `: CANNOT RUN - opa build did not produce a bundle, so there is no artifact to interrogate." >&2; cat /tmp/dies-build.out >&2; exit 2; }
if ! tar xzOf /tmp/dies-bundle.tar.gz /data.json > /tmp/dies-data.json 2>/tmp/dies-tar.err; then
  cat /tmp/dies-tar.err >&2
  echo "` + id + `: the built bundle carries NO data.json member at all. Bundle mode reads only files literally named data.json, so a tree using arbitrary JSON names tests green and ships an empty data document - star_only undefined, the visibility comprehension collecting nothing, every verb visible to every principal." >&2
  exit 1
fi
`
}

var Atoms = []AtomDef{
	// ---- fleet: every repository, whatever it is written in ----
	{
		ID: "fleet:check-yaml", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "Every YAML file in the tree parses.",
		// THE HOOK'S OWN ARGS AND EXCLUDE, when the repo's config states them;
		// and multi-document YAML is VALID YAML here whatever the config says.
		// pre-commit's check-yaml refuses a second document by default, a
		// stylistic guard no repo in this fleet relies on — while every
		// Kubernetes manifest (infra, the host stacks, ansible's k3s files)
		// is a stream of them. MEASURED 2026-09-10T01:2xZ on infra, a repo
		// with no pre-commit config at all: "expected a single document …
		// but found another document" on a k3s manifest, red on the runner's
		// gate for a file kubectl applies daily.
		Script: provisionGuard + worktreeRepo + gatePopulation + `guard uvx --from pre-commit-hooks check-yaml --help
files=$(hookpopulation check-yaml -- '*.yml' '*.yaml')
if [ -z "$files" ]; then echo "fleet:check-yaml: no YAML in this repository"; exit 0; fi
args="$(hookmeta check-yaml args)"
case " $args " in *" --allow-multiple-documents "*|*" -m "*) ;; *) args="$args --allow-multiple-documents";; esac
uvx --from pre-commit-hooks check-yaml $args $files || exit 1
echo "fleet:check-yaml: parsed all YAML"`,
	},
	{
		ID: "fleet:check-added-large-files", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No file in the tree exceeds 500 KB.",
		// THE POPULATION IS THE REPOSITORY'S TRACKED FILES. pre-commit's own
		// hook reads the index; this reads the nearest thing the engine can be
		// handed (see worktreeRepo). It walked the directory until 2026-09-09,
		// when tongs answered 40+ findings, every one a file under target/ that
		// git ignores. The assertion is unchanged: over 500 KiB, the same
		// threshold as --maxkb=500.
		// --maxkb from the repo's own hook args when stated (pre-commit's
		// default is 500); the hook-level exclude applies to the population.
		Script: worktreeRepo + gatePopulation + `maxkb=500
for a in $(hookmeta check-added-large-files args); do case "$a" in --maxkb=*) maxkb="${a#--maxkb=}";; esac; done
limit=$((maxkb * 1024))
big=$(hookpopulation check-added-large-files | tr '\n' '\0' | xargs -0 -r stat -c '%s %n' 2>/dev/null | awk -v lim="$limit" '$1 > lim { $1=""; sub(/^ /, ""); print }')
if [ -n "$big" ]; then echo "files over ${maxkb} KB:"; echo "$big"; exit 1; fi
echo "fleet:check-added-large-files: nothing over ${maxkb} KB"`,
	},
	{
		ID: "fleet:check-merge-conflict", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No conflict markers were committed.",
		// Only the two ANCHORED markers, not the bare row of equals signs: that
		// is a setext heading in Markdown and a table rule in reStructuredText,
		// and matching it turns every docs repo red for a reason nobody can act
		// on.
		//
		// OVER THE TRACKED FILES, not the directory. A conflict marker inside a
		// gitignored build artifact was never committed, and no --exclude-dir
		// list can name every generator (target/, .venv/, dist/, …). See
		// worktreeRepo.
		Script: worktreeRepo + `hits=$(git ls-files -z | xargs -0 -r grep -In -E '^(<<<<<<< |>>>>>>> )' 2>/dev/null || true)
if [ -n "$hits" ]; then echo "$hits"; exit 1; fi
echo "fleet:check-merge-conflict: no conflict markers"`,
	},
	{
		ID: "fleet:detect-secrets", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No new secret against the repository's .secrets.baseline.",
		// THE TREE HAS TO BE A REPOSITORY git CAN READ, for two reasons.
		//
		// detect-secrets-hook shells out to git while it decides whether the
		// baseline is current, and on a linked worktree that answered "fatal:
		// not a git repository" and exited 1 — a FINDING, for a scan that never
		// happened. Measured on a foundry-stocks worktree 2026-09-09. See
		// worktreeRepo.
		//
		// AND THE POPULATION IS git ls-files, NOT A TREE WALK. The baseline is
		// keyed on the path AS THE SCANNER WAS GIVEN IT, and the fleet's
		// baselines are written by pre-commit, which passes git-relative paths.
		// The old `find . -print` handed detect-secrets "./bases/x.yaml", which
		// matches no key in a baseline holding "bases/x.yaml", so EVERY
		// excused finding came back as a new secret — 200+ of them on
		// foundry-stocks, all of them already in its baseline. The walk also
		// scanned gitignored build junk (a stray .pytest_cache turned up in
		// that run), which cannot be committed and so cannot be a finding
		// about this repository.
		Script: provisionGuard + `if [ ! -f .secrets.baseline ]; then echo "fleet:detect-secrets: CANNOT RUN - no .secrets.baseline at the repository root. Refusing to report success without scanning." >&2; exit 2; fi
` + worktreeRepo + gatePopulation + `guard uvx --from detect-secrets detect-secrets-hook --help
files=$(hookpopulation detect-secrets)
if [ -z "$files" ]; then echo "fleet:detect-secrets: CANNOT RUN - the repository has no tracked file to scan" >&2; exit 2; fi
args="$(hookmeta detect-secrets args)"
case " $args " in *" --baseline "*) ;; *) args="$args --baseline .secrets.baseline";; esac
uvx --from detect-secrets detect-secrets-hook $args $files || exit 1
echo "fleet:detect-secrets: clean against .secrets.baseline"`,
	},
	{
		ID: "fleet:stop-justifications", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No silent suppression of any gate — a suppression carries a tool-conflict line.",
		// The script is READ AT ITS ONE HOME, not vendored. A missing source is
		// exit 2, never 0 — the same contract the pre-commit hook states, for
		// the same reason: pre-commit hides a passing hook's output, so a silent
		// skip is indistinguishable from a clean scan.
		//
		// TWO THINGS ABOUT git, BOTH MEASURED (foundry-tools#7626, 2026-09-09).
		//
		// FIRST, git ABSENT IS A CANNOT RUN, not a finding. The canonical script
		// enumerates the tree with subprocess.run(["git", ...]), which raises
		// FileNotFoundError when git is not on PATH — an uncaught traceback, so
		// python exited 1 and the old `|| exit 1` filed it as FINDINGS. A check
		// that could not find its tool has not found anything wrong; it has not
		// looked. The guard below is the shape every other provisioning probe in
		// this table takes, and the exit code is now passed through rather than
		// flattened to 1, so a CANNOT RUN the script itself reports (its own
		// exit 2 — "refusing to report success without scanning") survives.
		//
		// SECOND, A LINKED WORKTREE'S `.git` IS A FILE, AND IT DANGLES IN HERE.
		// It holds `gitdir: <primary>/.git/worktrees/<name>`, an absolute host
		// path that does not exist inside the container, so `git ls-files`
		// answers "fatal: not a git repository" and the script reports CANNOT
		// RUN — measured against a tartarus worktree in the engine. That is not
		// an edge case: the pre-push gate hook hands the engine `--source="$PWD"`
		// and this fleet works in linked worktrees, so it is the COMMON case.
		// The mounted tree is therefore given a throwaway repository of its own
		// and its index filled from the non-ignored files, which is the same
		// population every other atom here scans (they all walk with `find .`)
		// and is the tree the push is actually carrying. The origin URL is
		// reconstructed from the gitdir path because repo_name() reads it: the
		// DIRECTORY_EXEMPT rows are keyed on the repository, and an exemption
		// Rob granted must not evaporate because the push came from a worktree.
		Script: `if [ ! -f /stocks/ci/lib/stop_justifications.py ]; then echo "fleet:stop-justifications: CANNOT RUN - canonical source not reachable through the door" >&2; exit 2; fi
command -v python3 >/dev/null 2>&1 || { echo "fleet:stop-justifications: CANNOT RUN - python3 is not on PATH in this lane image." >&2; exit 2; }
` + worktreeRepo + `python3 /stocks/ci/lib/stop_justifications.py .
code=$?
[ "$code" -eq 0 ] || exit "$code"
echo "fleet:stop-justifications: no unexcused suppressions"`,
		NeedsStocks: true,
	},
	{
		ID: "fleet:sast-ruleset-lanes", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "The SAST ruleset declares every lane this repository actually builds.",
		// Ported from the go_B pre-commit hook. It is the companion to the
		// zero-file refusal and closes the one case that refusal structurally
		// cannot see: a ruleset that matches SOMETHING and misses the rest still
		// exits 0 (foundry-stocks#4949 — chaos scanned 28 of 2636 files, themis
		// 26 of 2221, both green).
		Script: `test -d rules/sast || { echo "fleet:sast-ruleset-lanes: ABSENT - no rules/sast in this tree"; exit 0; }
decl=$(cat rules/sast/*.yml 2>/dev/null | grep -vE "^[[:space:]]*#" | grep -oE "languages:[[:space:]]*\[[^]]*\]" | sed -E "s/.*\[//;s/\]//" | tr "," "\n" | sed -E "s/[^a-z]//g" | grep -vE "^$" | sort -u)
missing=""
for pair in go.mod:go Cargo.toml:rust pyproject.toml:python; do
  f=${pair%%:*}; lang=${pair##*:}
  test -f "$f" || continue
  printf "%s\n" "$decl" | grep -qx "$lang" || missing="$missing $lang($f)"
done
if test -f package.json; then
  printf "%s\n" "$decl" | grep -qxE "typescript|javascript" || missing="$missing typescript-or-javascript(package.json)"
fi
if test -z "$missing"; then echo "fleet:sast-ruleset-lanes: ruleset declares every lane this repo builds"; exit 0; fi
echo "sast-ruleset-lanes: rules/sast declares [$(printf '%s' "$decl" | tr '\n' ' ')] but this repo also builds:$missing" >&2
echo "A ruleset that never names a lane never examines it, and opengrep still exits 0 whenever some OTHER language matched - the partial-scan case the zero-file refusal cannot see (foundry-stocks#4949)." >&2
exit 2`,
	},
	{
		ID: "fleet:orbit-drift", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "This repo's declared seams agree with the canonical contracts in foundry-dies/orbits.",
		// The seam half of blast radius. A star's orbit.toml says which edges it
		// takes part in, so a session standing in the repo can see who it breaks
		// without cloning anything; this atom asks whether that declaration still
		// matches the canonical contract.
		//
		// IT COMPARES A DIGEST, NOT A VERSION INTEGER, and that is deliberate.
		// Two hand-bumped integers, in two repos, raised in two separate commits,
		// will skew — and skew across a seam is precisely the contract-drift
		// condition these files exist to detect. A check whose own failure mode is
		// the thing it detects is not a check. Bytes either hash to what the star
		// recorded or they do not.
		//
		// THE DOOR IS THE SAME ONE dies:contracts READS, anonymously, over the raw
		// API. It is still forgejo, which is being sunset; when that read moves,
		// it moves for both atoms together rather than one of them drifting off
		// alone.
		//
		// An edge that declares no digest is a FINDING, not a pass. It means the
		// repo named a seam and pinned nothing, so this atom compared nothing —
		// and "nothing to check" is not "checked and clean", which is this
		// module's founding argument.
		Script: provisionGuard + `test -f orbit.toml || { echo "fleet:orbit-drift: ABSENT - no orbit.toml in this tree, so this repo declares no seams"; exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "fleet:orbit-drift: CANNOT RUN - python3 is not on PATH in this lane image." >&2; exit 2; }
runpy() { python3 "$@"; }
if ! python3 -c 'import tomllib' >/dev/null 2>&1; then
  guard uv --version
  runpy() { uv run --no-project --quiet --with 'tomli>=2.0' python3 "$@"; }
  runpy -c 'import tomli' >/dev/null 2>&1 || { echo "fleet:orbit-drift: CANNOT RUN - neither tomllib nor tomli is importable, so orbit.toml cannot be read." >&2; exit 2; }
fi
cat > /tmp/orbit-drift.py <<'PYORBIT'
import hashlib, sys, urllib.error, urllib.request

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

DOOR = "https://forgejo.notusmi.com/api/v1/repos/foundry/foundry-dies/raw/orbits"

try:
    doc = tomllib.loads(open("orbit.toml", encoding="utf-8").read())
except Exception as exc:
    print("fleet:orbit-drift: CANNOT RUN - orbit.toml did not parse: %s" % exc, file=sys.stderr)
    sys.exit(2)

edges = [(d, e) for d in ("consumes", "produces") for e in doc.get(d, [])]
if not edges:
    print("fleet:orbit-drift: orbit.toml declares no seams")
    sys.exit(0)

agree, drift, unpinned = [], [], []
for direction, edge in edges:
    peer = edge.get("from") or edge.get("to") or "?"
    name = edge.get("contract")
    if not name:
        unpinned.append("%s %s names no contract" % (direction, peer))
        continue
    url = "%s/%s.toml" % (DOOR, name)
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            body = resp.read()
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            drift.append("%s: names contract '%s', which is not in foundry-dies/orbits" % (peer, name))
            continue
        print("fleet:orbit-drift: CANNOT RUN - the door answered HTTP %s for %s. A contract that could not be fetched is not a contract that agrees." % (exc.code, url), file=sys.stderr)
        sys.exit(2)
    except Exception as exc:
        print("fleet:orbit-drift: CANNOT RUN - the door is unreachable (%s): %s" % (url, exc), file=sys.stderr)
        sys.exit(2)
    # A BODY THAT IS NOT A CONTRACT IS NOT A CONTRACT THAT DISAGREES. The door
    # 301s a moved repo and urlopen follows it, but any proxy, login wall or
    # error page in the path answers 200 with HTML — and hashing that yields a
    # confident digest mismatch pointing at the wrong thing entirely. Measured
    # while writing this atom: the rob/ org now redirects to foundry/, and a
    # fetch that did not follow it hashed 199 bytes of "Moved Permanently".
    try:
        parsed = tomllib.loads(body.decode("utf-8"))
    except Exception as exc:
        print("fleet:orbit-drift: CANNOT RUN - %s did not answer a TOML document (%s). A body that is not a contract is not a contract that disagrees." % (url, exc), file=sys.stderr)
        sys.exit(2)
    if "verbs" not in parsed:
        print("fleet:orbit-drift: CANNOT RUN - %s parsed as TOML but carries no verbs key, so it is not a seam contract." % url, file=sys.stderr)
        sys.exit(2)

    want = edge.get("digest")
    got = "sha256:" + hashlib.sha256(body).hexdigest()
    if not want:
        unpinned.append("%s: contract '%s' carries no digest, so nothing was compared" % (peer, name))
    elif want != got:
        drift.append("%s: contract '%s' hashes to %s, orbit.toml records %s" % (peer, name, got, want))
    else:
        agree.append(peer)

if drift or unpinned:
    for line in drift:
        print("orbit-drift: " + line, file=sys.stderr)
    for line in unpinned:
        print("orbit-drift: " + line, file=sys.stderr)
    if drift:
        print("The canonical contract moved and this repo's declaration did not. Re-read foundry-dies/orbits and update orbit.toml, or say why the seam changed.", file=sys.stderr)
    if unpinned:
        print("An edge with no digest pins nothing, so this atom compared nothing for it - and nothing to check is not checked and clean.", file=sys.stderr)
    sys.exit(1)

print("fleet:orbit-drift: %d seam(s) agree with foundry-dies/orbits" % len(agree))
PYORBIT
runpy /tmp/orbit-drift.py`,
	},
	{
		ID: "fleet:opengrep-sast", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "SAST scan that refuses a zero-file scan.",
		// The zero-file refusal, kept as written. "Ran N rules on 0 files: 0
		// findings" exits 0 and renders as Passed, which is indistinguishable
		// from a clean scan — go-repo-template and rust-repo-template shipped
		// the PYTHON ruleset, so every Go and Rust star's security gate had
		// never examined a single file (foundry-stocks#4415).
		// THE BINARY IS BAKED INTO THE LANE IMAGE (stellar_core:*-ci carries
		// opengrep 1.25.0 — measured inside the engine 2026-09-09). The curl
		// install below is the FALLBACK now rather than the path, and that is
		// what closes foundry-tools#7626's first defect: on the old uv base
		// there was no curl at all, so this atom was cannot-run in every
		// repository in the fleet and no push could go green anywhere.
		Script: `test -d rules/sast || { echo "fleet:opengrep-sast: ABSENT - no rules/sast in this tree"; exit 0; }
if ! command -v opengrep >/dev/null 2>&1; then
  command -v curl >/dev/null 2>&1 || { echo "opengrep: CANNOT RUN - not baked into this lane image and no curl to fetch the installer. Refusing to report success without scanning." >&2; exit 2; }
  curl -fsSL https://raw.githubusercontent.com/opengrep/opengrep/main/install.sh -o /tmp/opengrep-install.sh || { echo "opengrep: CANNOT RUN - installer unreachable. Refusing to report success without scanning." >&2; exit 2; }
  sh /tmp/opengrep-install.sh >/dev/null 2>&1 || { echo "opengrep: CANNOT RUN - install failed. Refusing to report success without scanning." >&2; exit 2; }
  PATH="$HOME/.opengrep/cli/latest:$PATH"; export PATH
fi
command -v opengrep >/dev/null 2>&1 || { echo "opengrep: CANNOT RUN - not on PATH after install. Refusing to report success without scanning." >&2; exit 2; }
LANG=C.UTF-8; LC_ALL=C.UTF-8; export LANG LC_ALL
out=$(opengrep scan --config rules/sast --error . 2>&1); rc=$?
printf "%s\n" "$out"
if printf "%s" "$out" | grep -qE "Ran [0-9]+ rules on 0 files"; then
  echo "opengrep: REFUSING a zero-file scan. The ruleset matched no file in this repo, so 0 findings means NOTHING WAS EXAMINED - not that the code is clean. Check that rules/sast declares the language(s) this repo is actually written in." >&2
  exit 2
fi
exit $rc`,
	},

	// ---- go ----
	{
		ID: "go:gofmt", Stage: StagePrecommit, Lane: LaneGo, Image: imageGo,
		Desc: "Every Go file is gofmt-clean.",
		Script: `unformatted=$(gofmt -l $(find . -path ./vendor -prune -o -name '*.go' -print) 2>/dev/null)
if [ -n "$unformatted" ]; then echo "gofmt needed on:"; echo "$unformatted"; exit 1; fi
echo "go:gofmt: clean"`,
	},
	{
		ID: "go:vet", Stage: StagePrecommit, Lane: LaneGo, Image: imageGo,
		Desc:   "go vet ./... reports nothing.",
		Script: `go vet ./... || exit 1; echo "go:vet: clean"`,
	},
	{
		ID: "go:build", Stage: StagePrecommit, Lane: LaneGo, Image: imageGo,
		Desc:   "go build ./... succeeds.",
		Script: `go build ./... || exit 1; echo "go:build: clean"`,
	},
	{
		ID: "go:test-race", Stage: StagePrepush, Lane: LaneGo, Image: imageGo,
		Desc: "go test -race ./... passes.",
		// NeedsDies IS NOT A FACT ABOUT THE GO LANE. hephaestus's internal/slag
		// goldens grade every record committed in foundry-dies, the lane checks
		// out one repo, so they resolved nothing and SKIPPED — which `go test`
		// prints as ok and the gate settles as success. That is #2453, and then
		// #8118 for the same defect one level up: the fix that replaced the
		// home-directory constant documented an arming step nobody built.
		// Mounting the tree here is what lets a repo arm them. A repo with no
		// such test reads FOUNDRY_DIES and does nothing with it.
		NeedsDies: true,
		Script:    `go test -race ./... || exit 1`,
	},
	{
		ID: "go:staticcheck", Stage: StagePrepush, Lane: LaneGo, Image: imageGo,
		Desc: "staticcheck ./... reports nothing.",
		// The engine ends the "CANNOT RUN when not installed locally" branch this
		// check has carried since it was a pre-push hook: the toolchain is the
		// module's, so an absent staticcheck is a provisioning failure — still a
		// 2, never a 0.
		Script: provisionGuard + `guard go install honnef.co/go/tools/cmd/staticcheck@latest
staticcheck ./... || exit 1
echo "go:staticcheck: clean"`,
	},
	{
		ID: "go:govulncheck", Stage: StagePrepush, Lane: LaneGo, Image: imageGo,
		Desc: "govulncheck ./... reports no known vulnerability.",
		Script: provisionGuard + `guard go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./... || exit 1
echo "go:govulncheck: clean"`,
	},

	// ---- python ----
	{
		ID: "python:ruff-check", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc: "ruff lint is clean over every .py in the tree.",
		Script: provisionGuard + `guard uvx ruff@0.16.3 --version
uvx ruff@0.16.3 check . || exit 1
echo "python:ruff-check: clean"`,
	},
	{
		ID: "python:ruff-format", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc: "ruff format --check is clean over the product python.",
		// --check, NEVER the rewrite. The stock hook reformats in place and
		// fails so you re-stage, which has aborted a commit in this fleet before
		// (themis). The path scope is the measurement the go stars' hook
		// records: incidental python (scripts/, conformance recorders) is
		// linted but not formatted; product python under src/ and tests/ is.
		Script: provisionGuard + `guard uvx ruff@0.16.3 --version
if [ -f go.mod ]; then
  targets=""
  for d in src tests; do [ -d "$d" ] && targets="$targets $d"; done
  if [ -z "$targets" ]; then echo "python:ruff-format: ABSENT - a go star with no product python under src/ or tests/"; exit 0; fi
else
  targets="."
fi
uvx ruff@0.16.3 format --check $targets || exit 1
echo "python:ruff-format: clean"`,
	},
	{
		ID: "python:forge-testkit-assertion-free", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc:   "No assertion-free test bodies (forge-testkit).",
		Script: forgeTestkit("assertion-free", "tests/*.py"),
	},
	{
		ID: "python:forge-testkit-fake-placement", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc:   "Fake and Stub doubles live where they belong (forge-testkit).",
		Script: forgeTestkit("fake-placement", "*.py"),
	},
	{
		ID: "python:forge-testkit-schema-budget", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc:   "MCP verb descriptions stay inside the schema budget (forge-testkit).",
		Script: forgeTestkit("schema-budget", "src/*.py"),
	},
	{
		ID: "python:mypy", Stage: StagePrepush, Lane: LanePython, Image: imagePython,
		Desc: "mypy --strict is clean over src and tests.",
		// ONE type checker. pyright ran redundantly on every python pre-push
		// with no incident behind it and is dropped fleet-wide (decided, Rob) —
		// it is deliberately absent from this table, not overlooked.
		Script: provisionGuard + `guard uv --version
targets=""
for d in src tests; do [ -d "$d" ] && targets="$targets $d"; done
if [ -z "$targets" ]; then echo "python:mypy: ABSENT - no src/ or tests/ to type-check"; exit 0; fi
uv run --all-extras mypy --strict $targets || exit 1
echo "python:mypy: clean"`,
	},
	{
		ID: "python:pytest", Stage: StagePrepush, Lane: LanePython, Image: imagePython,
		Desc: "pytest passes.",
		// A repo with nothing for pytest to collect is ABSENT, not red: pytest
		// exits 5 for "no tests ran", and mypy already says ABSENT for the
		// same tree. MEASURED 2026-09-10T01:25Z gate-helios-057545b: a go
		// star with one .py file and no tests/ was red on "no tests ran in
		// 0.39s" — the permanent state of that repo, not a finding.
		Script: provisionGuard + worktreeRepo + gatePopulation + `guard uv --version
if [ ! -d tests ] && [ -z "$(population -- 'test_*.py' '*_test.py')" ]; then echo "python:pytest: ABSENT - no tests/ and no test files"; exit 0; fi
rc=0; uv run --all-extras pytest -q || rc=$?
if [ "$rc" -eq 5 ]; then echo "python:pytest: ABSENT - pytest collected no tests (exit 5)"; exit 0; fi
[ "$rc" -eq 0 ] || exit 1
echo "python:pytest: clean"`,
	},
	{
		ID: "python:pip-audit", Stage: StagePrepush, Lane: LanePython, Image: imagePython,
		Desc: "pip-audit reports no known vulnerability.",
		Script: provisionGuard + `guard uv --version
uv run --with pip-audit pip-audit || exit 1
echo "python:pip-audit: clean"`,
	},

	// ---- rust ----
	{
		ID: "rust:cargo-fmt", Stage: StagePrecommit, Lane: LaneRust, Image: imageRust,
		Desc:   "cargo fmt --all --check is clean.",
		Script: `cargo fmt --all --check || exit 1; echo "rust:cargo-fmt: clean"`,
	},
	{
		ID: "rust:cargo-clippy", Stage: StagePrecommit, Lane: LaneRust, Image: imageRust,
		Desc:   "cargo clippy is clean with warnings denied.",
		Script: `cargo clippy --workspace --all-targets -- -D warnings || exit 1; echo "rust:cargo-clippy: clean"`,
	},
	{
		ID: "rust:cargo-test", Stage: StagePrepush, Lane: LaneRust, Image: imageRust,
		Desc:   "cargo test --workspace passes.",
		Script: `cargo test --workspace || exit 1`,
	},
	{
		ID: "rust:cargo-audit", Stage: StagePrepush, Lane: LaneRust, Image: imageRust,
		Desc: "cargo audit reports no known vulnerability.",
		Script: provisionGuard + `guard cargo install cargo-audit --locked
cargo audit || exit 1
echo "rust:cargo-audit: clean"`,
	},

	// ---- ts ----
	{
		ID: "ts:bun-gate-commit", Stage: StagePrecommit, Lane: LaneTS, Image: imageTS,
		Desc: "bun run gate (format, lint, typecheck, test, build) passes.",
		// The pre-commit hook this ports runs in a checkout that already has
		// node_modules; the gate's checkout has none. MEASURED 2026-09-10T02:38Z
		// gate-calliope-9ed7599: `bun run format:check` → "prettier: command
		// not found", exit 127, a red about the runner, not the repo.
		Script: `bun install --frozen-lockfile || { echo "ts:bun-gate-commit: CANNOT RUN - frozen lockfile install failed" >&2; exit 2; }
bun run gate || exit 1`,
	},
	{
		ID: "ts:bun-gate", Stage: StagePrepush, Lane: LaneTS, Image: imageTS,
		Desc: "bun run gate passes against a frozen lockfile.",
		Script: `bun install --frozen-lockfile || { echo "ts:bun-gate: CANNOT RUN - frozen lockfile install failed" >&2; exit 2; }
bun run gate || exit 1`,
	},
	{
		ID: "ts:bun-audit", Stage: StagePrepush, Lane: LaneTS, Image: imageTS,
		Desc: "bun audit reports nothing at high or above.",
		Script: `BUN_CONFIG_REGISTRY=https://registry.npmjs.org/ bun audit --audit-level=high || exit 1
echo "ts:bun-audit: clean"`,
	},

	// ---- compose: the host stacks. Ported off the act-runner's validate.yml ----
	//
	// nas01-stacks and llm01-stacks ARE the boxes: every compose spec, the
	// Caddyfile, the runner config. Their `validate.yml` was the only thing that
	// had ever validated any of it, and the act-runner it ran on is being
	// removed — so these three atoms are what "validated by the gate or not at
	// all" means for those repos.
	//
	// WHAT A GREEN HERE MEANS, kept from the workflow's own header: "this parses
	// and its schema is valid", nothing stronger. It does not say the file
	// matches what is RUNNING on the box; that is drift, not syntax, and no gate
	// can see it from here.
	{
		ID: "compose:config", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "Every tracked compose spec parses and its schema validates.",
		// --no-interpolate IS LOAD-BEARING. These files use ${VAR:?message} to
		// make a missing variable a DEPLOY-TIME error, which is correct on the
		// box and fatal anywhere no variable is set. Without it the gate would
		// fail on every file for the wrong reason. Schema validation still runs.
		//
		// THE env_file TARGETS ARE STUBBED FIRST, and the stub scan reads its
		// own exit code. env_file targets are secrets and are correctly absent
		// from the repo, but `compose config` hard-errors on a missing env_file
		// before it ever reaches the schema. Nothing here reads a VALUE —
		// --no-interpolate is set — so an empty file is enough.
		//
		// grep is three-valued (0 selected · 1 selected nothing · >=2 the scan
		// broke) and both workflows had to learn that the hard way: a `|| true`
		// collapsed all three into an empty list, printed "nothing to stub",
		// stubbed nothing, and handed the parse a tree missing every file it was
		// supposed to create — a green step reporting a clean scan it never
		// performed (nas01-stacks validate.yml:63-79). rc 1 is the ANSWER; rc
		// >=2 is a refusal.
		Script: worktreeRepo + composeSurface("compose:config") + fetchBinary + composeClient + `stub_rc=0
raw=$(grep -rhoE '[./A-Za-z0-9_-]+\.env' --include='*.yml' --include='*.yaml' . 2>/dev/null) || stub_rc=$?
if [ "$stub_rc" -gt 1 ]; then echo "compose:config: CANNOT RUN - the env_file scan failed (grep exit $stub_rc). Refusing to report 'nothing to stub' from a scan that did not run, and then to parse a tree missing every file it was supposed to create." >&2; exit 2; fi
if [ "$stub_rc" -eq 0 ]; then
  printf '%s\n' "$raw" | sort -u | while read -r p; do
    [ -n "$p" ] || continue
    mkdir -p "$(dirname "$p")" 2>/dev/null
    [ -e "$p" ] || touch "$p" 2>/dev/null || echo "could not stub $p"
  done
else
  echo "no env_file references - nothing to stub"
fi
printf '%s\n' "$specs" > /tmp/compose-specs
fail=0
while read -r f; do
  [ -n "$f" ] || continue
  printf '%-48s' "$f"
  if out=$(compose_client -f "$f" config --no-interpolate --quiet 2>&1); then
    echo "OK"
  else
    echo "FAIL"; printf '%s\n' "$out" | sed 's/^/      /'; fail=1
  fi
done < /tmp/compose-specs
[ "$fail" -eq 0 ] || exit 1
echo "compose:config: every tracked compose spec parses"`,
	},
	{
		ID: "compose:no-tracked-secrets", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No credential-shaped file is tracked in a repository that ships compose specs.",
		// THE IGNORE RULE IS ASSERTED, NOT TRUSTED. Both repos' .gitignore state
		// the rule — "if it holds a credential, it is IGNORED" — and nothing
		// enforced it. An ignore rule only protects files it was written before;
		// this asserts the OUTCOME.
		//
		// THE POPULATION IS BARE `git ls-files`, deliberately NOT the gate
		// population the fleet atoms use. A .env that a repo's pre-commit
		// `exclude:` keeps out of its hooks is still a tracked .env, and a
		// credential does not stop being one because a config said not to look
		// at it. The argument that carries gatePopulation everywhere else —
		// grade the population the hook graded — argues the other way here,
		// because the hook is not what is being ported: the assertion is.
		//
		// The scan's exit code is read for the reason it is read in every other
		// atom on this page: a broken scan and a clean repository produce the
		// same empty string, and only one of them is a pass.
		Script: worktreeRepo + composeSurface("compose:no-tracked-secrets") + `secret_rc=0
hits=$(grep -E '(^|/)\.env$|\.env\.|(^|/)envs/|\.pem$|\.key$|_rsa$' /tmp/tracked-files) || secret_rc=$?
if [ "$secret_rc" -gt 1 ]; then echo "compose:no-tracked-secrets: CANNOT RUN - the credential-shape scan failed (grep exit $secret_rc). Refusing to report a clean tree from a scan that did not run." >&2; exit 2; fi
if [ -n "$hits" ]; then
  echo "Tracked files that must never be committed:" >&2
  printf '%s\n' "$hits" | sed 's/^/  /' >&2
  exit 1
fi
echo "compose:no-tracked-secrets: no credential-shaped file is tracked"`,
	},
	{
		ID: "compose:third-party-pins", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "Zero ${PIN_} image interpolations — the BP6b ratchet stays closed.",
		// The BP6b ratchet (BDTH, Rob-ratified 2026-08-08), fully tightened the
		// night it landed: the literal lane took every third-party image off
		// ${PIN_}, the staged :stable conversion took all 32 first-party stars
		// off it, and compose/pins.env is deleted. A COUNT gate stays closed
		// where a list gate reopens — ANY ${PIN_} image interpolation is a red
		// merge, and the pin era does not reopen (nas01-stacks
		// validate.yml:149-176).
		//
		// A COUNT GATE ONLY STAYS CLOSED IF THE COUNT HAPPENED. grep's rc 1 is
		// the ANSWER this gate wants; rc >=2 is a refusal, because a ratchet
		// cannot report closed on a scan that did not run.
		Script: worktreeRepo + composeSurface("compose:third-party-pins") + `pin_rc=0
hits=$(grep -rn 'image:.*\${PIN_' --include='*.yaml' --include='*.yml' --exclude-dir=.forgejo .) || pin_rc=$?
if [ "$pin_rc" -gt 1 ]; then echo "compose:third-party-pins: CANNOT RUN - the \${PIN_} scan itself failed (grep exit $pin_rc). Refusing to report a closed ratchet on a scan that did not run." >&2; exit 2; fi
n=0
if [ -n "$hits" ]; then n=$(printf '%s\n' "$hits" | wc -l | tr -d ' '); fi
if [ "$n" -ne 0 ]; then
  echo "compose:third-party-pins: ${n} \${PIN_} interpolation(s) found - the pin era does not reopen:" >&2
  printf '%s\n' "$hits" >&2
  exit 1
fi
echo "compose:third-party-pins: zero \${PIN_} interpolations; the pin era stays closed"`,
	},

	// ---- dies: the policy die's source. Ported off ci / contracts / schema ----
	//
	// Six atoms, and THE SPLIT BETWEEN THE FIRST TWO AND THE NEXT TWO IS THE
	// ARGUMENT. `opa test` and the dogfood eval grade the SOURCE; data-keys and
	// canary-visibility grade the ARTIFACT, and the gap between them is
	// measured rather than theoretical: a source tree can pass 312 assertions
	// and build a bundle that admits everything. See diesBundle.
	//
	// build.yml and fleet-bundle.yml are deliberately NOT here. They publish
	// rather than validate, and the bundle recipe lane owns them.
	{
		ID: "dies:opa-test", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "The rego unit and invariant suite passes.",
		// A ZERO-TEST RUN IS REFUSED, which the workflow did not do and this
		// module cannot skip: `opa test` over a policy tree containing no test
		// at all exits 0 (measured against an empty directory, 2026-09-10).
		// That renders as a clean suite and is not one — it is opengrep matching
		// zero files wearing different clothes, and it gets the same answer.
		//
		// opa's OWN CODES ARE THREE-VALUED TOO, and they do not line up with
		// this module's: a failing assertion is exit 2 and a rego parse error is
		// exit 1 (both measured). Passing either through would file a real
		// finding as CANNOT RUN, so both fold to 1 and only a code opa does not
		// use becomes a 2.
		Script: diesShape("dies:opa-test") + fetchBinary + opaClient("dies:opa-test") + `out=$("$OPA" test policy/ -v 2>&1); rc=$?
printf '%s\n' "$out"
case "$rc" in
  0) ;;
  1|2) exit 1 ;;
  *) echo "dies:opa-test: CANNOT RUN - opa exited $rc, which is neither a clean suite (0), a load error (1) nor a failing assertion (2)." >&2; exit 2 ;;
esac
n=$(printf '%s\n' "$out" | sed -n 's|^PASS: \([0-9][0-9]*\)/[0-9][0-9]*$|\1|p' | tail -1)
if [ -z "$n" ] || [ "$n" -eq 0 ]; then
  echo "dies:opa-test: REFUSING a zero-test run. opa test exits 0 over a policy tree with no assertion in it, which renders as a clean suite and is not one - nothing was examined." >&2
  exit 2
fi
echo "dies:opa-test: $n assertion(s) pass"`,
	},
	{
		ID: "dies:admission-dogfood", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "The admission domain admits this repo's own star shape.",
		// The domain that judges every star's slag is asked about the one star
		// whose shape lives in the same repo as the rule. A deny here means the
		// policy has drifted from the fleet it governs, and it shows up on the
		// repo that OWNS the rule rather than on whichever star was poured next
		// — the same alarm-asymmetry argument contracts.yml makes at length.
		//
		// A MISSING FIXTURE IS A 2. The atom's whole content is "the domain
		// admitted THIS input"; with no input there is no claim to make, and
		// exiting 0 would make one anyway.
		//
		// python3 READS THE RESULT, NOT jq. The workflow's runner image carried
		// jq; the lane images are not promised to, and provisioning a second
		// binary to count the length of a JSON array buys nothing. The value
		// extracted is the same one, and a shape this atom cannot read is a 2
		// rather than a deny count nobody computed.
		Script: diesShape("dies:admission-dogfood") + fetchBinary + opaClient("dies:admission-dogfood") + `[ -d policy/admission ] || { echo "dies:admission-dogfood: CANNOT RUN - policy/admission is absent, so there is no admission domain to ask." >&2; exit 2; }
[ -f tests/fixtures/ouranos-self.json ] || { echo "dies:admission-dogfood: CANNOT RUN - tests/fixtures/ouranos-self.json is absent, so there is no own-star shape to submit." >&2; exit 2; }
"$OPA" eval -d policy/admission -i tests/fixtures/ouranos-self.json "data.admission.deny" --format json > /tmp/dies-dogfood.json 2>/tmp/dies-dogfood.err || { echo "dies:admission-dogfood: CANNOT RUN - opa eval did not complete." >&2; cat /tmp/dies-dogfood.err >&2; exit 2; }
cat > /tmp/dies-dogfood.py <<'PYDOG'
import json, sys
try:
    doc = json.load(open("/tmp/dies-dogfood.json"))
    deny = doc["result"][0]["expressions"][0]["value"]
except Exception as exc:
    print("dies:admission-dogfood: CANNOT RUN - opa eval answered a shape this atom cannot read (%s), so there is no deny set to count." % exc, file=sys.stderr)
    sys.exit(2)
print("deny count = %d" % len(deny))
if deny:
    for d in deny:
        print("  %s" % (d,), file=sys.stderr)
    sys.exit(1)
PYDOG
python3 /tmp/dies-dogfood.py
dog_rc=$?
[ "$dog_rc" -eq 0 ] || exit "$dog_rc"
echo "dies:admission-dogfood: the admission domain admits our own star shape"`,
	},
	{
		ID: "dies:data-keys", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "The BUILT bundle carries every data root the policy reads, non-empty.",
		// The artifact gate, asked of the artifact. An empty or partial data
		// document is the silent fail-open diesBundle describes, so the
		// documents the policy actually reads are asserted PRESENT and
		// NON-EMPTY, by name.
		Script: diesShape("dies:data-keys") + fetchBinary + opaClient("dies:data-keys") + diesBundle("dies:data-keys") + `cat > /tmp/dies-datakeys.py <<'PYKEYS'
import json, sys
d = json.load(open("/tmp/dies-data.json"))
required = ["authz_audience", "authz_grants", "authz_meta", "path_grants", "subject_aliases"]
missing = [k for k in required if not d.get(k)]
if missing:
    print("::error::bundle data.json is missing/empty: %s. Bundle mode reads ONLY files named data.json - check the policy/<root>/data.json layout." % missing, file=sys.stderr)
    sys.exit(1)
stars = len(d["authz_audience"].get("star_only", {}))
print("data roots ok; star_only carries %d stars" % stars)
PYKEYS
python3 /tmp/dies-datakeys.py || exit 1
echo "dies:data-keys: the built bundle carries its data"`,
	},
	{
		ID: "dies:canary-visibility", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "The BUILT bundle still hides a curated verb from a session principal.",
		// THE CANARY IS READ OFF THE ROSTER, NOT NAMED, and that is the
		// post-mortem's own recommendation (2026-08-22). The first canary named
		// graph_subscribe, a verb chaos retired in F2; the second named
		// graph_nodes. A named canary goes stale the day its verb leaves the
		// roster and the gate then goes red on a roster that is MORE correct —
		// twice now. So the built bundle's own data.json is asked for chaos's
		// first star_only verb and THAT one is proved: whatever chaos curates
		// first is, by construction, curated. chaos because it is the star with
		// the largest curated surface, and an empty chaos row is itself the
		// failure — nothing curated means the roster did not survive the build.
		//
		// BOTH DIRECTIONS ARE ASSERTED. A curated verb visible to a session
		// principal is the fail-open. A curated verb INVISIBLE to a star
		// principal is the opposite error and just as wrong: curation that
		// narrowed both audiences instead of one.
		Script: diesShape("dies:canary-visibility") + fetchBinary + opaClient("dies:canary-visibility") + diesBundle("dies:canary-visibility") + `CANARY=$(python3 -c 'import json; d = json.load(open("/tmp/dies-data.json")); v = d.get("authz_audience", {}).get("star_only", {}).get("chaos") or []; print(v[0] if v else "")' 2>/dev/null)
if [ -z "$CANARY" ]; then
  echo "dies:canary-visibility: chaos has no star_only row in the built bundle - the roster did not survive the build." >&2
  exit 1
fi
echo "canary: $CANARY (chaos's first star_only verb, read off the bundle)"
probe() {
  printf '{"principal":{"type":"%s","subject":"s"},"verbs":["search","%s"]}' "$1" "$CANARY" > /tmp/dies-probe-in.json
  "$OPA" eval -b /tmp/dies-bundle.tar.gz --stdin-input --format json 'data.authz.visible.allowed' < /tmp/dies-probe-in.json > /tmp/dies-probe-out.json 2>/tmp/dies-probe.err || return 2
  python3 -c 'import json; print(json.dumps(json.load(open("/tmp/dies-probe-out.json"))["result"][0]["expressions"][0]["value"]))' 2>/dev/null || return 2
}
SESSION=$(probe session) || { echo "dies:canary-visibility: CANNOT RUN - the session-principal probe against the built bundle did not evaluate." >&2; cat /tmp/dies-probe.err >&2; exit 2; }
STAR=$(probe star) || { echo "dies:canary-visibility: CANNOT RUN - the star-principal probe against the built bundle did not evaluate." >&2; cat /tmp/dies-probe.err >&2; exit 2; }
echo "session: $SESSION"
echo "star:    $STAR"
if [ "$SESSION" != '["search"]' ]; then
  echo "dies:canary-visibility: a curated verb is VISIBLE to a session principal in the built bundle - the roster did not survive the build." >&2
  exit 1
fi
printf '%s' "$STAR" | python3 -c 'import json, sys; sys.exit(0 if sys.argv[1] in json.load(sys.stdin) else 1)' "$CANARY" || {
  echo "dies:canary-visibility: a curated verb is INVISIBLE to a star principal - curation narrowed both audiences, not one." >&2
  exit 1
}
echo "dies:canary-visibility: the built bundle still hides what it should"`,
	},
	{
		ID: "dies:contracts", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "Every copy of every shared closed set agrees — and the checker is proved to detect first.",
		// THE FIXTURES RUN FIRST AND MUST FAIL. The live check cannot prove the
		// checker DETECTS anything while it is green, so seven fixtures must be
		// caught and three controls must pass. Without the controls the failure
		// loop could be satisfied by a checker that simply fails everything —
		// including a pending entry whose grounds genuinely still hold, which is
		// a legitimate deferral. A gate that cannot fail is a gate that is not
		// there.
		//
		// THE FIXTURES TOUCH NO NETWORK, by the fixture manifest's own design,
		// which is what lets the detection proof stand while the door is down.
		//
		// THE DOOR IS PROBED BEFORE THE LIVE CHECK, and the probe target is READ
		// OUT OF THE MANIFEST rather than named — the same lesson as the canary
		// above. check_contracts.py raises ContractError on an unreachable copy
		// and main() returns 1 for it, which is right for a gate whose runner
		// sat on the same network as the door and wrong for an atom: a copy that
		// could not be FETCHED is not a copy that DISAGREES, and reporting one
		// as the other sends a reader to reconcile lists that may be identical.
		// So an unreachable door is a 2 here and every other answer stays the
		// checker's own.
		Script: provisionGuard + diesShape("dies:contracts") + `[ -f tools/check_contracts.py ] || { echo "dies:contracts: CANNOT RUN - tools/check_contracts.py is absent, so there is no checker to run." >&2; exit 2; }
[ -f tests/contracts/fixtures.toml ] || { echo "dies:contracts: CANNOT RUN - tests/contracts/fixtures.toml is absent, and a gate that cannot prove it detects is a gate that is not there." >&2; exit 2; }
[ -f contracts/contracts.toml ] || { echo "dies:contracts: CANNOT RUN - contracts/contracts.toml is absent, so there is no live manifest to check." >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "dies:contracts: CANNOT RUN - python3 is not on PATH in this lane image." >&2; exit 2; }
runpy() { python3 "$@"; }
if ! python3 -c 'import tomllib' >/dev/null 2>&1; then
  guard uv --version
  runpy() { uv run --no-project --quiet --with 'tomli>=2.0' python3 "$@"; }
  runpy -c 'import tomli' >/dev/null 2>&1 || { echo "dies:contracts: CANNOT RUN - neither tomllib nor tomli is importable, so the checker cannot read a manifest." >&2; exit 2; }
fi
fail=0
for c in lagging undeclared bad_pending unreadable expired_pending undated_pending old_shape_pending; do
  if runpy tools/check_contracts.py --manifest tests/contracts/fixtures.toml --contract "$c" >/dev/null 2>&1; then
    echo "::error::fixture '$c' PASSED - the checker no longer detects it" >&2; fail=1
  else
    echo "  fixture $c: correctly detected"
  fi
done
for c in agreeing holding_pending unmeasurable_pending; do
  if runpy tools/check_contracts.py --manifest tests/contracts/fixtures.toml --contract "$c" >/dev/null 2>&1; then
    echo "  control $c: correctly passed"
  else
    echo "::error::control '$c' FAILED - the checker invents divergence" >&2; fail=1
  fi
done
[ "$fail" -eq 0 ] || exit 1
cat > /tmp/dies-door-probe.py <<'PYDOOR'
import sys, urllib.error, urllib.request
try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib
DOOR = "https://forgejo.notusmi.com/api/v1/repos/rob"
try:
    doc = tomllib.loads(open("contracts/contracts.toml", encoding="utf-8").read())
except Exception as exc:
    print("could not read the manifest for the reachability probe (%s); letting the checker be the judge" % exc)
    sys.exit(0)
target = None
for spec in doc.get("contracts", {}).values():
    for copy in spec.get("copies", []):
        src = copy.get("source", {})
        if "repo" in src and "path" in src:
            target = "%s/%s/raw/%s" % (DOOR, src["repo"], src["path"])
            break
    if target:
        break
if target is None:
    print("every declared copy is local; the live check needs no network")
    sys.exit(0)
try:
    with urllib.request.urlopen(target, timeout=30) as resp:
        print("door raw API reachable (%s -> HTTP %s)" % (target, resp.status))
except urllib.error.HTTPError as exc:
    print("door raw API answered HTTP %s for %s; that is the checker's finding to make, not a provisioning failure" % (exc.code, target))
except Exception as exc:
    print("the door's raw API is unreachable (%s): %s" % (target, exc), file=sys.stderr)
    sys.exit(2)
PYDOOR
runpy /tmp/dies-door-probe.py
probe_rc=$?
if [ "$probe_rc" -ne 0 ]; then
  echo "dies:contracts: CANNOT RUN - the door's raw API is unreachable, so the remote copies cannot be read. A copy that could not be fetched is not a copy that agrees." >&2
  exit 2
fi
runpy tools/check_contracts.py || exit 1
echo "dies:contracts: the fixtures prove the gate detects, and every copy of every shared closed set agrees"`,
	},
	{
		ID: "fleet:witness", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet, NeedsStocks: true,
		Desc: "Every changed .py/.go file is shown to the code witness (narcissus): a canonical-class or Standard match is a finding, a Convention is advisory, novel is clean.",
		// THE PRE-GATE SOCKET, BACK AS AN ATOM. Born as a Tekton Task beside the
		// gate (The Thesis Project F8) that reached narcissus through hades over
		// mTLS with an identity minted for that Task alone; Tekton left on
		// 2026-09-09 and the socket went with it. The identity was that
		// pipeline's contrivance, not the witness's requirement: narcissus's
		// plaintext MCP port answers any in-cluster caller, MEASURED 2026-09-10
		// from inside a container on the fleet's dagger engine. So the atom
		// speaks to narcissus directly and needs nothing the other atoms lack.
		//
		// THE SCRIPT LIVES IN foundry-stocks (ci/lib/gate/witness.py, tested
		// offline by witness.test.sh) and is READ AT ITS ONE HOME through the
		// /stocks mount — the same rule the permit script follows. This body
		// only provisions and points: python3, git (worktreeRepo), the script.
		//
		// WHAT IT NEEDS THAT OTHER ATOMS DO NOT: the change set. GATE_BASE is
		// the pull's merge base, handed in by Verdicts' `base` argument (the
		// door passes CA_GATE_BASE); empty means the tip against its parent,
		// which is also what a local run gets. And the in-cluster port: a dev
		// box that cannot reach narcissus lands on 2, could-not-consult, and
		// says so — never a pass.
		Script: provisionGuard + worktreeRepo + `guard python3 --version
[ -f /stocks/ci/lib/gate/witness.py ] || { echo "fleet:witness: CANNOT RUN - /stocks/ci/lib/gate/witness.py is absent; foundry-stocks did not mount at its one home." >&2; exit 2; }
export WITNESS_DIR=/tmp/witness
python3 /stocks/ci/lib/gate/witness.py; rc=$?
[ -f /tmp/witness/reason ] && cat /tmp/witness/reason
exit $rc`,
	},
	{
		ID: "dies:schema", Stage: StagePrepush, Lane: LaneAny, Image: imageFleet,
		Desc: "The slag schema is a valid Draft 2020-12 document and every v2 record satisfies it.",
		// TWO ASSERTIONS ABOUT THE SCHEMA, and the second is the one
		// check_schema does not make. `required` naming a property that is not
		// DEFINED is legal to the metaschema and, under additionalProperties
		// false, makes the schema reject EVERY document — so pour would refuse
		// every well-formed melt, and the failure would surface at a pour rather
		// than here.
		//
		// AND THE v2 RECORDS ARE VALIDATED, which v1's never were: nothing in CI
		// ever checked a slag record against the schema, so the schema drifted
		// silently. The filename and meta.name rules ride along because a record
		// named anything other than <name>.slag beside its own directory is one
		// the loader will not find.
		//
		// jsonschema COMES THROUGH uv, NOT pip. The workflow's `python3 -m pip
		// install` assumed the act image's interpreter; the lane images are
		// uv-managed, where that install is refused outright as an
		// externally-managed environment. The provision is PROBED before the
		// gate runs, so a resolver that could not reach an index is a 2 rather
		// than a schema finding nobody made.
		Script: provisionGuard + diesShape("dies:schema") + `[ -f schema/slag.schema.json ] || { echo "dies:schema: CANNOT RUN - schema/slag.schema.json is absent, so there is no payload to validate." >&2; exit 2; }
[ -f schema/slag-v2.schema.json ] || { echo "dies:schema: CANNOT RUN - schema/slag-v2.schema.json is absent, so the v2 records cannot be discriminated." >&2; exit 2; }
guard uv --version
uv run --no-project --quiet --with 'jsonschema>=4.20' python3 -c 'import jsonschema' >/dev/null 2>&1 || { echo "dies:schema: CANNOT RUN - jsonschema could not be provisioned. Refusing to report a validated schema that was never validated." >&2; exit 2; }
cat > /tmp/dies-schema.py <<'PYSCHEMA'
import glob, json, sys
from jsonschema import Draft202012Validator

schema = json.load(open("schema/slag.schema.json"))
Draft202012Validator.check_schema(schema)
props = set(schema.get("properties", {}))
missing = [k for k in schema.get("required", []) if k not in props]
if missing:
    sys.exit("::error::required names properties that are not defined: %s" % missing)
print("valid Draft 2020-12 schema - %s" % schema.get("$id"))
print("%d required keys, all defined in properties" % len(schema.get("required", [])))

v2 = json.load(open("schema/slag-v2.schema.json"))
Draft202012Validator.check_schema(v2)
v = Draft202012Validator(v2)
bad = 0
records = sorted(glob.glob("fleet/stars/*/*.slag"))
for rec in records:
    name = rec.split("/")[2]
    doc = json.load(open(rec))
    findings = [
        "%s: %s" % ("/".join(str(x) for x in e.path) or "<root>", e.message)
        for e in sorted(v.iter_errors(doc), key=lambda e: list(e.path))
    ]
    if rec != "fleet/stars/%s/%s.slag" % (name, name):
        findings.append("<root>: a v2 record is named <name>.slag beside its own directory, not %s" % rec)
    if doc.get("meta", {}).get("name") != name:
        findings.append("meta/name: meta.name must equal the directory name %r" % name)
    for f in findings:
        bad += 1
        print("::error file=%s::%s" % (rec, f), file=sys.stderr)
print("%d v2 record(s) validated against %s" % (len(records), v2.get("$id")))
if bad:
    sys.exit("::error::%d v2 record finding(s)" % bad)
PYSCHEMA
uv run --no-project --quiet --with 'jsonschema>=4.20' python3 /tmp/dies-schema.py || exit 1
echo "dies:schema: the payload is a valid, satisfiable schema and every v2 record conforms"`,
	},

	// ---- sweep: repo cadence. NEVER IN A PULL'S PATH ----
	//
	// These describe a REPOSITORY rather than a change, so their answer cannot
	// differ between two pulls against the same repo — and running them per
	// pull leaves every repository nobody opened a PR against unevaluated
	// indefinitely. That is the whole of CA F9: the digest rots while the tree
	// sits still, so the probe has to be a clock, not a diff
	// (foundry-stocks/ci/lib/digest-pins.sh says exactly that in its own
	// header, and nothing had ever dispatched it).
	//
	// The absence is the acceptance: no atom below may appear in a pull's
	// path. PullPathAtoms is what makes that structural rather than a
	// convention, and TestNoSweepAtomOnThePullPath asserts it.
	// ---- mutation ----
	//
	// ONE SHAPE, FOUR LANGUAGES. Each atom runs the canonical script at its one
	// home (/stocks/ci/lib/mutation/<lang>.sh) phase by phase, in DIFF mode
	// against GATE_BASE — the pull's merge base as the door names it — and
	// exits with the verdict the score phase wrote: 0 clean, 1 survivors, 2
	// could not measure. The phases themselves never exit non-zero (reaching a
	// verdict is the score phase's job), so a phase that does is a broken
	// script, said as CANNOT RUN.
	//
	// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
	// repository whole and checks the head out, so `git cat-file -e <base>`
	// answers and the diff is real. A local pre-push run hands the engine a
	// linked worktree, which worktreeRepo turns into a throwaway repository
	// with no history: the resolve phase then stands down 0 with "no usable PR
	// base sha", printed, and the door's Job is the one that measures.
	//
	// critical_modules IS THE REPO'S DECLARATION for python, rust and ts, read
	// off .copier-answers.yml where the template question puts it — the same
	// string the retired mutation.yml rendered into its `modules` input. Blank
	// means the repo opted out (the template's own help text says so), which is
	// ABSENT, not a finding. Go needs none: gremlins scopes to the diff itself,
	// exactly as mutation-go did.
	//
	// ci/mutation.env IS THE REPO'S KNOBS, when it has any: KEY=VALUE lines of
	// MUT_* the atom sources before the phases, so a repo can say what the
	// retired workflow's inputs said — a workdir, a setup command, an exclude,
	// a test CPU bound — without a workflow file to say it in. Absent means
	// the script's own defaults, and a repo that never needs one never has one.
	// Sourced AFTER the atom's own exports so the repo wins.
	//
	// GENERATED GO IS EXCLUDED BY DEFAULT. MEASURED on this stage's first live
	// run (2026-09-11, foundry-tools' own diff): of 14 survivors, one was in
	// dagger.gen.go — dagger's codegen, which no test of ours covers and none
	// should. The retired mutation-go.yml carried the same exclusion per repo;
	// here it is the go atom's default, and ci/mutation.env can widen it.
	{
		ID: "go:mutation", Stage: StageMutation, Lane: LaneGo, Image: imageGo, NeedsStocks: true,
		Desc: "Every mutant gremlins makes of this pull's changed Go is killed by the tests.",
		Script: provisionGuard + worktreeRepo + `guard bash --version
[ -f /stocks/ci/lib/mutation/go.sh ] || { echo "go:mutation: CANNOT RUN - /stocks/ci/lib/mutation/go.sh is absent; foundry-stocks did not mount at its one home." >&2; exit 2; }
export MUT_DIR=/tmp/mutation MUT_MODE=diff MUT_BASE="${GATE_BASE:-}" MUT_EXCLUDE='^vendor/|(^|/)dagger\.gen\.go$|\.pb\.go$|(^|/)zz_generated'
if [ -f ci/mutation.env ]; then set -a; . ./ci/mutation.env; set +a; echo "go:mutation: knobs from ci/mutation.env"; fi
for phase in resolve setup cover mutate teardown score; do
  bash /stocks/ci/lib/mutation/go.sh "$phase" || { echo "go:mutation: CANNOT RUN - phase $phase exited non-zero; the phases never do on their own" >&2; exit 2; }
done
v="$(cat /tmp/mutation/verdict 2>/dev/null)"
[ -n "$v" ] || { echo "go:mutation: CANNOT RUN - the score phase wrote no verdict" >&2; exit 2; }
echo "go:mutation: $(cat /tmp/mutation/reason 2>/dev/null)"
exit "$v"`,
	},
	{
		ID: "python:mutation", Stage: StageMutation, Lane: LanePython, Image: imagePython, NeedsStocks: true,
		Desc: "Every mutant cosmic-ray makes of this pull's changes to the declared critical modules is killed by the tests.",
		Script: provisionGuard + worktreeRepo + `guard bash --version
guard uv --version
[ -f /stocks/ci/lib/mutation/python.sh ] || { echo "python:mutation: CANNOT RUN - /stocks/ci/lib/mutation/python.sh is absent; foundry-stocks did not mount at its one home." >&2; exit 2; }
MODS="$(sed -n 's/^critical_modules:[[:space:]]*//p' .copier-answers.yml 2>/dev/null | head -1 | sed -e "s/^['\"]//" -e "s/['\"]$//")"
[ -n "$(printf '%s' "$MODS" | tr -d ' ')" ] || { echo "python:mutation: ABSENT - no critical_modules declared in .copier-answers.yml; this repository opted out of the mutation gate"; exit 0; }
export MUT_DIR=/tmp/mutation MUT_MODE=diff MUT_BASE="${GATE_BASE:-}" MUT_MODULES="$MODS"
if [ -f ci/mutation.env ]; then set -a; . ./ci/mutation.env; set +a; echo "python:mutation: knobs from ci/mutation.env"; fi
for phase in resolve sync config init scope exec score; do
  bash /stocks/ci/lib/mutation/python.sh "$phase" || { echo "python:mutation: CANNOT RUN - phase $phase exited non-zero; the phases never do on their own" >&2; exit 2; }
done
v="$(cat /tmp/mutation/verdict 2>/dev/null)"
[ -n "$v" ] || { echo "python:mutation: CANNOT RUN - the score phase wrote no verdict" >&2; exit 2; }
echo "python:mutation: $(cat /tmp/mutation/reason 2>/dev/null)"
exit "$v"`,
	},
	{
		ID: "rust:mutation", Stage: StageMutation, Lane: LaneRust, Image: imageRust, NeedsStocks: true,
		Desc: "Every viable mutant cargo-mutants makes of this pull's changes to the declared critical modules is killed by the tests.",
		Script: provisionGuard + worktreeRepo + `guard bash --version
guard cargo mutants --version
[ -f /stocks/ci/lib/mutation/rust.sh ] || { echo "rust:mutation: CANNOT RUN - /stocks/ci/lib/mutation/rust.sh is absent; foundry-stocks did not mount at its one home." >&2; exit 2; }
MODS="$(sed -n 's/^critical_modules:[[:space:]]*//p' .copier-answers.yml 2>/dev/null | head -1 | sed -e "s/^['\"]//" -e "s/['\"]$//")"
[ -n "$(printf '%s' "$MODS" | tr -d ' ')" ] || { echo "rust:mutation: ABSENT - no critical_modules declared in .copier-answers.yml; this repository opted out of the mutation gate"; exit 0; }
export MUT_DIR=/tmp/mutation MUT_MODE=diff MUT_BASE="${GATE_BASE:-}" MUT_MODULES="$MODS"
if [ -f ci/mutation.env ]; then set -a; . ./ci/mutation.env; set +a; echo "rust:mutation: knobs from ci/mutation.env"; fi
for phase in resolve mutate score; do
  bash /stocks/ci/lib/mutation/rust.sh "$phase" || { echo "rust:mutation: CANNOT RUN - phase $phase exited non-zero; the phases never do on their own" >&2; exit 2; }
done
v="$(cat /tmp/mutation/verdict 2>/dev/null)"
[ -n "$v" ] || { echo "rust:mutation: CANNOT RUN - the score phase wrote no verdict" >&2; exit 2; }
echo "rust:mutation: $(cat /tmp/mutation/reason 2>/dev/null)"
exit "$v"`,
	},
	{
		ID: "ts:mutation", Stage: StageMutation, Lane: LaneTS, Image: imageTS, NeedsStocks: true,
		Desc: "Every mutant StrykerJS makes of this pull's changes to the declared critical modules is killed by the tests.",
		Script: provisionGuard + worktreeRepo + `guard bash --version
guard bun --version
[ -f /stocks/ci/lib/mutation/ts.sh ] || { echo "ts:mutation: CANNOT RUN - /stocks/ci/lib/mutation/ts.sh is absent; foundry-stocks did not mount at its one home." >&2; exit 2; }
MODS="$(sed -n 's/^critical_modules:[[:space:]]*//p' .copier-answers.yml 2>/dev/null | head -1 | sed -e "s/^['\"]//" -e "s/['\"]$//")"
[ -n "$(printf '%s' "$MODS" | tr -d ' ')" ] || { echo "ts:mutation: ABSENT - no critical_modules declared in .copier-answers.yml; this repository opted out of the mutation gate"; exit 0; }
export MUT_DIR=/tmp/mutation MUT_MODE=diff MUT_BASE="${GATE_BASE:-}" MUT_MODULES="$MODS"
if [ -f ci/mutation.env ]; then set -a; . ./ci/mutation.env; set +a; echo "ts:mutation: knobs from ci/mutation.env"; fi
for phase in resolve install build mutate score; do
  bash /stocks/ci/lib/mutation/ts.sh "$phase" || { echo "ts:mutation: CANNOT RUN - phase $phase exited non-zero; the phases never do on their own" >&2; exit 2; }
done
v="$(cat /tmp/mutation/verdict 2>/dev/null)"
[ -n "$v" ] || { echo "ts:mutation: CANNOT RUN - the score phase wrote no verdict" >&2; exit 2; }
echo "ts:mutation: $(cat /tmp/mutation/reason 2>/dev/null)"
exit "$v"`,
	},
	{
		ID: "sweep:digest-pins", Stage: StageSweep, Lane: LaneAny, Image: imageFleet,
		Desc: "Every image digest this repo's workflows pin still resolves in the registry.",
		// The canonical script, READ AT ITS ONE HOME. It already carries the
		// three states this module requires — 0 every pin resolves, 1 a pin is
		// BROKEN, 2 no pins found at all ("the scan is broken, not the tree
		// clean") — which is why this atom wraps it instead of reimplementing
		// it. Twice in five days a collected digest took out the same five
		// stars, and both times a human found it by noticing a red landing.
		//
		// THE SURFACE PROBE IS THE ATOM'S OWN, and it is what the script
		// cannot do for itself. The script was written for foundry-stocks,
		// where cast.yml carries several pins, so it is right to call zero
		// pins a broken scan THERE. Dispatched over all 86 repos in custody it
		// is wrong on most of them: a star calls the reusable workflow and the
		// pin lives in the callee's tree. Measured on
		// ca-sweep-manual-1788973171 (2026-09-09), that turned 57 of 86 repos
		// into cannot-run and buried the run's one real finding. So the atom
		// asks first whether there is a pin surface at all
		// (checks.PinSurfacePattern): no surface is ABSENT, and a surface the
		// extractor could not read stays a CANNOT RUN — now saying which of
		// the two it is, because a check that could not run must also say why.
		Script: provisionGuard + `test -d .forgejo/workflows || { echo "sweep:digest-pins: ABSENT - no .forgejo/workflows in this tree, so nothing here pins a digest."; exit 0; }
grep -rqE '` + PinSurfacePattern + `' .forgejo/workflows || { echo "sweep:digest-pins: ABSENT - .forgejo/workflows carries no digest reference at all, so this repo's pin population is EMPTY rather than unscanned. It calls the reusable workflows and the image pin lives in the callee's tree."; exit 0; }
test -f /stocks/ci/lib/digest-pins.sh || { echo "sweep:digest-pins: CANNOT RUN - the canonical script is not reachable through the door." >&2; exit 2; }
if ! command -v oras >/dev/null 2>&1; then
  python3 - "$ORAS_MIRROR" "$ORAS_URL" <<'PY' || { echo "sweep:digest-pins: CANNOT RUN - could not fetch oras from the mirror or from upstream. Resolving zero pins and calling them all healthy is the outage this check exists to catch, running backwards." >&2; exit 2; }
import sys, urllib.request
for url in sys.argv[1:]:
    try:
        urllib.request.urlretrieve(url, "/tmp/oras.tgz")
        sys.exit(0)
    except Exception as exc:
        print("could not fetch %s: %s" % (url, exc), file=sys.stderr)
sys.exit(1)
PY
  tar -xzf /tmp/oras.tgz -C /usr/local/bin oras || { echo "sweep:digest-pins: CANNOT RUN - the oras archive did not unpack." >&2; exit 2; }
fi
guard oras version
PINS_DIR=.forgejo/workflows bash /stocks/ci/lib/digest-pins.sh
code=$?
if [ "$code" -eq 2 ]; then
  echo "sweep:digest-pins: CANNOT RUN - this tree DOES carry a digest reference (it matches ` + PinSurfacePattern + `) and the canonical extractor still returned none, so the SCAN is broken rather than the tree unpinned. Compare digest_pins() in foundry-stocks/ci/lib/digest-pins.sh against the pin forms under .forgejo/workflows." >&2
fi
exit $code`,
		NeedsStocks: true,
	},
	{
		ID: "sweep:portfolio-sbom", Stage: StageSweep, Lane: LaneAny, Image: imageFleet,
		Desc: "A repository that builds an image builds it through the workflow that attests its SBOM.",
		// THIS DOES NOT RE-RUN THE PORTFOLIO SCAN, and that is deliberate. The
		// scan is fleet-wide, already scheduled, and stays exactly where it
		// is: CronJob portfolio-weekly (infra, ci-foundry, Mondays 07:00 UTC)
		// drives ci-portfolio-pipeline, which re-scores the SBOM attestations
		// the registry already holds. A per-repo copy would be a second
		// surface free to disagree with the first — the drift this module
		// exists to delete.
		//
		// What it closes is the hole that scan structurally cannot see. The
		// re-score reads ATTESTATIONS; a repo whose image is never attested
		// contributes nothing to read, so it scores clean by being invisible,
		// forever, and no pull will ever say so. That is CA F9's own value
		// statement — "repos nobody has opened a PR against stop being
		// invisible" — asked at the one place where the answer is a fact about
		// the tree rather than a fact about the database.
		Script: `test -f Dockerfile || { echo "sweep:portfolio-sbom: ABSENT - no Dockerfile at the repository root. The portfolio re-scores image SBOMs, and this repo builds no image."; exit 0; }
test -d .forgejo/workflows || { echo "sweep:portfolio-sbom: CANNOT RUN - a Dockerfile and no workflow tree. Nothing here says whether the image is ever built, so nothing here can say whether it is attested." >&2; exit 2; }
if grep -q -r -E "uses:[[:space:]]*foundry/foundry-stocks/\.forgejo/workflows/(build|frontend-build|bake-blade)\.yml@" .forgejo/workflows; then
  echo "sweep:portfolio-sbom: the image is built through the attesting workflow, so the weekly re-score can see this repo."; exit 0
fi
echo "sweep:portfolio-sbom: this repo has a Dockerfile but no workflow calling foundry-stocks build.yml (or frontend-build.yml / bake-blade.yml)." >&2
echo "The weekly portfolio re-score reads cosign SBOM attestations out of the registry. An image nobody attests contributes no SBOM, so it is not scored badly - it is not scored at all, and the digest reports clean because it never looked." >&2
exit 1`,
	},
	{
		ID: "sweep:template-render-matrix", Stage: StageSweep, Lane: LaneAny, Image: imageFleet,
		Desc: "Every case in this template's ci-matrix.toml still renders.",
		// A template bug does not break the template. It propagates into every
		// repo stamped afterward and surfaces later, in someone else's repo,
		// where the cause is expensive to trace — which is exactly why this is
		// a cadence check and not a pull check: the stamped population keeps
		// growing while the template tree sits still.
		Script: provisionGuard + `test -f ci-matrix.toml || { echo "sweep:template-render-matrix: ABSENT - no ci-matrix.toml at the repository root, so this repo declares no render matrix."; exit 0; }
test -f /stocks/ci/lib/template_render_matrix.py || { echo "sweep:template-render-matrix: CANNOT RUN - the canonical gate is not reachable through the door." >&2; exit 2; }
test -d .git || { echo "sweep:template-render-matrix: CANNOT RUN - no .git in the tree under check. The matrix renders the template AT ITS GIT HEAD (--vcs-ref=HEAD is load-bearing, foundry#130); without a repository copier resolves some other tree, and a green from that would be a green about something else." >&2; exit 2; }
guard uvx --version
python3 /stocks/ci/lib/template_render_matrix.py --template .`,
		NeedsStocks: true,
	},
	{
		ID: "sweep:kubeconform", Stage: StageSweep, Lane: LaneAny, Image: imageKubeconform,
		Desc: "Every manifest under flux/ validates against its Kubernetes schema.",
		// The zero-scan refusal in this atom's dialect. kubeconform reports
		// `skipped` both for a CRD genuinely absent from the catalogue and for
		// a catalogue it could not reach, and the second of those is a CANNOT
		// RUN — so the catalogue is PROBED before the scan, and a scan that
		// validated nothing at all is a 2 rather than a green.
		Script: `test -d flux || { echo "sweep:kubeconform: ABSENT - no flux/ tree at the repository root."; exit 0; }
wget -q -O /dev/null "$CRD_SCHEMA_PROBE" || { echo "sweep:kubeconform: CANNOT RUN - the CRD schema catalogue is unreachable. Every custom resource would then report as skipped, which is indistinguishable from a clean validation and is not one." >&2; exit 2; }
/kubeconform -ignore-missing-schemas -ignore-filename-pattern "\.json$" -schema-location default -schema-location "$CRD_SCHEMA_LOCATION" -summary -n 8 flux/ > /tmp/kc.out 2>/tmp/kc.err
rc=$?
sum=$(cat /tmp/kc.out /tmp/kc.err | grep -m1 "^Summary:")
valid=$(printf "%s" "$sum" | sed -n "s/.*Valid: \([0-9]*\).*/\1/p")
if [ -z "$valid" ]; then
  cat /tmp/kc.err /tmp/kc.out >&2
  echo "sweep:kubeconform: CANNOT RUN - kubeconform printed no summary, so there is no count to read and nothing was measured." >&2
  exit 2
fi
if [ "$valid" -eq 0 ]; then
  printf "%s\n" "$sum" >&2
  echo "sweep:kubeconform: REFUSING a zero-resource validation. Nothing under flux/ was checked against a schema, so 0 invalid means NOTHING WAS EXAMINED - not that the tree is correct." >&2
  exit 2
fi
printf "%s\n" "$sum"
if [ "$rc" -ne 0 ]; then grep -v "^Summary:" /tmp/kc.out | head -80; exit 1; fi
echo "sweep:kubeconform: clean"`,
	},
	{
		ID: "sweep:kube-linter", Stage: StageSweep, Lane: LaneAny, Image: imageKubeLinter,
		Desc: "Every workload under flux/ passes kube-linter's default checks.",
		// --fail-if-no-objects-found is kube-linter's own zero-population
		// refusal, and it exits 1 for it — the same code it uses for findings.
		// Reading that as findings would be wrong in the direction that still
		// looks like the check worked, so the message is matched and remapped
		// to 2.
		Script: `test -d flux || { echo "sweep:kube-linter: ABSENT - no flux/ tree at the repository root."; exit 0; }
out=$(/kube-linter lint --fail-if-no-objects-found flux/ 2>&1); rc=$?
if printf "%s" "$out" | grep -q "no valid objects found"; then
  echo "sweep:kube-linter: CANNOT RUN - kube-linter parsed no object under flux/. That is the same exit code as a finding, and it is not one." >&2
  exit 2
fi
if [ "$rc" -eq 0 ]; then echo "sweep:kube-linter: clean"; exit 0; fi
printf "%s\n" "$out" | tail -3 >&2
printf "%s\n" "$out" | head -120
exit 1`,
	},
}

// forgeTestkit builds one of the three forge-testkit lint bodies. The three
// differ only by mode, and three copies of the provisioning guard is exactly
// the duplication this repository exists to delete.
// forgeTestkit ports one forge-testkit-lint pre-commit hook. The hook takes
// PATHS — pre-commit appends the files its `files:` pattern matched, and the
// CLI refuses to run without them ("the following arguments are required:
// paths"). MEASURED 2026-09-10T01:25Z gate-helios-057545b: fake-placement and
// schema-budget red on that usage error in every repo the gate touched, go
// and python alike. The pattern is the template hook's `files:` (python-repo-
// template .pre-commit-config.yaml), read over the gate's own population so
// the repo's exclude applies; no files, or no forge-testkit DEPENDENCY ENTRY
// in pyproject.toml, is ABSENT — pre-commit skips a hook with an empty file
// list, and a repo that never took the dependency has nothing for it to
// read. The entry is a quoted "forge-testkit…" line: a comment naming the
// package, or go.mod's forge-testkit-go, is not one (measured on helios by
// Lovelace13, 2026-09-10 — the first cut grepped the bare word).
func forgeTestkit(mode, pattern string) string {
	return provisionGuard + worktreeRepo + gatePopulation + `guard uv --version
if ! grep -Eqs '^[[:space:]]*"forge-testkit([<>=!~ \["]|$)' pyproject.toml; then echo "python:forge-testkit-` + mode + `: ABSENT - forge-testkit is not a dependency of this project"; exit 0; fi
files=$(hookpopulation forge-testkit-` + mode + ` -- '` + pattern + `')
if [ -z "$files" ]; then echo "python:forge-testkit-` + mode + `: ABSENT - no files match ` + pattern + `"; exit 0; fi
printf '%s\n' "$files" | xargs -r uv run --extra dev forge-testkit-lint ` + mode + ` || exit 1
echo "forge-testkit ` + mode + `: clean"`
}

// AtomByID answers the definition for an id, and panics if there is none —
// every `// +check` function names a row in the table above, and a name that
// does not resolve is an authoring error caught by this repo's own tests, not a
// runtime condition.
func AtomByID(id string) AtomDef {
	for _, a := range Atoms {
		if a.ID == id {
			return a
		}
	}
	panic("no atom defined for id " + id)
}

// PullPathStages are the stages a pull's gate may run. Sweep is deliberately
// not one of them, and neither is mutation: it blocks the same pull, but it
// runs as its own lane, asked for by name (see StageMutation).
var PullPathStages = []string{StagePrecommit, StagePrepush}

// MutationAtoms answers the mutation stage — the set the door's `mutation`
// lane runs beside the gate.
func MutationAtoms() []AtomDef {
	return AtomsForStage(StageMutation)
}

// IsPullPath reports whether a stage belongs on a pull's path.
//
// The empty stage means "every stage a pull runs", which is every stage EXCEPT
// sweep — not "everything in the table". That distinction is the whole of CA
// F9's acceptance ("no `stage: sweep` atom appears in any pull's path"), and
// making it the default here is what turns the acceptance from a convention
// somebody has to remember into a fact about the code: a gate that asks for the
// vector and names no stage cannot be handed a sweep atom.
func IsPullPath(stage string) bool {
	for _, s := range PullPathStages {
		if s == stage {
			return true
		}
	}
	return false
}

// PullPathAtoms answers every atom a pull may run — the set the door reads.
func PullPathAtoms() []AtomDef {
	return AtomsForStage("")
}

// SweepAtoms answers every repo-cadence atom — the set ca-sweep runs.
func SweepAtoms() []AtomDef {
	return AtomsForStage(StageSweep)
}

// AtomsForStage selects the atoms for one stage; the empty stage selects every
// PULL-PATH atom, never the sweep. See IsPullPath for why that is the default.
func AtomsForStage(stage string) []AtomDef {
	out := make([]AtomDef, 0, len(Atoms))
	for _, a := range Atoms {
		if stage == "" {
			if !IsPullPath(a.Stage) {
				continue
			}
		} else if a.Stage != stage {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Select narrows a stage's atoms to a comma-separated list of ids.
//
// An id that names nothing, or names an atom the stage does not admit, is an
// ERROR rather than an omission. A selector is written by hand into a CronJob's
// env, where a typo is invisible: `sweep:digest_pins` for `sweep:digest-pins`
// would otherwise produce an empty vector, the sweep would report nothing, and
// nothing reported reads exactly like nothing wrong. That is the same shape as
// a ruleset matching zero files, and it gets the same answer — refuse.
func Select(from []AtomDef, ids string) ([]AtomDef, error) {
	have := map[string]AtomDef{}
	for _, a := range from {
		have[a.ID] = a
	}
	out := make([]AtomDef, 0, len(from))
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		a, ok := have[id]
		if !ok {
			if AtomExists(id) {
				return nil, fmt.Errorf("atom %q exists but this stage does not admit it — a selector that silently drops an atom reports nothing, and nothing reported reads exactly like nothing wrong", id)
			}
			return nil, fmt.Errorf("no atom %q — check the id against `dagger check -l`", id)
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the selector %q chose no atom; refusing to answer an empty vector", ids)
	}
	return out, nil
}

// AtomExists reports whether the table carries this id, without panicking.
func AtomExists(id string) bool {
	for _, a := range Atoms {
		if a.ID == id {
			return true
		}
	}
	return false
}
