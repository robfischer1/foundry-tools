---
title: "The lane returns its run record"
spec: "./spec.md"
constitution: "../../.specify/memory/constitution.md"
status: draft
---

# The lane returns its run record — Design Plan

> **Binding contract.** Every item is `decided` or `[OPEN]`. No advisory tier. (Constitution I/II)

## Summary

`Gate` stops calling `settle`, which ran a container whose non-zero exit was the verdict, and
instead builds the same `StageResult` the commit stage builds, marshals it, and returns it as a
sentinel-prefixed string. The dagger CLI prints a returned String on stdout, so the record
reaches the door's pod log where F1's reader already looks for it. `StageResult.Exit` is
untouched: the developer's `just check` still fails loudly.

## Architecture

| Path | Change | Responsibility |
| :--- | :--- | :--- |
| `stage.go` | write | `runRecordSentinel`; `(*StageResult).Record()` — marshal + prefix |
| `stage_test.go` | write | one line, the mark, round-trip, newline containment |
| `gate.go` | write | `Gate` returns `(string, error)`; builds the StageResult and returns its Record |
| `gate_test.go` | write | verdict parity across all three states; one record per run |

**`Gate` gains nothing it did not already have.** `gradeTree` already answers `[]checks.Verdict`;
`checks.SettleStage(lane, vector)` and `stageResult(...)` are exactly what `Check` and `Push`
already use to turn a vector into a `StageResult`. This feature reuses that path rather than
inventing a second shape, which is what keeps FR-009 (the verdict must not move) cheap to prove.

**The lane's argv does not change**, so neither ourea nor infra is touched by this: the door
calls `gate --tree=… --pin=… --base=… --stage=…` and those parameters are untouched. Only the
return type moves, and a dagger function's return type is not part of its call.

## Contracts & Seams

### Exposes

| Surface | Signature / shape | State |
| :--- | :--- | :--- |
| `stdout:lane-run-record` | one line: `ourea-run-record/1 {json}` where the object is the marshalled `StageResult` — `{Stage, State, Lanes, Atoms, Omitted, Unreached, Log}`, each atom carrying `Logs`/`Truncated`/`OriginalBytes` | decided |
| `function:foundry-tools:Gate` | `Gate(ctx, tree, pin string, base, stage string) (string, error)` — was `error` | decided |
| `function:foundry-tools:(*StageResult).Record` | `Record() (string, error)` | decided |
| `const:foundry-tools:runRecordSentinel` | `"ourea-run-record/1"` — **must equal ourea's `recordSentinel` byte for byte** | decided |

### Consumes / Requires

| Dependency | Contract relied on | Pin |
| :--- | :--- | :--- |
| `function:foundry-tools:internal/checks:SettleStage` | `SettleStage(name string, vs []Verdict, unreached ...string) Stage` | foundry-tools@a49be7c |
| `function:foundry-tools:stageResult` | `stageResult(st checks.Stage) *StageResult` | foundry-tools@a49be7c |
| `function:foundry-tools:gradeTree` | `([]checks.Verdict, int)` — unchanged | foundry-tools@a49be7c |
| `function:ourea:internal/gatejob:FindRunRecord` | keys on `ourea-run-record/1`, refuses two, refuses unparseable | ourea@367100c |
| `dagger:cli` | a function returning String prints it on stdout; value XOR error | dagger@v0.21.9 |

### Resource-Reach — verified against the real repo @ a49be7c

| RR pointer | Access | Role | Used by |
| :--- | :--- | :--- | :--- |
| `function:foundry-tools:Gate` | write | the return type and the body | Slice B |
| `function:foundry-tools:(*StageResult).Record` | write (new) | marshal + prefix | Slice A |
| `const:foundry-tools:runRecordSentinel` | write (new) | the agreed mark | Slice A |
| `function:foundry-tools:(*StageResult).Exit` | read only | **MUST NOT change** — the developer's path | Slice B |
| `function:foundry-tools:settle` | read only | no longer called by `Gate`; still used by `Exit` | Slice B |

## Decision Log

| Decision | Resolution | Rationale | Provenance | Alternatives |
| :--- | :--- | :--- | :--- | :--- |
| How the record reaches the door | the module marshals JSON and returns a **string** | `dagger call --json` on a module object returns an object REFERENCE, not data — only leaf selections serialise | Claude (master-plan, proven in a throwaway module) | return the object and use `--json` |
| How the record is marked | sentinel prefix `ourea-run-record/1` | the door reads a merged stdout+stderr stream; a positional rule would key on what the GRADED tree printed | **Rob** | a bare object found positionally |
| `StageResult.Exit` | **stays, untouched** | a human at a terminal wants a failing exit and readable prose; the door wants a record. Different consumers, different needs — folding them serves neither | Default (closes the master-plan's stated gap) | fold it into the record path |
| When `Gate` returns an error | only when no record can be produced | "could not grade" is a FACT about the run and belongs in the record with state 2; an error is the absence of an answer | Default | error on every non-clean state |
| A human summary line beside the record | **none** | Rob declined a second independent signal when offered one; the rendered log rides inside the record | **Rob** | print the summary to stderr too |
| The record's shape | the existing `StageResult` | `Check`/`Push` already build it; reusing it is what makes "the verdict did not move" provable | Claude | a new gate-specific shape |
| Ordering | this lands AFTER the door's guard | once this lands the lane exits 0 always; without the guard a lost record reads as clean | Claude | land together (impossible — two repos) |

## Dependencies

- Slice B depends on Slice A.
- **MUST land after ourea's `feat/a-record-lane-that-said-nothing-could-not-run`.** That is not
  a preference: between this landing and that one, a lost record settles a red run green.
- The infra switch (`record_lanes`) lands **after this**, closing the window.

## Impact

| Slice | Impact (0–10) |
| :--- | :--- |
| A — `Record()` and the sentinel | 6 — small, and it is the contract both repos share |
| B — `Gate` returns it | 9 — the flip; everything downstream becomes possible and the exit code stops meaning anything |

## Open & risk

- **No `[OPEN]` items.** The master-plan's stated gap for F3 — whether `StageResult.Exit` stays
  — is closed as a binding Default above.
- **Risk, stated and accepted: the pod log becomes less readable to a human.** Today a failing
  lane prints a summary and each red atom's reason as prose. After this it prints one long JSON
  line. Nothing is LOST — the rendered log is inside the record, and the reader-facing feature
  serves it — but for the window before that feature ships, reading a lane by eye is worse. Rob
  declined the mitigating summary line when offered it.
- **Risk: the sentinel is duplicated across two repos** and nothing but a test holds them equal.
  They are separate Go modules with no shared package, so a shared constant is not available.
  The contract file in ourea's spec is the written record; a test here pins the literal.
- **Risk: the record can be large.** The master-plan measured 69–306 KB. It is one line, and the
  door reads 400 lines of tail unclipped, so it fits — but a runaway tree with many capped atoms
  could approach 40 MB in the worst theoretical case. Not guarded here: the per-atom cap is the
  bound that exists, and a stage-wide cap would have to choose whose evidence to drop.

---
Definition of Ready:
- [x] every decision resolved + provenance-tagged — 7 rows, zero open
- [x] Contracts & Seams complete — 4 exposed, 5 consumed, pinned at `foundry-tools@a49be7c` / `ourea@367100c` / `dagger@v0.21.9`
- [x] Resource-Reach field-level, verified against the real repo
- [x] dependencies stated, no cycles — A → B; this → the door's guard; the switch → this
- [x] constitution check is real — below

## Constitution Check

| Principle | Check | Verdict |
| :--- | :--- | :--- |
| I — Spec Is Law | Every point decided; the inherited gap on `Exit` closed explicitly rather than by silence. | PASS |
| II — Deferral Terminates | Four Defaults binding with alternatives named; two decisions are Rob's and cited as his. | PASS |
| III — Contracts Named | The wire shape is given byte-level (the sentinel, one line, the object's fields), and the consumed reader is pinned by the function that reads it. | PASS |
| IV — Conformance Checkable | SC-002 is verdict parity across three states against the pre-change behaviour; SC-003 is a one-line assertion on content containing newlines. | PASS |
| V — Verify Before Done | Acceptance requires the developer path to be RUN against a passing and a failing tree, not asserted. | PASS |
