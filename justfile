# foundry-tools — the fleet's checks, as code.

# Regenerate the SDK bindings, then undo the SDK's own vulnerable pin.
#
# `dagger develop` re-adds four `replace` directives holding
# go.opentelemetry.io/otel/exporters/otlp/otlplog/* , otel/log and otel/sdk/log
# at v0.16.0, and v0.16.0 carries GO-2026-4985. Dropping them and re-tidying
# builds, tests and loads identically. A module whose whole point is that a
# check must not be silenced does not silence its own.
develop:
    dagger develop
    go mod edit \
      -dropreplace=go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp \
      -dropreplace=go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc \
      -dropreplace=go.opentelemetry.io/otel/log \
      -dropreplace=go.opentelemetry.io/otel/sdk/log
    go mod tidy

# THE TWO STAGES A COMMIT AND A PUSH MUST PASS (CA master-plan F15), as the
# recipes the tracked hooks call: hooks/pre-commit is `just check`; hooks/pre-push
# ran `just gate` until 2026-10-02 and now runs nothing (see `gate` below).
#
# `check` is the fleet's one line, `foundry-toolbox ask check`: the staged tree goes
# to the door's refs/checks/<c> and lane check runs on the cluster (SYS-008), the
# same as speckit/stages.just. That runs the DEPLOYED executor, not this tree's
# atoms: to grade a change to the atoms themselves with the code being changed,
# `just check-local` is the old `dagger call -m .` call.
#
# THE ENGINE IS A DEPENDENCY, DELIBERATELY. The stages run on the cluster's Dagger
# engine, so a commit needs the cluster — which is where the git origin already
# lives (Rob, 2026-09-17). An engine that cannot be reached refuses the commit
# rather than waving it through: a check that could not run is not a check that
# passed. NO SKIP LIST AND NO --no-verify: this is the gate the door runs.

# THE COMMIT STAGE is the basic checks every tree gets, beside the language
# checks for what this tree contains — format, lint, unit tests — planned before
# anything runs and settled on the worst of them. THE PUSH STAGE is the slow
# checks in sequence, cheapest first — deep lint, the known-vulnerability scan,
# the release build, the race + live-database suite — stopping at the first that
# finds something, with the mutation gate beside them. Its base is the merge base
# with origin/main, and only the atoms that grade a CHANGE read it; a repo with
# no origin/main yet (a fresh birth) is gated without one.

# The commit stage, on the cluster — what hooks/pre-commit runs.
check:
    foundry-toolbox ask check

# The commit stage on the engine with THIS tree's module (`-m .`, never main): a
# module whose job is gating gates its own changes with the code being changed.
check-local:
    dagger call -m . check exit

# The push stage runs nothing since 2026-10-02 (Rob, "Option 1": the lanes are
# the gate): the door asks a pushed branch's lanes on the cluster, metered by
# daedalus, and the pull that opens on the head reuses them (ourea #355).
# hooks/pre-push says so and exits 0; the recipe keeps its name for its callers
# and says the same. To grade a foundry-tools change against itself by hand,
# `dagger call -m . push --base=<merge-base> exit` is still the call.
gate:
    @echo "gate: this branch's lanes are its gate: the door runs them on the cluster for the head you pushed (metered by daedalus); git_pr open parks on them, and repo_ci_logs reads them" >&2

# List the atoms this module carries.
atoms:
    dagger check -l

# Point this clone's git hooks at the two stage calls (CA F15).
#
# `core.hooksPath` is repository configuration, not a file in the tree, so it is
# set once per clone and every linked worktree of that clone inherits it. The
# hooks themselves are TRACKED (hooks/), which is what makes them reviewable:
# a change to what a commit must pass arrives as a diff like any other.
#
# It also un-installs pre-commit's generated hooks by pointing away from
# .git/hooks — they are left on disk until the fleet has flipped, so a clone can
# be put back with `git config --unset core.hooksPath`.
hooks:
    git config core.hooksPath hooks
    @echo "hooks -> $(git config core.hooksPath) (pre-commit: just check, pre-push: just gate)"

# Put this clone back on pre-commit's generated hooks.
unhooks:
    git config --unset core.hooksPath
    @echo "hooks -> .git/hooks (pre-commit framework)"

# Seed the tool mirror with one pinned release asset (see internal/checks/toolmirror.go).
#
# Every pinned tool (checks.ToolSHA256: the wit, rust, tools-container and oras fetches)
# is fetched from registry.notusmi.com/foundry/tool/mirror first and from its own URL second, both verified against the same sha256. The mirror is
# a zot blob addressed by that sha256, so seeding is: fetch the asset from its own
# URL, prove it is the pinned bytes, push it under the tag zot's retention never
# reaps (sha256-<sum>). Idempotent: an asset already in the mirror is left alone.
#
# Needs a registry login that can write (`oras login registry.notusmi.com`, as
# publisher or rob); reads are anonymous. Examples (the full list is every ToolSHA256 pair):
#   just mirror-seed https://github.com/bytecodealliance/wasm-tools/releases/download/v1.261.0/wasm-tools-1.261.0-x86_64-linux.tar.gz ad62b2176037e93e1348cb65d6212d128ca9f097b63d155569f25215818ff7b1
#   just mirror-seed https://github.com/bytecodealliance/wac/releases/download/v0.12.0/wac-cli-x86_64-unknown-linux-musl 7cd490edbd452221ef89e6750fc43eacc9b71b1242095eb577cb9a3bf0d3beda
# The sum is the line in internal/checks/toolpins.go; a bump re-seeds with the new pair.
#
# Seed one pinned release asset into the tool mirror (idempotent).
mirror-seed url sum:
    #!/usr/bin/env bash
    set -euo pipefail
    ref="registry.notusmi.com/foundry/tool/mirror"
    if ! [[ "{{sum}}" =~ ^[0-9a-f]{64}$ ]]; then echo "mirror-seed: '{{sum}}' is not a lowercase sha256" >&2; exit 2; fi
    if oras manifest fetch --descriptor "$ref:sha256-{{sum}}" >/dev/null 2>&1; then
        echo "mirror-seed: sha256-{{sum}} is already in the mirror"; exit 0
    fi
    work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
    cd "$work"
    curl -fsSL --retry 3 -o asset "{{url}}"
    echo "{{sum}}  asset" | sha256sum -c -
    oras push --artifact-type application/vnd.foundry.tool.v1 "$ref:sha256-{{sum}}" asset:application/octet-stream
    # The blob the lanes fetch must answer anonymously and hash to the pin.
    got="$(curl -fsSL "https://registry.notusmi.com/v2/foundry/tool/mirror/blobs/sha256:{{sum}}" | sha256sum | cut -d' ' -f1)"
    [ "$got" = "{{sum}}" ] || { echo "mirror-seed: the mirror serves $got, not the pin" >&2; exit 1; }
    echo "mirror-seed: sha256-{{sum}} seeded and served at its pin"
