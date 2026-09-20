# Data model — The atom carries its own lines

## Persisted shape: NONE

The planning context's Shared-data-model slice reads, verbatim:

> **Shared-data-model slice:** none yet — the fields are in-memory until F4.

Reconciled as written. This feature adds no table, no column, no migration. It fills a shape;
F3 marshals it, F4 stores it, F5 serves it.

## In-flight shape

Three fields, added to the **same three structs**, because the atom's result is copied twice on
its way out and a field that stops at the first copy is a field the record never sees.

| Field | Type | Required | Meaning |
| :--- | :--- | :--- | :--- |
| `logs` | `[]string` | **yes** | The lines the atom itself produced. **Never null** — an atom that printed nothing carries `[]`. |
| `truncated` | `bool` | yes | Whether the cap bit. |
| `original_bytes` | `int` | yes | The size of the output **before** any cut — the real number, always, whether or not it was cut. |

### Where they live

| Struct | Package | Role |
| :--- | :--- | :--- |
| `Verdict` | `internal/checks` | built at `VerdictOf` — **the capture point** |
| `StageAtom` | `internal/checks` | the stage's own line per atom (copied at `stage.go:91`) |
| `AtomResult` | root module | the module's wire shape (copied at `stage.go:187`) |

### The invariant that makes them trustworthy

`original_bytes` is **the true size, not the carried size**. When `truncated` is false the two
coincide; when it is true they must not, and the difference is exactly what was dropped. A
reader can therefore always answer "how much am I not seeing" from the result alone — which is
the property the Loki truncation bug lacked and the reason this plan exists.

## State transitions

None. The three fields are set once, where the verdict is built, and are copied unchanged
thereafter.

## Relationships

`Stage` (1) → `StageAtom` (0..n), each with its own `logs`. **No shared buffer** — spec FR-010.
An atom listed in `Unreached` has no result and therefore no `logs`, which is different from an
atom that ran and printed nothing (`logs: []`).
