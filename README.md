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
| **Namespaced** | `fleet:` · `go:` · `python:` · `rust:` · `ts:` on a pull's path, beside `compose:` · `dies:` · `ops:` · `template:` · `wit:`. The catalogue (`internal/catalogue`) lists all of them with descriptions. |
| **One definition** | The atom table in `internal/checks/atoms.go` drives both the check functions and the verdict vector, so the runner and the catalogue cannot drift apart. The census counted that drift 31 times across two independently-maintained surfaces; there is one surface here. |

## Using it

The atoms run on the cluster's in-pod executor (daedalus), never on a workstation and never on a Dagger engine (the module and the engine were retired 2026-10-24).

```sh
just check                            # the commit stage: `foundry-toolbox ask check`, the door's check lane on your staged tree
just _dev test ./internal/checks/...  # a narrower loop: the door's dev lane tests your worktree on the cluster
```

A pushed branch's lanes are its gate: the door runs them on the head, `git_pr open` parks on them, and `repo_ci_logs` reads them, atom by atom. Each atom lives in its own directory, `atoms/<ns>/<name>/<name>.go` (a logic atom) or `atoms/<ns>/<name>.toml` (a command atom); the `atoms` lane compiles them into the index the executor runs (see the last section).

## The atoms

**Who votes (F5a, the cutover).** Fifty of the cheap atoms (every `fleet:` `compose:` `dies:` `ops:` `template:` and `wit:` atom except `ops:kube-linter`) are also compiled into one binary, `internal/atoms`, built in the Dagger pipeline into a **tools container**: a pinned `debian:bookworm-slim` with each third-party tool as a checksum-verified layer, the python interpreter and venv, and the binary on top. The vector the door reads (the gate stage, the prepush stage, and the local hook's `dagger call check`) takes the verdict of each of those atoms from that binary, run **once per lane** in that container, and slots it into the vector in catalogue order; the record's shape is untouched. The toolchain tier (`go:` `python:` `rust:` `ts:`, `ops:kube-linter`, mutation) keeps its chains. **An atom the binary settles could-not-run (any reason: a network error, a tool layer that did not build, a crash, an OOM, a lane deadline) is asked once through its old chain, and the chain's answer is the vote**, so a broken binary or tools layer cannot turn all fifty red at once; a finding is never re-asked. `fleet:witness` asks narcissus for real as the voter, with the lane's SPIRE socket mounted; its chain runs only if the binary could not, so narcissus is asked once. **Rollback is one constant**: set `defaultVoter` in `atoms_vote.go` to `voterChains` and the chains vote again; the binary does not run.

**An unrelated edit re-runs nothing (CA F12).** The atoms that compile, vet, lint or test mount the tree less `checks.InertPaths` — the root prose by name (README, CHANGELOG, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, the governance furnace pours), licences, `docs/`, `specs/`, the CI directories, the hooks, the justfiles, pre-commit's and copier's files — so their execs are keyed on the code. A commit that touches only those hits the engine's cache for every language atom; the basic fanout (secrets, large files) still reads the whole tree, because a secret in a README is exactly what it is for. The set is an exclude measured against what the fleet's tests actually open (a Dockerfile, a `.melt`, `migrations/`, `plugins/`, `CLAUDE-INIT.md`), never an include derived from `go list`; every pattern is root-anchored so nothing reaches a package's fixtures, and **no prose is excluded by glob** — the first cut shipped `*.md` and hephaestus went red on the one `.md` its module-map test reads (`open /src/CLAUDE-INIT.md: no such file`). A glob excludes what nobody enumerated; a name goes on the list only after the grep finds nothing opening it. The mutation lane and the witness keep the whole tree — they run git against it.

**The hooks are two calls (CA F15).** `just hooks` points this clone's `core.hooksPath` at the tracked `hooks/` directory: the commit hook is `just check` (`foundry-toolbox ask check`), the push hook runs nothing since 2026-10-02 (the door asks a pushed branch's lanes on the cluster), and `just gate` only says so. The hooks are one line each and never change; the recipes in the justfile are what a commit must pass, and a session runs the same recipes by hand. They are the same stages the door runs, so a commit that passes here passes there. The engine is a dependency on purpose — it is where the git origin already lives — and an engine that cannot be reached refuses the commit rather than waving it through. `just unhooks` puts a clone back on the pre-commit framework while the fleet is mid-flip. In foundry-tools itself the recipes call `-m .`, because a module whose job is gating must gate its own changes with the code being changed, not with main; the fleet's copy (foundry-stocks `speckit/stages.just`, imported by each star's justfile) calls the module at main.

**Stages (CA F12, 2026-09-17).** `precommit` is the **commit** stage (the `check` lane) — and holds the basic checks every tree gets (the `fleet:` namespace) beside the language checks for what the tree contains: format, lint and tests. `prepush` is the **push** stage (the `gate` lane) — and holds the complex checks — deep lint, vulnerability audits, the build, the race + live-database suite, the bundle probes, the witness — run **in sequence, cheapest first, stopping at the first one that finds something**, beside the mutation lane, which runs concurrently and is allowed to finish. A stopped sequence names the atoms it never reached. `internal/checks/stage_test.go` pins both lists.

**The Go suite is split by cadence (CA F13).** `go:test` is the commit's unit run — the same packages with no race detector, no database and no build tags, so the DB-gated suites do not compile in — and `go:test-race` is the push's run, with `-race` and the live databases the record declares. A caller that asks for both stages at once (the door's gate lane, until F16 runs the stages separately) gets the race run and an omission line for the unit one: `AtomDef.SubsumedBy` stands it down rather than compiling the suite twice.

**`fleet:`** — every repository, whatever it is written in.
`check-yaml` · `check-added-large-files` · `check-merge-conflict` · `stop-justifications` · `hadolint` · `vale` · `sast-ruleset-lanes` · `opengrep-sast` (commit) · `renovate-lockstep` · `witness` (push)

**`fleet:vale` lints the prose a model reads as instructions.** Every tracked `SKILL.md`, `CLAUDE.md`, `CLAUDE-INIT.md`, `AGENTS.md`, `GEMINI.md` or `COWORK.md`, and every markdown file under a `.claude/`, `skills/`, `agents/`, `rules/` or `hooks/` segment (`checks.IsModelProse`; `testdata/`, `fixtures/`, `node_modules/` and `vendor/` are skipped), is held to the **Forge** style, a Vale style kept in foundry-dies at `styles/Forge` and read off the lane's dies mount. The atom writes the fleet's configuration for the run (`checks.ValeConfig`) and runs vale with `--no-global`, so no repository's `.vale.ini` is read, and the configuration's sections are the per-artifact profiles: `Forge.ArgumentSlot` (a `$` before a digit, which Claude Code reads as an argument slot) fires only in a skill body. Vale's exit is the verdict: an error-level alert is a finding, warnings and suggestions are printed and pass, and a run that could not start is a 2. Why the rules are rules is in each `.yml`'s header; the measurement behind their levels (748 files across 54 repos, 2026-10-30) is in the landing that added them.

**`go:`** (`go.mod`) — `gofmt` · `vet` · `test` (commit) · `staticcheck` · `govulncheck` · `build` · `release` · `test-race` (push)

**The release build is derived, not declared (CA F13).** `go:release` compiles what the image will carry, with the image's own flags, and `dagger call release` hands F14's Build that same directory — the engine answers it from the exec the push already ran, so nothing is compiled twice. Measured across the fleet's 29 Go Dockerfiles: the flags are one constant (`CGO_ENABLED=0 -trimpath -ldflags="-s -w"`), the target is always `./cmd/<X>` → `/out/<X>`, and `-mod=vendor` appears in exactly the five repos carrying `vendor/`. So the binaries are the Dockerfile's own `COPY release/<name>` lines (each built from `./cmd/<name>`, and a COPY with no `cmd/<name>` behind it is a finding), `vendor/` decides the flag, and nothing is declared in the record. Walking `cmd/` is deliberately NOT the rule for an image: helios and thalia carry four `cmd/` directories between them that their images do not ship. **The build lane's compile is stamped** (foundry-tools#18676): the Gate's `go:release` links no version, but the build lane, which knows the commit, calls `Release --version={committer time}-{sha7}` (the stamp tag's own tail) and the compile adds `-X` for `main.Version`, `main.version` and the module's `internal/config`, `internal/verbs` and `verbs` `Version` vars — the linker ignores a symbol a binary lacks. A star keeping its version in a `const` cannot be stamped and owns its own change.

**The image is the base plus the artifact (CA F14/F17).** A star's Dockerfile that copies from `release/` — `FROM registry.notusmi.com/foundry/base-images/<lang>:stable@sha256:…` + `COPY release/<star> /<star>` + `CMD` — is asking for the Gate's release build, and the build lane stages `dagger call release`'s directory at `release/` in the build context before the Dockerfile runs: the same exec `go:release` ran at the push, so the engine answers it from cache and the image compiles nothing. `buildlane.CopiesRelease` is the whole contract, and `go:release` reads it too: a Dockerfile that still carries its own build stage copies nothing from `release/`, gets nothing staged, builds as it always did, and the atom settles ABSENT rather than compile a binary the image never carries (narcissus, whose tree-sitter analyzers are cgo and compile statically in their own stage) — so the fleet flips one star at a time. A release that does not compile settles the lane as findings in the release build (could-not-run on a fault), before anything is built on top of it. `release/` is never in the tree; the templates' `.gitignore` refuses it.

**A bun or python image's release is built on its own base (CA F17).** The image's base picks the release build, not the tree's manifests — mnemosyne carries a `go.mod` and the `pyproject.toml` its Go port left behind, and only the go base made its image (`buildlane.BaseToolchain`). A Dockerfile on `foundry/base-images/python` gets `python:release`: `uv sync --locked --no-dev --no-editable` with no extras, run at `/app` ON that base so the venv's shebangs and interpreter are the image's, staged at `release/app` for `COPY --chown=999:999 release/app /app`. A Dockerfile on `foundry/base-images/bun` gets `ts:release`: `bun install --frozen-lockfile`, then the repo's own `bun run release` (a root package.json script, the same interface as `gate` and `build`), whose `release/` is what the COPY gets. uv exits 2 on every error, so a step's failure is read by its words (`checks.ReleaseStepState`): a network fault is the substrate's, anything else a command chose is the tree's.

**Nothing is published that the scan did not pass (CA F14).** The build lane runs Verify's `image:trivy` atom on the image the engine just built — the same chain the publish pushes — before it publishes: a fixable HIGH or CRITICAL in the star's **own** layer settles the lane as findings and nothing is pushed, so the registry never holds it; a base's findings are named in the log and not counted (they are the bases lane's); a scan that could not run is could-not-run, never a pass. A pull reports the same scan, so the finding is on the pull, not the landing. The SBOM half of Verify is what the lane already attaches at attest time; the standalone `verify` stage stays for the door's own run of the chain (F16).

**`python:`** (`pyproject.toml`) — `ruff-check` · `ruff-format` · `forge-testkit-assertion-free` · `forge-testkit-fake-placement` · `forge-testkit-schema-budget` · `mypy` · `pytest` (commit) · `pip-audit` (push)

**`rust:`** (`Cargo.toml`) — `cargo-fmt` · `cargo-clippy` · `cargo-test` (commit) · `cargo-audit` (push). A workspace that carries `tools/scope/scope.py` opts into per-crate scoping: `cargo-clippy` and `cargo-test` grade the crates the pull's diff reaches (the script names them and the tests that belong to them) instead of `--workspace`, and the whole workspace whenever a file in the root package changes or anything is in doubt. `wit-guest`'s native lib tests run `--workspace`, because a world's tests live in its crate.

**`ts:`** (`package.json`) — `bun-gate` (commit) · `bun-audit` (push) · `visual` (the visual lane, its own Job: `gate-file --stage=visual` runs every Playwright screenshot suite a root `visual.toml` declares, in the ts image, against the committed baselines; a red run pushes its diffs and the baselines it would accept to `registry.notusmi.com/foundry/visual/<star>:<sha>`)

**`compose:`** (a tracked `compose.ya?ml`) — `config` · `no-tracked-secrets` · `third-party-pins` (commit)

**`dies:`** (`policy/.manifest` **and** `fleet/repos/`) — `opa-test` · `contracts` · `schema` (commit) · `admission-dogfood` · `data-keys` · `canary-visibility` (push)

### The rulesets are the fleet's

No atom reads a configuration the repository authored — not `pyproject.toml`'s `[tool.ruff]`, not `staticcheck.conf`, not `.pre-commit-config.yaml`, and not `.hadolint.yaml`. `fleet:hadolint` is the worked example: hadolint reads `.hadolint.yaml` from the working directory by default, so the atom writes the fleet's ruleset (`checks.HadolintConfig`) to `/etc/hadolint/fleet.yaml` inside the lane container and names it with `--config`, which *replaces* that lookup rather than merging with it (measured). The ruleset is hadolint's own defaults (the failure threshold at info, written out) plus two measured decisions — apt package pins (DL3008) ignored because the digest pins the output and a Renovate-managed pin could not see Debian's security pocket, and `trustedRegistries` set to the mirror and the forge so a `FROM docker.io/…` is the finding images.go says it is. Every reason is beside its line in `internal/checks/hadolintlane.go`. A repository's own `.hadolint.yaml` serves its local pre-commit hook and nothing else; inline `# hadolint ignore=` pragmas beside a reason are honoured, as `noqa` is under `stop-justifications`.

### Two namespaces that are neither a lane nor a clock

`compose:` and `dies:` are cross-lane like `fleet:`, run on a pull's path like `fleet:`, and are **not** `fleet:` — because `fleet:` is the namespace whose atoms have something to say about *every* repository, which is what makes `dagger check fleet:` worth typing. These have something to say about six of the eighty-six. Their condition is a **surface** the atom finds inside the tree rather than a root manifest a `Lane` can name, so the namespace names the surface and everywhere else they report `ABSENT` and say why. The set of surface namespaces is **closed** (`checks.SurfaceNamespaces`), and `TestEveryAtomIsWellFormed` refuses an id in an undeclared one.

`law:` (Nomos F5b) is the same kind of namespace for foundry-stocks' governance law: `law:lint`, `law:verb-liveness` and `law:budget` run on every law commit and report `ABSENT` on any tree that lacks a root `kits.toml` beside a `retired-verbs.toml`. Verb liveness grades the blocks and rendered context documents against `retired-verbs.toml` and a snapshot of hades's wire surface (`internal/govlaw/verb-surface.toml`; a foundry-stocks checkout's own `verb-surface.toml` overrides it) because a gate atom cannot list the live gateway.

They exist because the **act-runner is being removed**, and what it validated is validated by the gate or not at all:

| atom | ported from | what it asks | ABSENT when |
| :-- | :-- | :-- | :-- |
| `compose:config` | the stacks repos' former validate workflow | every tracked compose spec parses, env_file targets stubbed, `--no-interpolate` | no tracked `compose.ya?ml` |
| `compose:no-tracked-secrets` | same | no `.env` / `envs/` / `.pem` / `.key` / `_rsa` file is **tracked** | ″ |
| `compose:third-party-pins` | nas01's BP6b ratchet | zero `${PIN_}` image interpolations, as a **count** | ″ |
| `dies:opa-test` | foundry-dies' former ci workflow | the rego unit + invariant suite passes | not `policy/.manifest` + `fleet/repos/` |
| `dies:admission-dogfood` | ″ | the admission domain admits our own star shape | ″ |
| `dies:data-keys` | ″ | the **built bundle** carries every data root, non-empty | ″ |
| `dies:authz-grid` | foundry-dies `tools/check_authz_grid.py` | a pull's Rego gives every authz_v2 grid cell the answer the base's Rego does, over the head's data and with `time.now_ns` pinned (no stored golden; each side is asked with the decision table it derives; a pull touching no `.rego` passes unevaluated; a deliberate change carries an `Authz-Semantics-Change` trailer naming the moved-cells digest) | ″ |
| `dies:render-author` | themis render loop (S2) | a pull with a commit authored or committed as `themis@pantheon.foundry` changes only `policy/grants/data.json` and `policy/principals/data.json` | ″ |
| `fleet:l7-render` | nyx render worker (nyx#22257) | flux `prime/first-l7-enforcement.yaml` gives every verb a star serves and the grants allow (the published `prime/first-l7-enforcement.openapi/`) a matching hades-sa path, no CNP/CCNP/NetworkPolicy admits a peer to hades's attributed :8101 at L4 (an L4 allow voids the L7 stamp; a deny from that entity or `all` covers it), and a pull with a commit by `nyx@pantheon.foundry` changes only that file and its published copies | ″ |
| `dies:canary-visibility` | ″ | the **built bundle** still hides a curated verb from a session principal | ″ |
| `dies:contracts` | `contracts.yml` | every copy of every shared closed set agrees — after 7 fixtures fail and 3 controls pass | ″ |
| `dies:contract-copies` | foundry-tools#15237 | every vendored copy **this tree holds** matches foundry-dies' authority (closed set and bytes), graded here instead of on the next dies pull | a tree that holds no copy `contracts.toml` declares; foundry-dies itself |
| `dies:schema` | `schema.yml` | every `fleet/repos/<name>.json` catalog entry satisfies `schema/fleet-star.schema.json` (and a tree still carrying `fleet/stars/`, the retired slag, is refused) | ″ |

Three things the ports changed on purpose, each because the workflow's assumption was about its runner rather than about the check:

- **`--no-interpolate` stays, and the env stub reads grep's exit code.** `grep` is three-valued — 0 selected, 1 selected nothing, ≥2 *the scan broke* — and a `|| true` collapses all three into "nothing to stub", stubs nothing, and hands the parse a tree missing every file it was supposed to create. Both workflows had to fix that in their own bodies; here rc 1 is the answer and rc ≥2 is a refusal.
- **`opa test` over a policy tree with no assertion in it exits 0** (measured 2026-09-10). That renders as a clean suite and is not one, so `dies:opa-test` reads the `PASS: n/n` count and refuses a zero-test run — `opengrep` matching zero files, wearing different clothes.
- **An unreachable door is a `CANNOT RUN`, not a divergence.** `check_contracts.py` returns 1 for a copy it could not fetch, which is right for a runner sitting on the door's own network and wrong for an atom: a copy that could not be *fetched* is not a copy that *disagrees*, and reporting one as the other sends a reader to reconcile lists that may be identical. The atom probes the door first, and the probe target is read **out of the manifest** rather than named — the same lesson the canary learned twice.

`dies:data-keys` and `dies:canary-visibility` interrogate the **artifact, never the source tree**, and that distinction is measured: rename every `policy/*/data.json` to `values.json` and `opa test` still passes 312 assertions while the built bundle ships `data.json == {}` — `star_only` undefined, the visibility comprehension collecting nothing, **every verb visible to every principal**. Fail-open, silent, and green the whole way down.

**There is no `sweep` stage (deleted 2026-09-23, CA F18).** It was repo cadence on `ca-sweep`'s clock, and the walk cloned eighty repos to ask six questions of the five that have the surface. `sweep:kubeconform` was already subsumed by `ops:flux`, which runs kubeconform with the same flags over the built kustomize output; the other two came home into the pull path as `ops:kube-linter` (commit) and `template:render-matrix` (push), and answer `ABSENT` in a repo that has no `flux/` or no `ci-matrix.toml`. `dagger call verdicts` with no stage answers the pull-path vector, precommit and prepush; `TestTheCommitAndPushStagesHoldTheirAtoms` pins what the two pull stages hold. The stages that are not on the pull path are asked for by name: `mutation`, `visual`.

**`sweep:portfolio-sbom` retired 2026-09-16.** It asked whether a repo's image was built through a foundry-stocks workflow that attests its SBOM — a question about `.forgejo/workflows`, which the fleet largely no longer carries: measured at retirement, **37 of the 39 repos with a root `Dockerfile` had no workflow tree at all**, so the atom answered CANNOT RUN for all but two. More decisively, the question moved. Since the build lane attaches the SBOM as a plain OCI referrer and attests an `sbom-ref/v1` pointer itself, attestation is a property of building through the door, not of a workflow file existing. The weekly `portfolio-weekly` CronJob and `ci-portfolio-pipeline` are untouched and still re-score what the registry holds.

### What is deliberately NOT here

- **`pyright`** — two type checkers ran on every python pre-push with no incident behind the duplication. One type checker, `mypy --strict`. Ratified.
- **`trailing-whitespace` / `end-of-file-fixer`** — removed fleet-wide on measurement (0.7% and 0.75% yield) and, in the second case, an outage: a 24-repo red-CI cascade over an unterminated `#copier updated` sentinel. They are not atoms; they are deletions.
- **`worktree-guard`** — it asserts the *commit* was made from a linked worktree rather than the shared checkout. Its own script stands down under `$CI`, and an engine IS a CI boundary, so an atom here would be a permanent no-op. It stays at the commit boundary, where the fact it tests exists.
- **`lint-staged`** — defined over the git **index**. The engine receives a directory, not an index; an atom claiming to be lint-staged would be checking a different population than the hook it replaced, which is the silent-drift failure this module exists to end.
- **`fetch-origin`** — a background convenience with no verdict. It was never a check.

The one check that is deliberately **absent entirely** is the **mutation nightly full-run**: the machinery exists in `foundry-stocks` and stays unwired and unscheduled (decided, Rob). Mutation gates PR-time on the diff; there are no nightlies until further notice.

## Working on it

```sh
just _dev test <pkgs>   # the door's dev lane: tests this worktree on the cluster
just check              # the commit stage, as hooks/pre-commit runs it
```

## Layout

```
atoms/<ns>/<name>/  one atom: <name>.go and its tests, nothing else
atoms/<ns>/*.toml   the command atoms' manifests
lanes/*.toml        each lane's phases, as lane@1
internal/checks/    the shared judgements, engine-free and unit-tested
internal/catalogue/ the generated registry of every atom (registry_gen.go)
tools.toml          the pinned third-party tools the atoms exec
```

## The atom index follows the default tip

Every landing on this repo's default branch owes the `atoms` lane: daedalus asks it for the tip, and its green settle republishes `foundry/atoms/index:main`, whose source sha is the tip it was built from. The ask does not depend on the tip's own gate running: a landing whose tree a parent already proved settles its gate from that proof (ourea `tree_proven`), and a tip that asks no lane is sent to daedalus as a lane-less dispatch (ourea `noteTip`), which is what daedalus asks `atoms` from, so the index follows it (measured 2026-10-20: three such landings left the index at 8930ebf).
