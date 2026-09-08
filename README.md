# foundry-tools — the fleet's checks, as code

**The charter.** Every check this fleet runs is one **zero-argument function** in this module, with **three states** and no fourth: `0` it ran and found nothing · `1` it ran and has something to say · `2` **it did not run**. A two-outcome check is a lie waiting to happen, and this repository exists because that lie was measured: `go-repo-template` and `rust-repo-template` shipped a python-only SAST ruleset, so every Go and Rust star's security gate scanned **zero files** and reported success, every time, for as long as it took someone to look (`foundry-stocks#4415`). The narrower case is worse — a ruleset that matches *something* and misses the rest still exits 0: `chaos` scanned 28 of 2636 tracked files and `themis` 26 of 2221, both green (`foundry-stocks#4949`).

So: **a check that could not run is never a pass.** That discipline already existed in two hooks — `staticcheck` and `govulncheck` degraded to exit 2 rather than skip — and here it is the type rather than the exception.

## The contract

| | |
| :-- | :-- |
| **Zero-argument** | Every `// +check` function takes a `context.Context` and nothing else. There is no directory parameter anywhere below the constructor, so no atom can be pointed at a tree other than the one being gated. |
| **One repository** | The tree under check is bound once, in `New`, from the caller's own context directory (`+defaultPath="/"`). |
| **Three states** | `0` pass · `1` findings · `2` could not run. Every exit that is not 0 or 1 — a 127 missing binary, a 137 OOM kill, a 143 cancellation — maps to **2**, because reading any of those as findings is wrong and reading them as a pass is the failure this module deletes. |
| **Absent says so** | A lane with no surface in the tree — no `go.mod`, no `pyproject.toml` — reports `ABSENT`, exits 0, and **prints why**. Silence would be indistinguishable from a clean scan. |
| **Namespaced** | `fleet:` · `go:` · `python:` · `rust:` · `ts:`. `dagger check -l` lists all of them with descriptions. |
| **One definition** | The atom table in `internal/checks/atoms.go` drives both the check functions and the verdict vector, so `dagger check -l` and the catalogue cannot drift apart. The census counted that drift 31 times across two independently-maintained surfaces; there is one surface here. |

## Using it

```sh
# List every atom this module carries, from a pinned ref through the door.
dagger check -l -m git.notusmi.com/rob/foundry-tools@<sha>

# Run them against the repository you are standing in.
dagger check                     # all of them
dagger check go: python:         # one namespace at a time
dagger check go:staticcheck      # one atom

# The verdict VECTOR — one element per atom, each preserving its own 0/1/2,
# rather than a single exit code that flattens a 2 into "the run failed".
dagger call verdicts --stage=prepush
dagger call catalogue            # the atom table as catalogue rows
dagger call lanes                # which lanes this repository actually builds
```

A gate resolves this module **at a pinned git ref through the door** — `git.notusmi.com` serves `go-import` without SSO; `forgejo.notusmi.com` sits behind the portal and a machine cannot log in. Hephaestus renders that pin into each repo's `dagger.json` (CA F4), which is also what binds `+defaultPath="/"` to the repo being gated rather than to this one.

## The atoms

**`fleet:`** — every repository, whatever it is written in.
`check-yaml` · `check-added-large-files` · `check-merge-conflict` · `detect-secrets` · `stop-justifications` (pre-commit) · `sast-ruleset-lanes` · `opengrep-sast` (pre-push)

**`go:`** (`go.mod`) — `gofmt` · `vet` · `build` (pre-commit) · `test-race` · `staticcheck` · `govulncheck` (pre-push)

**`python:`** (`pyproject.toml`) — `ruff-check` · `ruff-format` · `forge-testkit-assertion-free` · `forge-testkit-fake-placement` · `forge-testkit-schema-budget` (pre-commit) · `mypy` · `pytest` · `pip-audit` (pre-push)

**`rust:`** (`Cargo.toml`) — `cargo-fmt` · `cargo-clippy` (pre-commit) · `cargo-test` · `cargo-audit` (pre-push)

**`ts:`** (`package.json`) — `bun-gate-commit` (pre-commit) · `bun-gate` · `bun-audit` (pre-push)

### What is deliberately NOT here

- **`pyright`** — two type checkers ran on every python pre-push with no incident behind the duplication. One type checker, `mypy --strict`. Ratified.
- **`trailing-whitespace` / `end-of-file-fixer`** — removed fleet-wide on measurement (0.7% and 0.75% yield) and, in the second case, an outage: a 24-repo red-CI cascade over an unterminated `#copier updated` sentinel. They are not atoms; they are deletions.
- **`worktree-guard`** — it asserts the *commit* was made from a linked worktree rather than the shared checkout. Its own script stands down under `$CI`, and an engine IS a CI boundary, so an atom here would be a permanent no-op. It stays at the commit boundary, where the fact it tests exists.
- **`lint-staged`** — defined over the git **index**. The engine receives a directory, not an index; an atom claiming to be lint-staged would be checking a different population than the hook it replaced, which is the silent-drift failure this module exists to end.
- **`fetch-origin`** — a background convenience with no verdict. It was never a check.

Sweep-cadence checks (mutation, digest-pins, portfolio SBOM) are not here either — they belong to the sweep, not to a pull's path.

## Working on it

```sh
just develop      # regenerate the SDK bindings, then re-drop the otel replaces
just gate         # gofmt · vet · build · test -race · staticcheck · govulncheck
```

**`dagger develop` re-adds four `replace` directives** pinning `go.opentelemetry.io/otel/exporters/otlp/otlplog/*`, `otel/log` and `otel/sdk/log` back to `v0.16.0`. That version carries **GO-2026-4985**, so a bare `dagger develop` turns this repo's own `govulncheck` red. Dropping the four replaces and re-tidying builds, tests and loads identically — verified — and `just develop` does exactly that. This is the module's own rule applied to itself: the finding is fixed, not silenced.

The generated bindings (`dagger.gen.go`, `internal/dagger/`) are **committed**, against the SDK's default `.gitignore`. CI has no Dagger engine until the in-cluster engine lands (CA F5), so the repository has to build and test with the Go toolchain alone.

## Layout

```
main.go             the module root: the source binding, the lane namespaces, the vector
checks_*.go         the // +check functions — one per atom, each a call into the table
internal/checks/    the pure core, engine-free and unit-tested
  atoms.go          THE TABLE: id, stage, lane, image, description, body
  lane.go           lane detection from root manifests
  verdict.go        the three states and the exit-code mapping
  images.go         the lane images, in one place, for the digest-pins sweep to land on
```

`internal/checks` imports nothing from the Dagger SDK, which is why `go test ./...` runs without an engine.
