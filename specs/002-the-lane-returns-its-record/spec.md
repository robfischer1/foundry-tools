---
title: "The lane returns its run record"
---
# Feature Specification: The lane returns its run record

**Status**: Draft | **Input**: "Progressive Disclosure for CI Logs — Master-plan" · F3, lane half

> **Gap-protocol (Constitution I).** Every unresolved point is `[OPEN: question]`; Defaults are
> logged in Assumptions and are BINDING on the plan.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The lane hands back what it found (Priority: P1)

A gate run currently ends by exiting with a number, and everything it learned — which atoms
ran, what each printed, what was never reached — is flattened into that one integer plus prose
in a log. This makes the run hand back its whole structured account of itself instead, on its
own output, for the door to settle from.

**Why this priority**: This is the flip the whole plan builds toward. Every feature before it
exists to make this safe, and every feature after it reads what this produces.

**Independent Test**: Run the lane over a tree and confirm its output carries exactly one
record, that the record parses, and that the record's verdict matches the verdict the same run
would have exited with.

**Acceptance Scenarios**:
1. **Given** a tree with no findings, **When** the lane runs, **Then** its output carries a
   record stating clean.
2. **Given** a tree with findings, **When** the lane runs, **Then** its output carries a record
   stating findings — and the run does **not** signal failure any other way, because it no
   longer can.
3. **Given** a run that could not grade at all, **When** the lane runs, **Then** its output
   carries a record stating could-not-run, with the reason inside it.
4. **Given** any of the above, **When** the door reads the output, **Then** it finds exactly
   one record and settles from it.

### User Story 2 - The record is findable in a merged stream (Priority: P1)

The record is not alone on the lane's output. Progress narration, the graded repository's own
tool output, and the record all arrive interleaved. The record must be locatable without
depending on what anything else printed.

**Why this priority**: The door reads a transport that merges the run's result with its
narration. A record that can only be found by position is a record that can be forged or lost
by whatever the graded tree happened to print.

**Acceptance Scenarios**:
1. **Given** a run whose graded tree printed arbitrary text, including JSON, **When** the door
   looks for the record, **Then** it finds the lane's record and not the tree's output.
2. **Given** a record, **When** it is written, **Then** it occupies exactly one line, whatever
   is inside it.
3. **Given** a record, **When** it is written, **Then** it is marked so the reader keys on the
   mark rather than on position.

### User Story 3 - The developer's own pre-commit path is untouched (Priority: P1)

A developer running the commit stage by hand still gets a non-zero exit and a readable log when
something is found. Only the door's lane changes.

**Acceptance Scenarios**:
1. **Given** a developer runs the commit stage, **When** it finds something, **Then** it still
   fails with a non-zero exit and prints the human-readable log, exactly as before.
2. **Given** the commit stage, **When** it passes, **Then** it still exits 0 and behaves as
   before.

### Edge Cases
- A run whose record cannot be produced at all: it must still fail the way an ungradeable run
  fails, rather than returning an empty or partial record.
- A record containing text with newlines in it: the record still occupies one line.
- A record large enough to be awkward: it is still one line, and the reader's bounds are the
  reader's problem, already solved.
- The graded tree printing something that looks like a record: the mark is what distinguishes
  them, and a second mark makes the reader refuse both rather than choose.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The door's lane MUST hand back its run's structured record rather than signalling
  its verdict by failing.
- **FR-002**: The record MUST carry the run's overall verdict in the same three states the door
  already settles.
- **FR-003**: The record MUST carry each atom that ran, each atom omitted, and what was never
  reached — the account the previous feature filled in.
- **FR-004**: The record MUST occupy exactly one line of output.
- **FR-005**: The record MUST be preceded by an agreed mark that the reader keys on, and that
  mark MUST be exactly the one the door already reads.
- **FR-006**: The lane MUST emit **at most one** record per run.
- **FR-007**: A run that cannot produce a record at all MUST fail rather than return an empty or
  partial one.
- **FR-008**: The developer-facing commit stage MUST keep its existing failing behaviour and its
  human-readable log.
- **FR-009**: The record's verdict MUST equal the verdict the same run would previously have
  exited with — this feature moves the channel, it does not re-grade.

### Key Entities

- **Run record**: the stage's whole answer — its name, its verdict, the lanes found, the atoms
  that ran with their own lines, the atoms omitted, what was unreached, and the rendered log.
- **The mark**: the agreed token that makes the record findable in a merged stream.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of lane runs produce exactly one parseable record.
- **SC-002**: The record's verdict matches the pre-change verdict for 100% of runs, across all
  three states.
- **SC-003**: 0 records span more than one line, including records whose contents contain
  newlines.
- **SC-004**: 100% of records carry the mark the door reads, byte for byte.
- **SC-005**: The developer's commit stage behaves identically — verified by running it against
  a passing and a failing tree.

## Assumptions

- **A-001 (Rob)**: The mark is a sentinel prefix, `ourea-run-record/1`, not a positional rule.
  Ruled 2026-09-20 on the reader's side; this is the writer honouring the same contract.
- **A-002 (measured)**: The record is one line because it is JSON, and JSON escapes the newlines
  inside its strings. The rendered log and each atom's lines therefore travel intact without
  breaking the line.
- **A-003 (Default)**: The developer's commit-stage exit path **stays exactly as it is**. The
  master-plan asked whether it should be folded in; it should not. It is a different consumer
  with a different need — a human at a terminal wants a failing exit and readable prose, and the
  door wants a structured record. Only the door's lane changes.
- **A-004 (Default)**: The lane returns an error **only** when it cannot produce a record at all.
  Every gradeable outcome — including "could not grade this tree" — is a record with a verdict
  inside it, because that is a fact about the run rather than a failure to report one.
- **A-005 (Default)**: No human-readable summary line is added alongside the record. Rob
  declined a second independent signal when offered one; the rendered log rides inside the
  record and the reader-facing feature serves it.

## Non-goals

- **Changing what the atoms check or how a run is graded.** The verdict must not move (FR-009).
- **Storing or serving the record.** Those are the next two features.
- **Changing the developer's commit stage.**
- **Switching the door's requirement on.** That is a configuration change and lands last.
