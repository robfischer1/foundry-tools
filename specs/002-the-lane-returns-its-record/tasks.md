---
description: "Forge work-chunks — binding, conflict-checked, executor-optimized"
---

# Tasks: The lane returns its run record

**Binding contract:** every task is binding spec. (Constitution I/II)

## Parallelization

- **Critical path:** T001 → T002. No `[P]`: T002 calls what T001 exposes.

| Lane | Tasks | Depends on | Distinct files |
| :--- | :--- | :--- | :--- |
| 1 | T001 | — | `stage.go`, `stage_test.go` |
| 1 | T002 | T001 | `gate.go`, `gate_test.go` |

## Work-chunks

### T001 — The record, marked and on one line · S · sequential

- **Serves:** plan.md Slice A. spec FR-002, FR-003, FR-004, FR-005.
- **Acceptance:**
  **Given** a `StageResult`,
  **When** `Record()` is called,
  **Then** it answers one string beginning with `ourea-run-record/1 ` followed by the marshalled
  object;
  **And** the string contains **no newline at all**, even when the stage's rendered log and the
  atoms' lines contain many — JSON escapes them, and a test with embedded newlines proves it;
  **And** the marshalled object carries `Stage`, `State`, `Lanes`, `Atoms`, `Omitted`,
  `Unreached` and `Log`, with each atom's `Logs` / `Truncated` / `OriginalBytes` intact;
  **And** the sentinel literal is `ourea-run-record/1`, pinned **byte for byte** by a test,
  because the reader lives in another repo and nothing else holds the two equal.
- **Exposes:** `function:foundry-tools:(*StageResult).Record` · `const:foundry-tools:runRecordSentinel` · both decided
- **Touches (RR):** write `function:foundry-tools:(*StageResult).Record` (new) · write
  `const:foundry-tools:runRecordSentinel` (new) · write `file:foundry-tools/stage_test.go`
- **State:** none.
- **Budget:** exactly one line, always. A record containing a newline is a failed acceptance,
  not a cosmetic issue — the reader scans line by line and would see a fragment.
- **Decisions-slice:** marshal to a string (Claude) · sentinel prefix (**Rob**) · reuse
  `StageResult` rather than a gate-specific shape (Claude).
- **Conflicts-with:** none.
- **Open:** none.
- **Size basis:** one pass — a marshal and a prefix; the newline-containment property is the
  only thing that needs proving and it needs one test.

### T002 — `Gate` hands it back instead of failing · M · sequential

- **Serves:** plan.md Slice B. spec FR-001, FR-006, FR-007, FR-008, FR-009.
- **Acceptance:**
  **Given** the gate or mutation lane,
  **When** it finishes grading,
  **Then** it returns the record string and **does not** call `settle`;
  **And** the record's `State` equals the state the same run would previously have exited with,
  for clean, findings **and** could-not-run (spec FR-009) — the verdict moves channel, not value;
  **And** a run that cannot produce a record at all returns an error instead (spec FR-007);
  **And** exactly one record is returned per run (spec FR-006);
  **And** `(*StageResult).Exit` is **not edited**, so the developer's commit stage still fails
  with a non-zero exit and a readable log (spec FR-008);
  **And** `Gate`'s parameters are unchanged, so the door's argv and infra's config need no edit.
- **Exposes:** `stdout:lane-run-record` · `function:foundry-tools:Gate` returning
  `(string, error)` · decided
- **Touches (RR):** write `function:foundry-tools:Gate` · read
  `function:foundry-tools:(*StageResult).Exit` (**must not change**) · read
  `function:foundry-tools:settle` (no longer called by `Gate`) · write
  `file:foundry-tools/gate_test.go`
- **State:** the lane's verdict moves from the exit code to the returned record. **After this,
  the lane's exit code carries nothing** — which is why the door's guard lands first.
- **Budget:** zero edits to `Exit` and zero edits to `settle`. Either is evidence the developer
  path was disturbed.
- **Decisions-slice:** `Exit` stays (Default) · error only when no record can be produced
  (Default) · no summary line beside the record (**Rob**) · land after the door's guard (Claude).
- **Conflicts-with:** T001 (consumes it). Sequential.
- **Open:** none.
- **Size basis:** two passes — the change is a return type and a few lines, but verdict parity
  across three states is its own pass and is the claim that matters.

---
Done-when (the gate):
- [x] every task: Serves + Acceptance + field-level Touches + Decisions-slice + size
- [x] no [P] claimed; dependency stated
- [x] critical path identified; no cycles
- [x] every Exposes shape traces to plan.md Contracts & Seams
- [x] Budget falsifiable on both (one line always; zero edits to Exit/settle)
