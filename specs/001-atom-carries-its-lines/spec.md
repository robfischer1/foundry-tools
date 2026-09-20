---
title: "The atom carries its own lines"
---
# Feature Specification: The atom carries its own lines

**Status**: Draft | **Input**: "Progressive Disclosure for CI Logs — Master-plan" · F2

> **Gap-protocol (Constitution I).** Every unresolved point is marked `[OPEN: question]`.
> Defaults chosen here are logged in Assumptions as Default-provenance decisions and are
> BINDING on the plan. The WHAT lives here; the HOW lives in plan.md.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The evidence lives with the verdict (Priority: P1)

Today a run's verdict says *which* atom found something, and the lines that atom actually
printed live only in a shared transcript that has to be read by eye and grepped by hand.
After this feature, each atom's own output is attached to that atom's result, so "what did
`go:vet` say" is a field rather than a search.

**Why this priority**: Every later feature reads the atoms' lines. If they are not attached
here, the record ships with empty evidence and the disclosure has nothing to disclose.

**Independent Test**: Run a stage over a tree with a failing atom and a passing atom, and
confirm each result carries the lines that atom produced, and only that atom's lines.

**Acceptance Scenarios**:
1. **Given** an atom that printed output and found something, **When** its result is built,
   **Then** that result carries that atom's own lines.
2. **Given** two atoms in one stage, **When** their results are built, **Then** neither
   carries the other's lines.
3. **Given** an atom that could not run, **When** its result is built, **Then** it carries
   whatever the attempt printed — the reason it could not run is usually in there.

### User Story 2 - A passing atom's lines are kept too (Priority: P1)

An atom that passed carries its lines exactly as one that failed does. Today a passing atom's
output is discarded at the moment its verdict is built, so "it passed" is all that survives.

**Why this priority**: Rob's decision, verbatim: *"We don't trust absence as evidence."* A
record that keeps evidence only for failures cannot answer "did this atom actually look?" —
which is the same conflation the ABSENT result already exists to prevent, one shelf up.

**Independent Test**: Run a stage where every atom passes and confirm the results carry the
atoms' output rather than only their pass lines.

**Acceptance Scenarios**:
1. **Given** an atom that passed with output, **When** its result is built, **Then** its lines
   are kept, not discarded.
2. **Given** an atom that passed silently, **When** its result is built, **Then** it carries an
   empty list of lines — explicitly empty, distinguishable from a field that was never set.
3. **Given** an atom that declared itself ABSENT, **When** its result is built, **Then** it
   carries the lines it printed saying so.

### User Story 3 - A cut is stated, never silent (Priority: P1)

One runaway atom must not be able to carry an unbounded amount of text. Above a cap, the
result keeps the cap's worth and **says** it was cut, along with how much there really was.

**Why this priority**: This is the whole plan's thesis applied to itself. The bug that started
this work was a log line silently truncated mid-path with nothing saying so; a feature that
reintroduces silent truncation one layer up would be self-defeating.

**Independent Test**: Give one atom more output than the cap and confirm the result carries the
cap's worth, a flag saying it was cut, and the true original size.

**Acceptance Scenarios**:
1. **Given** an atom whose output exceeds the cap, **When** its result is built, **Then** the
   result carries at most the cap's worth of lines, states that it was truncated, and states
   the original size in bytes.
2. **Given** an atom whose output is under the cap, **When** its result is built, **Then** the
   result does not claim truncation and reports the real size.
3. **Given** a truncated result, **When** anyone reads it, **Then** the truncation is
   discoverable from the result alone, without comparing it to anything else.

### Edge Cases
- What happens to an atom's existing verdict text, which today already contains a copy of the
  output for failures? It must not change — things downstream read it by shape today.
- What happens when output is not valid text, or contains partial multi-byte characters at the
  cap boundary? A cut must not sever a character in half.
- What happens to the stage's overall worst-state answer? Nothing — this feature moves
  evidence, it does not grade differently.
- What happens to an atom the stage never reached? It has no lines because it never ran, and
  that is different from running and printing nothing.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every atom result MUST carry the lines that atom itself produced.
- **FR-002**: An atom that passed MUST carry its lines on the same terms as one that failed —
  no state may cause the evidence to be discarded.
- **FR-003**: An atom that produced no output MUST carry an explicitly empty list, never an
  absent or null field.
- **FR-004**: An atom's lines MUST be bounded by a per-atom cap.
- **FR-005**: When the cap bites, the result MUST state that it was truncated AND the original
  size in bytes, so the cut is legible from the result alone.
- **FR-006**: When the cap does not bite, the result MUST NOT claim truncation, and the
  reported size MUST be the real one.
- **FR-007**: A truncation MUST NOT sever a multi-byte character.
- **FR-008**: The atom's existing verdict text MUST be unchanged in shape and content — this
  feature adds fields beside it and rewrites none.
- **FR-009**: The stage's overall state and the set of atoms that ran MUST be unchanged.
- **FR-010**: An atom's lines MUST belong to that atom alone — no shared or concatenated buffer.

### Key Entities

- **Atom result**: one atom's line in a stage's answer. Today: which atom, its group, its
  state, its result word, and its verdict text. This feature adds its own lines, whether they
  were cut, and how much there originally was.
- **Atom output**: the raw text an atom's execution produced. It exists today and is already
  handed to the code that builds the verdict — it is simply dropped for passing atoms.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of atoms that ran in a stage carry their own lines, across every state —
  pass, findings, could-not-run, and absent.
- **SC-002**: 0 atoms carry another atom's lines.
- **SC-003**: No single atom contributes more than the cap, measured over a stage whose atom
  deliberately exceeds it.
- **SC-004**: 100% of truncated results state both the truncation and the original byte count.
- **SC-005**: The median atom's carried evidence stays small — the master-plan measured 82–89
  bytes across Go, Python and Rust — so the common case costs almost nothing.
- **SC-006**: Every existing stage and verdict test passes unchanged, proving the verdict text
  and the stage's grading did not move.

## Assumptions

Default-provenance decisions taken here. Each is BINDING on the plan.

- **A-001 (measured, not assumed — this closes the master-plan's stated gap for F2)**: The
  master-plan asked whether the atom's lines should *split* the existing verdict text or
  *duplicate* it. Measured against the live code: the answer is **neither**. The verdict text
  is built by a function that receives the raw output and, for a passing atom, returns only
  `"<id>: PASS"` — **the output is already discarded at that point**. Splitting is therefore
  impossible for exactly the atoms Rob's "all atoms carry logs" decision cares most about.
  The lines are taken from the raw output where it is still in hand, and the verdict text is
  left byte-identical (FR-008).
- **A-002 (Rob)**: Every atom carries lines, passing ones included. "We don't trust absence as
  evidence."
- **A-003 (Default, from the master-plan)**: The per-atom cap is 1 MB — roughly three times the
  largest atom measured (`cargo test`, 115 KB).
- **A-004 (Default)**: The cap is measured in **bytes of output**, not in lines, because the
  failure it guards against is a runaway volume of text and a line count does not bound that.
- **A-005 (Default)**: When the cap bites, the lines kept are the **last** ones, not the first.
  A failing tool's reason for failing is at the end of its output; its start is setup noise.
- **A-006 (Default)**: Lines are carried as a list of lines rather than one blob, because the
  consumer of this evidence displays and filters it per line.

## Non-goals

- **Changing what the atoms check, or how a stage grades.** This moves evidence only.
- **Emitting or returning the record.** That is F3; this feature stops at the in-memory result.
- **Storing the lines.** That is F4.
- **Serving them.** That is F5.
- **Changing the existing verdict text**, which already carries a copy of the output for
  failures and is read by shape today.
