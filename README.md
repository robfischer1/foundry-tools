# foundry-tools — the fleet's checks, as code

**The charter.** Every check this fleet runs is one **zero-argument function** in this module, with **three states** and no fourth: `0` it ran and found nothing · `1` it ran and has something to say · `2` **it did not run**. A two-outcome check is a lie waiting to happen, and this repository exists because that lie was measured: `go-repo-template` and `rust-repo-template` shipped a python-only SAST ruleset, so every Go and Rust star's security gate scanned **zero files** and reported success, every time, for as long as it took someone to look (`foundry-stocks#4415`). The narrower case is worse — a ruleset that matches *something* and misses the rest still exits 0: `chaos` scanned 28 of 2636 tracked files and `themis` 26 of 2221, both green (`foundry-stocks#4949`).

So: **a check that could not run is never a pass.** That discipline already existed in two hooks — `staticcheck` and `govulncheck` degraded to exit 2 rather than skip — and here it is the type rather than the exception.

## The contract

| | |
| :-- | :-- |
| **Zero-argument** | Every `// +check` function takes a `context.Context` and nothing else. There is no directory parameter anywhere below the constructor, so no atom can be pointed at a tree other than the one being gated. |
| **One repository** | The tree under check is bound once, in `New`, from the caller's own context directory (`+defaultPath="/"`). |
| **Three states** | `0` pass · `1` findings · `2` could not run. Every exit that is not 0 or 1 — a 127 missing binary, a 137 OOM kill, a 143 cancellation — maps to **2**, because reading any of those as findings is wrong and reading them as a pass is the failure this module deletes. |
| **Absent says so** | A lane with no surface in the tree — no `go.mod`, no `pyproject.toml` — reports `ABSENT`, exits 0, and **prints why**. Silence would be indistinguishable from a clean scan. An atom that finds its own absence *inside* the tree says so on stdout, and the vector renders that as `absent` rather than `pass` — "nothing to check" is not "checked and clean". |
| **Namespaced** | `fleet:` · `go:` · `python:` · `rust:` · `ts:` on a pull's path; `sweep:` on the clock. `dagger check -l` lists all of them with descriptions. |
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
# Standing in ANOTHER repo with the module resolved remotely, name the tree —
# see "A remotely-resolved module binds its own tree" below. --source is the
# constructor's parameter; nothing below New takes a directory.
dagger call -m git.notusmi.com/rob/foundry-tools@<sha> --source=. verdicts --stage=sweep

dagger call verdicts                 # every PULL stage — precommit and prepush
dagger call verdicts --stage=prepush
dagger call verdicts --stage=sweep   # the clock's vector, asked for by name
dagger call catalogue            # the atom table as catalogue rows
dagger call lanes                # which lanes this repository actually builds
```

A gate resolves this module **at a pinned git ref through the door** — `git.notusmi.com` serves `go-import` without SSO; `forgejo.notusmi.com` sits behind the portal and a machine cannot log in. Hephaestus renders that pin into each repo's `dagger.json` (CA F4).

**A remotely-resolved module binds ITS OWN tree, not yours — name the source.** Measured 2026-09-08 against the in-cluster engine: standing in `infra` and running `dagger call -m git.notusmi.com/rob/foundry-tools@<sha> lanes` answers `go (go.mod)`. `infra` has no `go.mod`; foundry-tools does. `+defaultPath="/"` resolves against the module's context, and for a module fetched from git that context is the module's git tree. `dagger check -m <remote>` behaves the same way — `sweep:kube-linter` returned OK in 0.4s against a tree that yields 371 findings in nine seconds.

That failure is silent and it is green, which makes it the exact shape this repository exists to delete: a sweep over sixty repos would have returned sixty identical passes, every one of them a true statement about the wrong repository. So a caller that is not standing inside a repo whose own `dagger.json` declares the dependency **must name the tree**:

```sh
dagger call -m git.notusmi.com/rob/foundry-tools@<sha> --source=. verdicts --stage=sweep
```

`--source` is the constructor's parameter and the ONLY place a directory may be named — the charter is that nothing *below* `New` takes one, so no atom can be pointed somewhere else once the tree is bound. `ca-sweep` invokes exactly the line above, once per repo. Once F4 has rendered the pin into a repo's own `dagger.json`, `dagger check` from inside that repo binds the repo's workspace and the flag is unnecessary.

## The atoms

**`fleet:`** — every repository, whatever it is written in.
`check-yaml` · `check-added-large-files` · `check-merge-conflict` · `detect-secrets` · `stop-justifications` (pre-commit) · `sast-ruleset-lanes` · `opengrep-sast` (pre-push)

**`go:`** (`go.mod`) — `gofmt` · `vet` · `build` (pre-commit) · `test-race` · `staticcheck` · `govulncheck` (pre-push)

**`python:`** (`pyproject.toml`) — `ruff-check` · `ruff-format` · `forge-testkit-assertion-free` · `forge-testkit-fake-placement` · `forge-testkit-schema-budget` (pre-commit) · `mypy` · `pytest` · `pip-audit` (pre-push)

**`rust:`** (`Cargo.toml`) — `cargo-fmt` · `cargo-clippy` (pre-commit) · `cargo-test` · `cargo-audit` (pre-push)

**`ts:`** (`package.json`) — `bun-gate-commit` (pre-commit) · `bun-gate` · `bun-audit` (pre-push)

**`sweep:`** — repo cadence, on a clock, **never in a pull's path**.
`digest-pins` (nightly) · `portfolio-sbom` · `template-render-matrix` · `kubeconform` · `kube-linter` (weekly)

`digest-pins` probes for a **pin surface** before it scans: a repo whose `.forgejo/workflows` carries no `@sha256:` reference at all calls the reusable workflows, so its pin population is *empty* and the answer is `ABSENT`. Only a tree that **has** a digest reference the canonical extractor could not read is a `CANNOT RUN`, and it says which. Without that split the fleet-wide sweep read 57 of 86 repos as could-not-run (`ca-sweep-manual-1788973171`, 2026-09-09) and buried the run's one real finding — the canonical script's "the scan is broken, not the tree clean" is true where it lives, in `foundry-stocks`, and false in every star that calls `gate.yml@main`.

These describe a **repository** rather than a change, so their answer cannot differ between two pulls against the same repo — and running them per pull leaves every repository nobody opened a PR against unevaluated indefinitely. `digest-pins` is the worked example: both outages it exists for were caused by an image being *rebuilt*, with no merge anywhere near the five stars that went red. The digest rots while the tree sits still, so the probe has to be a clock, not a diff.

| atom | what it asks | ABSENT when |
| :-- | :-- | :-- |
| `sweep:digest-pins` | every image digest this repo's workflows pin still resolves in the registry | no `.forgejo/workflows`, **or no digest reference under it** |
| `sweep:portfolio-sbom` | a repo that builds an image builds it through the workflow that **attests** its SBOM | no `Dockerfile` |
| `sweep:template-render-matrix` | every case in this template's `ci-matrix.toml` still renders | no `ci-matrix.toml` |
| `sweep:kubeconform` | every manifest under `flux/` validates against its Kubernetes schema | no `flux/` |
| `sweep:kube-linter` | every workload under `flux/` passes kube-linter's default checks | no `flux/` |

**The absence is the acceptance.** CA F9's success criterion is that no `stage: sweep` atom ever appears in a pull's path, and that is structural here rather than conventional: `dagger call verdicts` with **no stage** answers the *pull-path* vector — precommit and prepush — so a door that asks for "the vector" cannot be handed a sweep atom by omission. You get the sweep by naming it (`--stage=sweep`, or `dagger check sweep:`) and no other way. `TestNoSweepAtomOnThePullPath` asserts it.

`sweep:portfolio-sbom` **does not re-run the portfolio scan.** That scan is fleet-wide and already scheduled — CronJob `portfolio-weekly` (Mondays 07:00 UTC) drives `ci-portfolio-pipeline`, which re-scores the SBOM attestations the registry holds — and it stays exactly where it is. What the atom closes is the hole that scan structurally cannot see: the re-score reads *attestations*, so a repo whose image is never attested contributes nothing to read and scores clean by being invisible, forever.

The one caller that runs any of this is the `ca-sweep` CronJob (`infra/flux/apps/ca-sweep.yaml`).

### What is deliberately NOT here

- **`pyright`** — two type checkers ran on every python pre-push with no incident behind the duplication. One type checker, `mypy --strict`. Ratified.
- **`trailing-whitespace` / `end-of-file-fixer`** — removed fleet-wide on measurement (0.7% and 0.75% yield) and, in the second case, an outage: a 24-repo red-CI cascade over an unterminated `#copier updated` sentinel. They are not atoms; they are deletions.
- **`worktree-guard`** — it asserts the *commit* was made from a linked worktree rather than the shared checkout. Its own script stands down under `$CI`, and an engine IS a CI boundary, so an atom here would be a permanent no-op. It stays at the commit boundary, where the fact it tests exists.
- **`lint-staged`** — defined over the git **index**. The engine receives a directory, not an index; an atom claiming to be lint-staged would be checking a different population than the hook it replaced, which is the silent-drift failure this module exists to end.
- **`fetch-origin`** — a background convenience with no verdict. It was never a check.

Sweep-cadence checks live in the `sweep:` namespace above, not on a pull's path. The one that is deliberately **absent entirely** is the **mutation nightly full-run**: the machinery exists in `foundry-stocks` and stays unwired and unscheduled (decided, Rob). Mutation gates PR-time on the diff; there are no nightlies until further notice.

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
                    (checks_sweep.go is the clock's namespace)
internal/checks/    the pure core, engine-free and unit-tested
  atoms.go          THE TABLE: id, stage, lane, image, description, body
  lane.go           lane detection from root manifests
  verdict.go        the three states and the exit-code mapping
  images.go         the lane AND sweep images, in one place, for digest-pins to land on
```

`internal/checks` imports nothing from the Dagger SDK, which is why `go test ./...` runs without an engine.
