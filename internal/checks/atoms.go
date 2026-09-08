package checks

// Stage names where in a pull's life an atom fires. The catalogue carries the
// same values (nereus.antibody_store.stage), so a row and its code agree by
// construction.
const (
	StagePrecommit = "precommit"
	StagePrepush   = "prepush"
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

var Atoms = []AtomDef{
	// ---- fleet: every repository, whatever it is written in ----
	{
		ID: "fleet:check-yaml", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "Every YAML file in the tree parses.",
		Script: provisionGuard + `guard uvx --from pre-commit-hooks check-yaml --help
files=$(find . -path ./.git -prune -o -path '*/node_modules' -prune -o -path '*/vendor' -prune -o -type f \( -name '*.yml' -o -name '*.yaml' \) -print)
if [ -z "$files" ]; then echo "fleet:check-yaml: no YAML in this tree"; exit 0; fi
uvx --from pre-commit-hooks check-yaml $files || exit 1
echo "fleet:check-yaml: parsed all YAML"`,
	},
	{
		ID: "fleet:check-added-large-files", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No file in the tree exceeds 500 KB.",
		// Ported to a tree walk rather than pre-commit's git-index form: the
		// engine receives a DIRECTORY, not an index, so a body that shelled out
		// to `git diff --cached` would report CANNOT RUN forever. The assertion
		// is unchanged; the population it reads is the tree.
		Script: `big=$(find . -path ./.git -prune -o -path '*/node_modules' -prune -o -type f -size +500k -print)
if [ -n "$big" ]; then echo "files over 500 KB:"; echo "$big"; exit 1; fi
echo "fleet:check-added-large-files: nothing over 500 KB"`,
	},
	{
		ID: "fleet:check-merge-conflict", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No conflict markers were committed.",
		// Only the two ANCHORED markers, not the bare row of equals signs: that
		// is a setext heading in Markdown and a table rule in reStructuredText,
		// and matching it turns every docs repo red for a reason nobody can act
		// on.
		Script: `hits=$(grep -rIn --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=vendor -E '^(<<<<<<< |>>>>>>> )' . || true)
if [ -n "$hits" ]; then echo "$hits"; exit 1; fi
echo "fleet:check-merge-conflict: no conflict markers"`,
	},
	{
		ID: "fleet:detect-secrets", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No new secret against the repository's .secrets.baseline.",
		Script: provisionGuard + `if [ ! -f .secrets.baseline ]; then echo "fleet:detect-secrets: CANNOT RUN - no .secrets.baseline at the repository root. Refusing to report success without scanning." >&2; exit 2; fi
guard uvx --from detect-secrets detect-secrets-hook --help
files=$(find . -path ./.git -prune -o -path '*/node_modules' -prune -o -path '*/vendor' -prune -o -path ./testdata -prune -o -type f -print)
if [ -z "$files" ]; then echo "fleet:detect-secrets: CANNOT RUN - empty tree" >&2; exit 2; fi
uvx --from detect-secrets detect-secrets-hook --baseline .secrets.baseline $files || exit 1
echo "fleet:detect-secrets: clean against .secrets.baseline"`,
	},
	{
		ID: "fleet:stop-justifications", Stage: StagePrecommit, Lane: LaneAny, Image: imageFleet,
		Desc: "No silent suppression of any gate — a suppression carries a tool-conflict line.",
		// The script is READ AT ITS ONE HOME, not vendored. A missing source is
		// exit 2, never 0 — the same contract the pre-commit hook states, for
		// the same reason: pre-commit hides a passing hook's output, so a silent
		// skip is indistinguishable from a clean scan.
		Script: `if [ ! -f /stocks/ci/lib/stop_justifications.py ]; then echo "fleet:stop-justifications: CANNOT RUN - canonical source not reachable through the door" >&2; exit 2; fi
python3 /stocks/ci/lib/stop_justifications.py . || exit 1
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
		Script: `test -d rules/sast || { echo "fleet:opengrep-sast: ABSENT - no rules/sast in this tree"; exit 0; }
if ! command -v opengrep >/dev/null 2>&1; then
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
		Script: forgeTestkit("assertion-free"),
	},
	{
		ID: "python:forge-testkit-fake-placement", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc:   "Fake and Stub doubles live where they belong (forge-testkit).",
		Script: forgeTestkit("fake-placement"),
	},
	{
		ID: "python:forge-testkit-schema-budget", Stage: StagePrecommit, Lane: LanePython, Image: imagePython,
		Desc:   "MCP verb descriptions stay inside the schema budget (forge-testkit).",
		Script: forgeTestkit("schema-budget"),
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
		Script: provisionGuard + `guard uv --version
uv run --all-extras pytest -q || exit 1`,
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
}

// forgeTestkit builds one of the three forge-testkit lint bodies. The three
// differ only by mode, and three copies of the provisioning guard is exactly
// the duplication this repository exists to delete.
func forgeTestkit(mode string) string {
	return provisionGuard + `guard uv --version
uv run --extra dev forge-testkit-lint ` + mode + ` || exit 1
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
