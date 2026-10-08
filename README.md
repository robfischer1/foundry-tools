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

dagger call check                    # the commit stage: basic + language fanouts, one settled answer
dagger call check exit               # …exiting 0, 1 or 2 with the stage's log
dagger call push --base=<sha>        # the push stage: the complex checks in sequence, beside mutation
dagger call release                  # the binaries the image will carry, compiled once by the push
dagger call push --base=<sha> exit   # …exiting on the worst of the two
dagger call verdicts                 # every PULL stage — precommit and prepush
dagger call verdicts --stage=prepush
dagger call verdicts --stage=sweep   # the clock's vector, asked for by name
dagger call catalogue            # the atom table as catalogue rows
dagger call lanes                # which lanes this repository actually builds
```

A gate resolves this module **at a pinned git ref through the door** — `git.notusmi.com` serves `go-import` without SSO; `git.notusmi.com` sits behind the portal and a machine cannot log in.

**A repository declares this module as its toolchain** (2026-09-14). The four repo templates render a `dagger.json` — name, `engineVersion`, one unpinned entry under `toolchains` naming `git.notusmi.com/rob/foundry-tools` — and infra carries the same file by hand. Standing in such a repo, `dagger check` and `dagger call foundry-tools …` bind **that repo's** tree, which is the documented Dagger workflow and the end of the `--source=.` trap below. Unpinned on purpose: the door resolves this module at `main` for every dispatch (ourea #109), and an unpinned toolchain resolves the same way (measured 2026-09-13 against the cluster engine). A consumer's `dagger.json` declares no `sdk`; that key is how hephaestus's tree probe tells a consumer from a module.

```sh
dagger check                          # every atom, against the repo you are standing in
dagger check foundry-tools:go:        # one namespace
dagger call foundry-tools lanes       # a function, same binding
```

**The door's runner names the commit instead of a tree.** `New` also takes `--repo` (the door's clone URL) and `--sha`: the engine fetches the commit itself, with full history (`fleet:witness` and the mutation lane diff against the pull's base, which the engine also fetches by sha), caches it by commit, and `dagger call … tree` answers `HEAD^{tree}` so the runner still proves it is grading the tree the door named before it asks for a vector. `ca-gate` (infra `flux/apps/ca-gate.yaml`) is the one caller; a session never needs it.

```sh
dagger call -m git.notusmi.com/rob/foundry-tools@<sha> \
  --repo=http://ourea:8215/<repo>.git --sha=<commit> \
  verdicts --base=<merge-base>
```

**A remotely-resolved module binds ITS OWN tree, not yours — name the source.** Measured 2026-09-08 against the in-cluster engine: standing in `infra` and running `dagger call -m git.notusmi.com/rob/foundry-tools@<sha> lanes` answers `go (go.mod)`. `infra` has no `go.mod`; foundry-tools does. `+defaultPath="/"` resolves against the module's context, and for a module fetched from git that context is the module's git tree. `dagger check -m <remote>` behaves the same way — `sweep:kube-linter` returned OK in 0.4s against a tree that yields 371 findings in nine seconds.

That failure is silent and it is green, which makes it the exact shape this repository exists to delete: a sweep over sixty repos would have returned sixty identical passes, every one of them a true statement about the wrong repository. So a caller that is not standing inside a repo whose own `dagger.json` declares the dependency **must name the tree**:

```sh
dagger call -m git.notusmi.com/rob/foundry-tools@<sha> --source=. verdicts --stage=sweep
```

`--source` is the constructor's parameter and the ONLY place a directory may be named — the charter is that nothing *below* `New` takes one, so no atom can be pointed somewhere else once the tree is bound. `ca-sweep` invokes exactly the line above, once per repo. A repo that carries the toolchain declaration above binds its own workspace from `dagger check` and needs neither flag.

## The atoms

**An unrelated edit re-runs nothing (CA F12).** The atoms that compile, vet, lint or test mount the tree less `checks.InertPaths` — the root prose by name (README, CHANGELOG, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, the governance furnace pours), licences, `docs/`, `specs/`, the CI directories, the hooks, the justfiles, pre-commit's and copier's files — so their execs are keyed on the code. A commit that touches only those hits the engine's cache for every language atom; the basic fanout (secrets, large files) still reads the whole tree, because a secret in a README is exactly what it is for. The set is an exclude measured against what the fleet's tests actually open (a Dockerfile, a `.melt`, `migrations/`, `plugins/`, `CLAUDE-INIT.md`), never an include derived from `go list`; every pattern is root-anchored so nothing reaches a package's fixtures, and **no prose is excluded by glob** — the first cut shipped `*.md` and hephaestus went red on the one `.md` its module-map test reads (`open /src/CLAUDE-INIT.md: no such file`). A glob excludes what nobody enumerated; a name goes on the list only after the grep finds nothing opening it. The mutation lane and the witness keep the whole tree — they run git against it.

**The hooks are two calls (CA F15).** `just hooks` points this clone's `core.hooksPath` at the tracked `hooks/` directory: the commit hook is `just check` (`dagger call check exit`), the push hook is `just gate` (`dagger call push --base=<merge-base> exit`). The hooks are one line each and never change; the two recipes in the justfile are what a commit and a push must pass, and a session runs the same recipes by hand. They are the same stages the door runs, so a commit that passes here passes there. The engine is a dependency on purpose — it is where the git origin already lives — and an engine that cannot be reached refuses the commit rather than waving it through. `just unhooks` puts a clone back on the pre-commit framework while the fleet is mid-flip. In foundry-tools itself the recipes call `-m .`, because a module whose job is gating must gate its own changes with the code being changed, not with main; the fleet's copy (foundry-stocks `speckit/stages.just`, imported by each star's justfile) calls the module at main.

**Stages (CA F12, 2026-09-17).** `precommit` is the **commit** stage — `dagger call check` — and holds the basic checks every tree gets (the `fleet:` namespace) beside the language checks for what the tree contains: format, lint and tests. `prepush` is the **push** stage — `dagger call push` — and holds the complex checks — deep lint, vulnerability audits, the build, the race + live-database suite, the bundle probes, the witness and orbit drift — run **in sequence, cheapest first, stopping at the first one that finds something**, beside the mutation lane, which runs concurrently and is allowed to finish. A stopped sequence names the atoms it never reached. `internal/checks/stage_test.go` pins both lists.

**The Go suite is split by cadence (CA F13).** `go:test` is the commit's unit run — the same packages with no race detector, no database and no build tags, so the DB-gated suites do not compile in — and `go:test-race` is the push's run, with `-race` and the live databases the record declares. A caller that asks for both stages at once (the door's gate lane, until F16 runs the stages separately) gets the race run and an omission line for the unit one: `AtomDef.SubsumedBy` stands it down rather than compiling the suite twice.

**`fleet:`** — every repository, whatever it is written in.
`check-yaml` · `check-added-large-files` · `check-merge-conflict` · `stop-justifications` · `hadolint` · `sast-ruleset-lanes` · `opengrep-sast` (commit) · `orbit-drift` · `dagger-lockstep` · `witness` (push)

**`go:`** (`go.mod`) — `gofmt` · `vet` · `test` (commit) · `staticcheck` · `govulncheck` · `build` · `release` · `test-race` (push)

**The release build is derived, not declared (CA F13).** `go:release` compiles what the image will carry, with the image's own flags, and `dagger call release` hands F14's Build that same directory — the engine answers it from the exec the push already ran, so nothing is compiled twice. Measured across the fleet's 29 Go Dockerfiles: the flags are one constant (`CGO_ENABLED=0 -trimpath -ldflags="-s -w"`), the target is always `./cmd/<X>` → `/out/<X>`, and `-mod=vendor` appears in exactly the five repos carrying `vendor/`. So the convention is the star's own name, `vendor/` decides the flag, and a record says something only when the repo ships more than its own name — `tools.build.binaries`, which today is two repos (`blade-runner`, `clio`). Walking `cmd/` is deliberately NOT the rule: helios and thalia carry four `cmd/` directories between them that their images do not ship.

**The image is the base plus the artifact (CA F14/F17).** A star's Dockerfile that copies from `release/` — `FROM registry.notusmi.com/foundry/base-images/<lang>:stable@sha256:…` + `COPY release/<star> /<star>` + `CMD` — is asking for the Gate's release build, and the build lane stages `dagger call release`'s directory at `release/` in the build context before the Dockerfile runs: the same exec `go:release` ran at the push, so the engine answers it from cache and the image compiles nothing. `buildlane.CopiesRelease` is the whole contract, and `go:release` reads it too: a Dockerfile that still carries its own build stage copies nothing from `release/`, gets nothing staged, builds as it always did, and the atom settles ABSENT rather than compile a binary the image never carries (narcissus, whose tree-sitter analyzers are cgo and compile statically in their own stage) — so the fleet flips one star at a time. A release that does not compile settles the lane as findings in the release build (could-not-run on a fault), before anything is built on top of it. `release/` is never in the tree; the templates' `.gitignore` refuses it.

**A bun or python image's release is built on its own base (CA F17).** The image's base picks the release build, not the tree's manifests — mnemosyne carries a `go.mod` and the `pyproject.toml` its Go port left behind, and only the go base made its image (`buildlane.BaseToolchain`). A Dockerfile on `foundry/base-images/python` gets `python:release`: `uv sync --locked --no-dev --no-editable` plus the record's `tools.build.extras`, run at `/app` ON that base so the venv's shebangs and interpreter are the image's, staged at `release/app` for `COPY --chown=999:999 release/app /app`. A Dockerfile on `foundry/base-images/bun` gets `ts:release`: `bun install --frozen-lockfile`, then the record's `tools.build.release` steps (one argv each — there is no bun convention), whose `release/` is what the COPY gets. uv exits 2 on every error, so a step's failure is read by its words (`checks.ReleaseStepState`): a network fault is the substrate's, anything else a command chose is the tree's.

**Nothing is published that the scan did not pass (CA F14).** The build lane runs Verify's `image:trivy` atom on the image the engine just built — the same chain the publish pushes — before it publishes: a fixable HIGH or CRITICAL in the star's **own** layer settles the lane as findings and nothing is pushed, so the registry never holds it; a base's findings are named in the log and not counted (they are the bases lane's); a scan that could not run is could-not-run, never a pass. A pull reports the same scan, so the finding is on the pull, not the landing. The SBOM half of Verify is what the lane already attaches at attest time; the standalone `verify` stage stays for the door's own run of the chain (F16).

**`python:`** (`pyproject.toml`) — `ruff-check` · `ruff-format` · `forge-testkit-assertion-free` · `forge-testkit-fake-placement` · `forge-testkit-schema-budget` · `mypy` · `pytest` (commit) · `pip-audit` (push)

**`rust:`** (`Cargo.toml`) — `cargo-fmt` · `cargo-clippy` · `cargo-test` (commit) · `cargo-audit` (push)

**`ts:`** (`package.json`) — `bun-gate` (commit) · `bun-audit` (push) · `visual` (the visual lane, its own Job: `gate-file --stage=visual` runs every Playwright screenshot suite a root `visual.toml` declares, in the ts image, against the committed baselines; a red run pushes its diffs and the baselines it would accept to `registry.notusmi.com/foundry/visual/<star>:<sha>`)

**`compose:`** (a tracked `compose.ya?ml`) — `config` · `no-tracked-secrets` · `third-party-pins` (commit)

**`dies:`** (`policy/.manifest` **and** `fleet/stars/`) — `opa-test` · `contracts` · `schema` (commit) · `admission-dogfood` · `data-keys` · `canary-visibility` (push)

### The rulesets are the fleet's

No atom reads a configuration the repository authored — not `pyproject.toml`'s `[tool.ruff]`, not `staticcheck.conf`, not `.pre-commit-config.yaml`, and not `.hadolint.yaml`. `fleet:hadolint` is the worked example: hadolint reads `.hadolint.yaml` from the working directory by default, so the atom writes the fleet's ruleset (`checks.HadolintConfig`) to `/etc/hadolint/fleet.yaml` inside the lane container and names it with `--config`, which *replaces* that lookup rather than merging with it (measured). The ruleset is hadolint's own defaults (the failure threshold at info, written out) plus two measured decisions — apt package pins (DL3008) ignored because the digest pins the output and a Renovate-managed pin could not see Debian's security pocket, and `trustedRegistries` set to the mirror and the forge so a `FROM docker.io/…` is the finding images.go says it is. Every reason is beside its line in `internal/checks/hadolintlane.go`. A repository's own `.hadolint.yaml` serves its local pre-commit hook and nothing else; inline `# hadolint ignore=` pragmas beside a reason are honoured, as `noqa` is under `stop-justifications`.

### Two namespaces that are neither a lane nor a clock

`compose:` and `dies:` are cross-lane like `fleet:`, run on a pull's path like `fleet:`, and are **not** `fleet:` — because `fleet:` is the namespace whose atoms have something to say about *every* repository, which is what makes `dagger check fleet:` worth typing. These have something to say about six of the eighty-six. Their condition is a **surface** the atom finds inside the tree rather than a root manifest a `Lane` can name, so the namespace names the surface and everywhere else they report `ABSENT` and say why. The set of surface namespaces is **closed** (`checks.SurfaceNamespaces`), none may carry `stage: sweep`, and `TestEveryAtomIsWellFormed` refuses an id in an undeclared one.

They exist because the **act-runner is being removed**, and what it validated is validated by the gate or not at all:

| atom | ported from | what it asks | ABSENT when |
| :-- | :-- | :-- | :-- |
| `compose:config` | the stacks repos' former validate workflow | every tracked compose spec parses, env_file targets stubbed, `--no-interpolate` | no tracked `compose.ya?ml` |
| `compose:no-tracked-secrets` | same | no `.env` / `envs/` / `.pem` / `.key` / `_rsa` file is **tracked** | ″ |
| `compose:third-party-pins` | nas01's BP6b ratchet | zero `${PIN_}` image interpolations, as a **count** | ″ |
| `dies:opa-test` | foundry-dies' former ci workflow | the rego unit + invariant suite passes | not `policy/.manifest` + `fleet/stars/` |
| `dies:admission-dogfood` | ″ | the admission domain admits our own star shape | ″ |
| `dies:data-keys` | ″ | the **built bundle** carries every data root, non-empty | ″ |
| `dies:canary-visibility` | ″ | the **built bundle** still hides a curated verb from a session principal | ″ |
| `dies:contracts` | `contracts.yml` | every copy of every shared closed set agrees — after 7 fixtures fail and 3 controls pass | ″ |
| `dies:contract-copies` | foundry-tools#15237 | every vendored copy **this tree holds** matches foundry-dies' authority (closed set and bytes), graded here instead of on the next dies pull | a tree that holds no copy `contracts.toml` declares; foundry-dies itself |
| `dies:schema` | `schema.yml` | the slag schema is valid Draft 2020-12 and every v2 record satisfies it | ″ |

Three things the ports changed on purpose, each because the workflow's assumption was about its runner rather than about the check:

- **`--no-interpolate` stays, and the env stub reads grep's exit code.** `grep` is three-valued — 0 selected, 1 selected nothing, ≥2 *the scan broke* — and a `|| true` collapses all three into "nothing to stub", stubs nothing, and hands the parse a tree missing every file it was supposed to create. Both workflows had to fix that in their own bodies; here rc 1 is the answer and rc ≥2 is a refusal.
- **`opa test` over a policy tree with no assertion in it exits 0** (measured 2026-09-10). That renders as a clean suite and is not one, so `dies:opa-test` reads the `PASS: n/n` count and refuses a zero-test run — `opengrep` matching zero files, wearing different clothes.
- **An unreachable door is a `CANNOT RUN`, not a divergence.** `check_contracts.py` returns 1 for a copy it could not fetch, which is right for a runner sitting on the door's own network and wrong for an atom: a copy that could not be *fetched* is not a copy that *disagrees*, and reporting one as the other sends a reader to reconcile lists that may be identical. The atom probes the door first, and the probe target is read **out of the manifest** rather than named — the same lesson the canary learned twice.

`dies:data-keys` and `dies:canary-visibility` interrogate the **artifact, never the source tree**, and that distinction is measured: rename every `policy/*/data.json` to `values.json` and `opa test` still passes 312 assertions while the built bundle ships `data.json == {}` — `star_only` undefined, the visibility comprehension collecting nothing, **every verb visible to every principal**. Fail-open, silent, and green the whole way down.

**`sweep:`** — repo cadence, on a clock, **never in a pull's path**.
`template-render-matrix` · `kubeconform` · `kube-linter` (weekly)

These describe a **repository** rather than a change, so their answer cannot differ between two pulls against the same repo — and running them per pull leaves every repository nobody opened a PR against unevaluated indefinitely.

| atom | what it asks | ABSENT when |
| :-- | :-- | :-- |
| `sweep:template-render-matrix` | every case in this template's `ci-matrix.toml` still renders | no `ci-matrix.toml` |
| `sweep:kubeconform` | every manifest under `flux/` validates against its Kubernetes schema | no `flux/` |
| `sweep:kube-linter` | every workload under `flux/` passes kube-linter's default checks | no `flux/` |

**The absence is the acceptance.** CA F9's success criterion is that no `stage: sweep` atom ever appears in a pull's path, and that is structural here rather than conventional: `dagger call verdicts` with **no stage** answers the *pull-path* vector — precommit and prepush — so a door that asks for "the vector" cannot be handed a sweep atom by omission. You get the sweep by naming it (`--stage=sweep`, or `dagger check sweep:`) and no other way. `TestNoSweepAtomOnThePullPath` asserts it.

**`sweep:portfolio-sbom` retired 2026-09-16.** It asked whether a repo's image was built through a foundry-stocks workflow that attests its SBOM — a question about `.forgejo/workflows`, which the fleet largely no longer carries: measured at retirement, **37 of the 39 repos with a root `Dockerfile` had no workflow tree at all**, so the atom answered CANNOT RUN for all but two. More decisively, the question moved. Since the build lane attaches the SBOM as a plain OCI referrer and attests an `sbom-ref/v1` pointer itself, attestation is a property of building through the door, not of a workflow file existing. The weekly `portfolio-weekly` CronJob and `ci-portfolio-pipeline` are untouched and still re-score what the registry holds.

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
  images.go         the lane AND sweep images, in one place
```

`internal/checks` imports nothing from the Dagger SDK, which is why `go test ./...` runs without an engine.
