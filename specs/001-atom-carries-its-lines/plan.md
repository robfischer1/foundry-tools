---
title: "The atom carries its own lines"
spec: "./spec.md"
constitution: "../../.specify/memory/constitution.md"
status: draft
---

# The atom carries its own lines — Design Plan

> **Binding contract.** Every item is `decided` (executor MUST follow — no discretion)
> or `[OPEN]` (spec is silent and it matters — executor SURFACES it back, never invents).
> No advisory tier, no "use judgment." Open is the only license for discretion. (Constitution I/II)

## Summary

An atom's raw output already reaches `checks.VerdictOf`, which keeps a *rendered* copy of it
inside `Verdict.Reason` for failures and **throws it away for passes**. This feature adds three
fields — the atom's own lines, whether they were cut, and the true original size — to the one
shape that carries an atom's result, and threads them through the two mapping sites that copy
that shape outward. `Reason` is not touched. The core move is that the lines are captured at
`VerdictOf`, the single point where every atom's output is still in hand, rather than
reconstructed downstream from prose that no longer contains it.

## Architecture

The chain is three hops and two copies, verified at `5f2eeff`:

```
atoms_*.go  ──►  checks.VerdictOf(a, exit, output)  ──►  checks.Verdict
                                                             │  internal/checks/stage.go:91
                                                             ▼
                                                        checks.StageAtom
                                                             │  stage.go:187
                                                             ▼
                                                          AtomResult          (the module's wire shape)
```

| Path | Change | Responsibility |
| :--- | :--- | :--- |
| `internal/checks/logs.go` | **new** | `CaptureLogs(output string) ([]string, bool, int)` — the cap, the tail rule, the rune-safe cut. Pure. |
| `internal/checks/logs_test.go` | **new** | Every cap/empty/rune-boundary case |
| `internal/checks/verdict.go` | write | `Verdict` gains the three fields; `VerdictOf` and `AbsentVerdict` populate them |
| `internal/checks/stage.go` | write | `StageAtom` gains the three fields; the `:91` copy carries them |
| `stage.go` | write | `AtomResult` gains the three fields; the `:187` copy carries them |

**One capture point, not three.** `VerdictOf` is where every atom's output arrives, whatever
its state and whichever of the ~40 `atoms_*.go` call sites produced it. Capturing anywhere
downstream means capturing from `Reason`, which for a pass contains no output at all.

## Contracts & Seams

### Exposes — the interface this provides

| Surface | Signature / shape | State |
| :--- | :--- | :--- |
| `type:foundry-tools:AtomResult` | gains `Logs []string`, `Truncated bool`, `OriginalBytes int` beside the existing `{Atom, Group, State, Result, Reason}` | decided |
| `type:foundry-tools:internal/checks:Verdict` | gains `logs []string`, `truncated bool`, `original_bytes int` (JSON-tagged, `logs` never omitempty) | decided |
| `type:foundry-tools:internal/checks:StageAtom` | gains the same three | decided |
| `function:foundry-tools:internal/checks:CaptureLogs` | `CaptureLogs(output string) (logs []string, truncated bool, originalBytes int)` | decided |
| `const:foundry-tools:internal/checks:AtomLogCap` | `1 << 20` — 1 MB of output per atom | decided |

### Consumes / Requires — the seams (what this CALLS)

| Dependency | Contract relied on (signature consumed) | Pin |
| :--- | :--- | :--- |
| `function:foundry-tools:internal/checks:VerdictOf` | `VerdictOf(a AtomDef, exit int, output string) Verdict` — already receives the raw output | foundry-tools@5f2eeff, signature unchanged |
| `function:foundry-tools:internal/checks:reasonFor` | unchanged; `Reason` keeps its exact bytes | foundry-tools@5f2eeff |
| `stdlib:unicode/utf8` | rune-safe truncation | go stdlib |

### Resource-Reach — touched, field-level (VERIFIED against the real repo @ 5f2eeff)

| RR pointer | Access | Role | Used by |
| :--- | :--- | :--- | :--- |
| `file:foundry-tools/internal/checks/logs.go` | write (new) | `AtomLogCap`, `CaptureLogs` | Slice A |
| `field:foundry-tools:internal/checks:Verdict.Logs` | write (new) | the atom's own lines | Slice B |
| `field:foundry-tools:internal/checks:Verdict.Truncated` | write (new) | the cut, stated | Slice B |
| `field:foundry-tools:internal/checks:Verdict.OriginalBytes` | write (new) | the true size | Slice B |
| `function:foundry-tools:internal/checks:VerdictOf` | write | the ONE capture point | Slice B |
| `function:foundry-tools:internal/checks:AbsentVerdict` | write | an absence carries an empty list, not null | Slice B |
| `field:foundry-tools:internal/checks:StageAtom.Logs` (+2) | write (new) | the first copy outward | Slice C |
| `file:foundry-tools/internal/checks/stage.go` line 91 | write | `StageAtom{…}` gains the three | Slice C |
| `field:foundry-tools:AtomResult.Logs` (+2) | write (new) | the module's wire shape | Slice C |
| `file:foundry-tools/stage.go` line 187 | write | `AtomResult{…}` gains the three | Slice C |
| `function:foundry-tools:internal/checks:reasonFor` | read only | MUST NOT change — FR-008 | Slice B |

## Data model

No persisted shape. See `data-model.md` — the three fields are in-memory until F3 marshals
them and F4 stores them.

## Decision Log

| Decision | Resolution | Rationale | Provenance | Alternatives |
| :--- | :--- | :--- | :--- | :--- |
| Which atoms carry logs | all of them | "We don't trust absence as evidence" | Rob | failures only |
| Log cap | generous per-atom cap + `truncated` / `original_bytes` | keeps every atom's evidence, bounds a runaway, states the cut | Rob | unbounded; cap clean atoms only |
| Cap value | 1 MB | ~3x the largest atom measured (`cargo test`, 115 KB) | Default | 256 KB; 4 MB |
| **Split `Reason` or duplicate it** | **neither — capture from the raw output at `VerdictOf`** | `reasonFor` returns only `"<id>: PASS"` for a pass: **the output is already discarded** before `Reason` exists. Splitting is unavailable for exactly the atoms Rob's decision targets | **Claude, measured @5f2eeff** | split `Reason`; duplicate `Reason` |
| Where capture happens | `VerdictOf`, once | the single point every atom's output reaches, across ~40 call sites and all four results | Claude | at each `atoms_*.go` site; at the stage |
| Cap unit | bytes | a line count does not bound a runaway volume of text | Default | lines; both |
| Which end survives a cut | the tail | a failing tool's reason is at the END; its head is setup noise | Default | head; head+tail with a gap marker |
| Carried as | a list of lines | the consumer displays and filters per line (F5) | Default | one blob string |
| Empty output | an empty list, never null | FR-003 — "printed nothing" must be distinguishable from "field never set" | Default | null/omitted |
| `Reason` | byte-identical, untouched | things downstream read it by shape today; FR-008 | Claude | fold `Reason` into `logs` |
| Rune safety | cut on a rune boundary | the repo already learned this: a byte-sliced reason carrying box-drawing characters was refused by Postgres (ourea, 2026-09-10) | Claude | byte slice |

## Dependencies

- **Slice B** depends on **Slice A** (the capture function).
- **Slice C** depends on **Slice B** (the fields must exist before they can be copied).
- No cycles. Nothing here depends on F1 or F3; **F3 depends on all of this** — it marshals
  the shape this feature fills.

## Impact

| Slice | Impact (0–10) |
| :--- | :--- |
| A — the capture function | 6 — pure, holds every cap and boundary rule |
| B — Verdict carries the lines | 8 — the one capture point; where a pass stops losing its evidence |
| C — the two copies outward | 5 — mechanical, but the record is empty without it |

## Open & risk

- **No `[OPEN]` items.** The master-plan's single stated gap for F2 is closed on evidence
  (Decision Log row 4), not deferred.
- **Risk: `Verdict` is JSON-tagged and crosses a wire.** `logs` must not be `omitempty`, or an
  atom that printed nothing becomes indistinguishable from one that was never asked — the exact
  conflation FR-003 forbids. Pinned by a test.
- **Risk: memory, not storage.** A stage holds every atom's lines at once. Worst realistic case
  is ~40 atoms; only a pathological run approaches the cap on many of them at the same time.
  The master-plan measures the median atom at 82–89 bytes, so the common case is negligible —
  but the cap is per-atom, not per-stage, and this plan does not add a stage-wide bound. Stated
  rather than guarded: a stage-wide cap would have to choose which atom's evidence to drop,
  and that is a decision for whoever measures a real problem.
- **Risk: ~40 `atoms_*.go` call sites.** None of them change, because the capture is inside
  `VerdictOf`. That is the reason for capturing there and the thing to re-check if it moves.
- **`CannotRunVector` builds verdicts from a reason string, not from a run.** Those carry that
  string as their lines, which is correct — for an atom that could not run, the reason IS the
  evidence.

---
Definition of Ready (the gate — must pass, not vacuously):
- [x] every decision resolved + provenance-tagged (incl. defaults) — 11 rows, zero open
- [x] Contracts & Seams complete — 5 exposed surfaces with shapes; 3 consumed deps pinned at `foundry-tools@5f2eeff`
- [x] Resource-Reach field-level AND verified against the real repo — every pointer resolved, both copy sites cited by line
- [x] dependencies stated, no cycles — A → B → C; F3 → this
- [x] constitution check is real — below

## Constitution Check

| Principle | Check | Verdict |
| :--- | :--- | :--- |
| I — The Spec Is Law | Every point `decided`; the one inherited gap closed by measurement rather than invention. | PASS |
| II — Deferral Terminates | Six Defaults written binding with their alternatives named (cap value, unit, which end, list-vs-blob, empty-is-empty, rune safety). | PASS |
| III — Contracts Are Named | Both sides pinned: three struct shapes + the capture signature + the cap constant exposed; `VerdictOf`'s existing signature and `reasonFor`'s invariance consumed. | PASS |
| IV — Conformance Is Checkable | SC-001/002 are per-atom counts; SC-003 is a deliberate over-cap run; SC-006 is the existing suite unchanged. Falsifiable. | PASS |
| V — Verify Before Done | Acceptance requires the existing stage/verdict tests to be RUN and reported, since FR-008/FR-009 are invariance claims that only a run can support. | PASS |
