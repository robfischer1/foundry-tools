---
description: "Forge work-chunks — binding, conflict-checked, executor-optimized"
---

# Tasks: The atom carries its own lines

**Input:** plan.md (required) · spec.md · the Contracts & Seams (the dependency edges).
**Binding contract:** every task is binding spec. The executor follows it and does NOT
use judgment outside items marked `[OPEN]`. A needed deviation is surfaced back, not
decided locally. (Constitution I/II)

## Parallelization — conflict-checked (NOT optimistic)

- **Critical path:** T001 → T002 → T003. Three chunks, strictly sequential.
- No `[P]` is claimed. T002 and T003 both write `internal/checks/`, and T003 cannot compile
  until T002's fields exist.

| Lane | Tasks | Depends on | Distinct files (conflict-verified) |
| :--- | :--- | :--- | :--- |
| 1 | T001 | — | `internal/checks/logs.go`, `internal/checks/logs_test.go` |
| 1 | T002 | T001 | `internal/checks/verdict.go`, `internal/checks/verdict_test.go` |
| 1 | T003 | T002 | `internal/checks/stage.go`, `stage.go`, `stage_test.go` |

## Work-chunks

### T001 — The cap, the tail, and a cut that does not sever a rune · S · sequential

- **Serves:** plan.md Slice A. spec FR-003, FR-004, FR-005, FR-006, FR-007.
- **Acceptance:**
  **Given** an atom's raw output,
  **When** `CaptureLogs(output)` is called,
  **Then** it answers the lines, whether it cut, and the **true original byte count**;
  **And** output at or under `AtomLogCap` (1 MB) is carried whole with `truncated` false and
  `originalBytes` equal to its real length;
  **And** output over the cap carries at most the cap's worth of its **LAST** bytes, with
  `truncated` true and `originalBytes` still the **full pre-cut** size;
  **And** empty output answers an **empty, non-nil** slice — `[]string{}`, never `nil` — with
  `truncated` false and `originalBytes` 0;
  **And** a cut never lands inside a multi-byte character;
  **And** the function performs no I/O and is deterministic.
- **Exposes:**
  - `function:foundry-tools:internal/checks:CaptureLogs` — `CaptureLogs(output string) ([]string, bool, int)` · decided
  - `const:foundry-tools:internal/checks:AtomLogCap` — `1 << 20` · decided
- **Touches (RR, field-level):**
  write `file:foundry-tools/internal/checks/logs.go` (new) ·
  write `file:foundry-tools/internal/checks/logs_test.go` (new)
- **State:** none — pure.
- **Budget:** the median atom is 82–89 bytes (master-plan, measured across Go/Python/Rust), so
  the common path must not copy or scan more than the output itself.
- **Decisions-slice:** cap 1 MB (Default) · cap in **bytes** not lines (Default) · keep the
  **tail** (Default) · a **list** not a blob (Default) · empty is `[]` not nil (Default) ·
  rune-safe cut (Claude — Postgres refused a byte-sliced reason carrying box-drawing
  characters, ourea 2026-09-10).
- **Conflicts-with:** none — both files are new.
- **Open:** none.
- **Size basis:** one pass — one pure function; every boundary is enumerated in Acceptance.

### T002 — The verdict captures the lines, and `Reason` does not move · M · sequential

- **Serves:** plan.md Slice B. spec FR-001, FR-002, FR-008.
- **Acceptance:**
  **Given** any atom result built by `VerdictOf(a, exit, output)`,
  **When** the verdict is built,
  **Then** `Logs`, `Truncated` and `OriginalBytes` are populated from `CaptureLogs(output)`;
  **And** this happens for **every** state — pass, findings, cannot-run **and** absent — so a
  passing atom stops losing its evidence (spec FR-002, Rob: "we don't trust absence as
  evidence");
  **And** `Reason` is **byte-identical** to what it is today for every state, and `reasonFor`
  is not edited at all (spec FR-008);
  **And** `AbsentVerdict`, which receives no output, carries an **empty non-nil** `Logs`;
  **And** the JSON tag for `logs` is **not** `omitempty` — an atom that printed nothing must
  stay distinguishable from a field that was never set (spec FR-003);
  **And** none of the ~40 `atoms_*.go` call sites change, because the capture is inside
  `VerdictOf`.
- **Exposes:** `type:foundry-tools:internal/checks:Verdict` gains `logs []string` /
  `truncated bool` / `original_bytes int` · decided
- **Touches (RR, field-level):**
  write `field:foundry-tools:internal/checks:Verdict.Logs` ·
  write `field:foundry-tools:internal/checks:Verdict.Truncated` ·
  write `field:foundry-tools:internal/checks:Verdict.OriginalBytes` ·
  write `function:foundry-tools:internal/checks:VerdictOf` ·
  write `function:foundry-tools:internal/checks:AbsentVerdict` ·
  read `function:foundry-tools:internal/checks:reasonFor` (**must not change**) ·
  write `file:foundry-tools/internal/checks/verdict_test.go`
- **State:** none new — the existing four results are unchanged.
- **Budget:** **zero edits under `atoms_*.go`.** A diff that touches one is a failed
  acceptance, not a detail: it would mean the capture point moved off the single seam.
- **Decisions-slice:** all atoms carry logs (Rob) · capture at `VerdictOf`, once (Claude) ·
  `Reason` untouched (Claude) · `logs` never `omitempty` (Default).
- **Conflicts-with:** T003 shares the `internal/checks` package. Sequential.
- **Open:** none.
- **Size basis:** two passes — the capture is one line, but the invariance claim (`Reason`
  byte-identical across four states) needs its own pass and its own test.

### T003 — Both copies outward carry them · S · sequential

- **Serves:** plan.md Slice C. spec FR-009, FR-010.
- **Acceptance:**
  **Given** a verdict carrying lines,
  **When** the stage copies it to `StageAtom` (`internal/checks/stage.go:91`) and the module
  copies that to `AtomResult` (`stage.go:187`),
  **Then** all three fields survive both hops unchanged;
  **And** each atom's lines belong to that atom alone — no shared or concatenated buffer
  (spec FR-010);
  **And** the stage's `State`, `Lanes`, `Ran`, `Omitted`, `Unreached` and `Log` are unchanged
  (spec FR-009);
  **And** the whole existing stage and verdict suite passes untouched (spec SC-006).
- **Exposes:** `type:foundry-tools:AtomResult` gains the three fields — **this is the shape F3
  marshals** · decided
- **Touches (RR, field-level):**
  write `field:foundry-tools:internal/checks:StageAtom.Logs` (+`Truncated`, +`OriginalBytes`) ·
  write `file:foundry-tools/internal/checks/stage.go` (the `StageAtom{…}` at line 91) ·
  write `field:foundry-tools:AtomResult.Logs` (+`Truncated`, +`OriginalBytes`) ·
  write `file:foundry-tools/stage.go` (the `AtomResult{…}` at line 187) ·
  write `file:foundry-tools/stage_test.go`
- **State:** none.
- **Budget:** the existing suite must pass **unchanged** — no test edited to accommodate a new
  field. An edit to an existing assertion is evidence FR-009 was broken.
- **Decisions-slice:** the record needs the fields at the wire shape, so a field that stops at
  the first copy is a field F3 never sees (Claude).
- **Conflicts-with:** T002 (same package). Sequential.
- **Open:** none.
- **Size basis:** one pass — two struct literals and two type definitions; the risk is
  forgetting one of the two copies, which a round-trip test pins.

---
Done-when (the gate):
- [x] every task: Serves + Acceptance + field-level Touches + Decisions-slice + size
- [x] every [P] verified conflict-free — none claimed; T002/T003 share a package and say so
- [x] critical path identified (T001 → T002 → T003); no dependency cycles
- [x] every Exposes shape traces to plan.md Contracts & Seams — all five
- [x] State + Budget present where load-bearing — T002 and T003 both carry a **falsifiable**
      budget (zero `atoms_*.go` edits; zero existing-test edits), not a latency guess
