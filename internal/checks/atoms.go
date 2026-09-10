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
	// matches nereus.antibody_store.stage, which admits exactly these three.
	StageSweep = "sweep"
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
		Desc:   "go test -race ./... passes.",
		Script: `go test -race ./... || exit 1`,
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
		Desc:   "bun run gate (format, lint, typecheck, test, build) passes.",
		Script: `bun run gate || exit 1`,
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
// the repo's exclude applies; no files, or no forge-testkit in the project,
// is ABSENT — pre-commit skips a hook with an empty file list, and a repo
// that never took the dependency has nothing for it to read.
func forgeTestkit(mode, pattern string) string {
	return provisionGuard + worktreeRepo + gatePopulation + `guard uv --version
if ! grep -qs 'forge-testkit' pyproject.toml; then echo "forge-testkit ` + mode + `: ABSENT - forge-testkit is not a dependency of this project"; exit 0; fi
files=$(hookpopulation forge-testkit-` + mode + ` -- '` + pattern + `')
if [ -z "$files" ]; then echo "forge-testkit ` + mode + `: ABSENT - no files match ` + pattern + `"; exit 0; fi
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
// not one of them.
var PullPathStages = []string{StagePrecommit, StagePrepush}

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
