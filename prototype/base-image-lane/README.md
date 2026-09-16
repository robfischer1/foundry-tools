# base-image-lane — a prototype, NOT wired to anything

This is the #30 prototype: **a base image through foundry-tools' image lane,
then trivy**. It is preserved here because it was proven against the real
cluster engine and otherwise lived only in a session scratchpad. It is a
nested Go module, so `go build ./...` at this repo's root does not see it
(`go list ./...` matches it zero times) and no lane calls it.

**Do not treat it as the design of record.** Two things about it are settled
and several are not.

## What was proven, each against a real repo on the real engine

- **A named base from a subdirectory.** `Image(base="python-runtime")` builds
  `bases/python-runtime/Dockerfile`. The whole of multi-base support is
  pointing foundry-tools' `Image` at a subdirectory `Source` — it builds the
  Dockerfile at the root of whatever `Directory` it is handed
  (`image.go`: `i.Source.DockerBuild`). **No fork of the image lane**, and
  every base gets the labels, SBOM and signature a star's image gets.
- python-base-image `bases/python-runtime` → rc=0, *scan clean: no fixable
  HIGH or CRITICAL vulnerabilities*.
- go-base-image root Dockerfile, no `--base` → rc=0, scan clean. No regression
  on the shape #27 already ships.
- Trivy gate **negative control**: rc=1, *scan found 15 fixable HIGH or
  CRITICAL vulnerabilities*. The gate reds, so it can fail.
- `Bases()` enumeration: foundry-stocks 27, python-base-image 1,
  go-base-image 0.

## Two defects found and fixed while building it

1. `Bases()` first listed every **entry** under `bases/` and answered 29 for
   foundry-stocks against 27 real bases — `README.md` and `departed.toml` sit
   beside them. It would have sent the lane to build
   `bases/README.md/Dockerfile`. A base is now **defined as a directory
   holding a Dockerfile**.
2. **The relock phase was missing**, and the prototype could not have found
   it: it only ever built go-base-image, which ships no `pyproject`. Building
   the python base failed at the Dockerfile's bind mount —
   `/bases/python-runtime/uv.lock: not found`. `ca-bases` carries this
   deliberately and its own comment says the phase was **lost** when
   `build-base.yml` left, not omitted. Carried in here with the
   **single-index seam** intact: the lock and the Dockerfile's
   `uv sync --locked` must resolve against the same index or the build rejects
   a lock written seconds earlier (measured foundry-stocks#7278: Nexus
   185,665 B vs upstream 207,123 B).

## What is NOT proven, and what is NOT decided

- **Only `scan` is exercised.** `publish` / `sign` / SBOM / `:stable` have
  never run through this module.
- **`:stable` moves without a mold permit in this lane.** That is a question
  about release authority, not an implementation gap, and it is open.
- The DAG order, the stage phase (assembling `COPY --from` contexts) and
  retention — all of which `ca-bases` does in 555 lines — are **not** here.
- The module's `dagger.json` is **not carried here**. It pinned foundry-tools
  at `1960e3de` (a pin already behind) and declared engine `v0.21.9` with the
  Go SDK; the full 40-character sha tripped detect-secrets as a high-entropy
  string, and a regenerable file is not worth an entry in the repo's baseline.
  Recreate it with `dagger init`/`dagger install` against a current pin.

## Where the real work has to land

`ca-bases` (infra `flux/apps/ca-recipe.yaml`, 555 lines) orchestrates
`forge-bases`, a binary pinned by digest from `stellar_core:forge-tools`.
This design **replaces** that rather than porting it, which is why it deletes
more than it moves. The bases lane is the last lane that is still a script,
and #49 is waiting on it.

The scope correction from #30 that matters most: **32 bases across 7 repos**,
of which foundry-stocks holds 27. Rob's decision
(*"foundry/base-images but move the repo-per-language work over as
subdirectories rather than independent git repos, same structure we used for
blades"*) covers the repo-per-language ones. **foundry-stocks' 27 stay where
they are**, so `ca-recipe`/`ca-bases` stays alive until they move too.
