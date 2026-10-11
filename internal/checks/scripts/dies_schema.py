# SPDX-FileCopyrightText: 2026 Rob Fischer
#
# SPDX-License-Identifier: Apache-2.0
"""Validate foundry-dies' slag schemas (v1, v3 and later) and every fleet record against its own.

Since slag-by-kind F8 it also holds every catalog entry, fleet/stars/<name>.json, to
schema/fleet-star.schema.json.

Two halves. Both the published slag-schema die (schema/slag.schema.json, v1)
and the v3 schema are checked for being well-formed Draft 2020-12 documents
whose `required` names only properties they define — a schema that requires a
key it never describes validates nothing about that key. Then every
`fleet/stars/*/slag.json` is validated against the schema its own `$schema`
names (v3 or v4), plus the naming invariant the schema itself cannot express: `meta.name` agrees with the record's own
directory.

THE RECORDS ARE slag.json, NOT <name>.slag. This used to glob
`fleet/stars/*/*.slag` against the v2 schema; no such file exists (v3 replaced
v1 in place at slag.json), so the half matched zero records and passed on
nothing. The record count is now part of the contract: a tree with schema/ but
no records to validate is CANNOT RUN, because zero records validated is not the
same fact as every record valid.

THE EXIT LADDER IS THREE-STATE, AND IT WAS TWO. 0 is clean, 1 is a finding, 2
is CANNOT RUN. Every fault used to leave by `sys.exit("...")` — which is exit
1 with the message on stderr — or by an uncaught raise, whose traceback also
exits 1. So a schema file that could not be read and a record that failed
validation were the same code, and the dies lane reads 1 as findings: "the
check did not run" was reported as "the check found something", in the one
script that should know better. Noticed by Fox32, 2026-09-25, and verified
before it was changed.

The instrument's own faults — a schema file missing, unreadable or not JSON —
are 2. Everything the schema and records are found to be wrong about is 1.
"""

import json
import sys
from pathlib import Path

from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError


def load_schema(path: str) -> dict:
    """Read one schema file, or leave with 2 — this is the instrument, not the subject.

    Args:
        path: the schema's path, relative to the repository root.

    Returns:
        The parsed schema document.

    """
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"::error::could not read {path}: {exc}", file=sys.stderr)
        sys.exit(2)


def well_formed(schema: dict, path: str) -> None:
    """Refuse a schema that is not a Draft 2020-12 schema, as a FINDING.

    A malformed schema is something wrong with foundry-dies, which is what this
    atom exists to find — so it is 1, not 2. It used to arrive as a traceback,
    which exits 1 by accident rather than on purpose.

    Args:
        schema: the parsed schema document.
        path: its path, for the message.

    """
    try:
        Draft202012Validator.check_schema(schema)
    except SchemaError as exc:
        sys.exit(f"::error file={path}::not a valid Draft 2020-12 schema: {exc}")


def check_schema_file(path: str) -> dict:
    """Load one schema, require it well formed, and require `required` within `properties`.

    Args:
        path: the schema's path, relative to the repository root.

    Returns:
        The parsed schema document.

    """
    doc = load_schema(path)
    well_formed(doc, path)
    props = set(doc.get("properties", {}))
    missing = [k for k in doc.get("required", []) if k not in props]
    if missing:
        sys.exit(f"::error file={path}::required names properties that are not defined: {missing}")
    print(f"valid Draft 2020-12 schema - {doc.get('$id')}")
    print(f"{len(doc.get('required', []))} required keys, all defined in properties")
    return doc


SCHEMA = "schema/slag-v3.schema.json"
# THE SCHEMA A RECORD IS HELD TO IS THE ONE IT NAMES. A record carries `$schema`, and its suffix
# (/slag-v3.schema.json or /slag-v4.schema.json) says which file of schema/ is its law; v3 is
# required (it is the one every record carried until the flip), a later version is checked when its
# file exists, and a record that names a version whose file is absent cannot be judged: 2.
schemas = {path: check_schema_file(path) for path in ("schema/slag.schema.json", SCHEMA)}
for later in sorted(Path("schema").glob("slag-v[0-9]*.schema.json")):
    schemas.setdefault(str(later), check_schema_file(str(later)))
validators = {path: Draft202012Validator(doc) for path, doc in schemas.items()}


def schema_path_for(doc: object) -> str:
    """Name the schema file a record is held to, by the suffix of its own `$schema`.

    Args:
        doc: the parsed record.

    Returns:
        The schema's path; v3 for a record that names none (the v3 schema then reports the gap).

    """
    named = doc.get("$schema") if isinstance(doc, dict) else None
    if isinstance(named, str):
        tail = named.rsplit("/", 1)[-1]
        if tail.startswith("slag-v") and tail.endswith(".schema.json"):
            return "schema/" + tail
    return SCHEMA


bad = 0
used: dict[str, int] = {}
records = sorted(str(p) for p in Path("fleet/stars").glob("*/slag.json"))
if not records:
    print("::error::fleet/stars/ carries no slag.json, so there is no record to validate", file=sys.stderr)
    sys.exit(2)
for rec in records:
    name = rec.split("/")[2]
    try:
        doc = json.loads(Path(rec).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        # A record that is not JSON is a finding ABOUT THE RECORD, not a
        # failure of this atom: the subject is broken, not the instrument.
        bad += 1
        print(f"::error file={rec}::not readable as JSON: {exc}", file=sys.stderr)
        continue
    law = schema_path_for(doc)
    if law not in validators:
        print(f"::error::{rec} names {law}, which is not in schema/: could not read {law}", file=sys.stderr)
        sys.exit(2)
    used[law] = used.get(law, 0) + 1
    findings = [
        f"{'/'.join(str(x) for x in e.path) or '<root>'}: {e.message}"
        for e in sorted(validators[law].iter_errors(doc), key=lambda e: [str(x) for x in e.path])
    ]
    meta = doc.get("meta") if isinstance(doc, dict) else None
    if not isinstance(meta, dict) or meta.get("name") != name:
        findings.append(f"meta/name: meta.name must equal the directory name {name!r}")
    for f in findings:
        bad += 1
        print(f"::error file={rec}::{f}", file=sys.stderr)
held = ", ".join(f"{n} against {path}" for path, n in sorted(used.items()))
print(f"{len(records)} record(s) validated: {held}")

# THE CATALOG ENTRIES (slag-by-kind F8): fleet/stars/<name>.json, beside the per-star directories,
# each held to schema/fleet-star.schema.json and named for its file. The bundle lane serves them as
# data.fleet.declared.<name>, so an entry whose name is not its filename is a star routed as another.
# Zero entries is the migration's starting point, not a could-not-run; a tree with entries and no
# schema cannot be judged: 2.
CATALOG = "schema/fleet-star.schema.json"
entries = sorted(str(p) for p in Path("fleet/stars").glob("*.json"))
if entries:
    if not Path(CATALOG).exists():
        print(f"::error::fleet/stars/ carries catalog entries and there is no {CATALOG}: could not read {CATALOG}", file=sys.stderr)
        sys.exit(2)
    catalog = Draft202012Validator(check_schema_file(CATALOG))
    for entry in entries:
        stem = Path(entry).stem
        try:
            doc = json.loads(Path(entry).read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            bad += 1
            print(f"::error file={entry}::not readable as JSON: {exc}", file=sys.stderr)
            continue
        findings = [
            f"{'/'.join(str(x) for x in e.path) or '<root>'}: {e.message}"
            for e in sorted(catalog.iter_errors(doc), key=lambda e: [str(x) for x in e.path])
        ]
        if not isinstance(doc, dict) or doc.get("name") != stem:
            findings.append(f"name: name must equal the filename {stem!r}")
        for f in findings:
            bad += 1
            print(f"::error file={entry}::{f}", file=sys.stderr)
print(f"{len(entries)} catalog entr{'y' if len(entries) == 1 else 'ies'} validated against {CATALOG}")
if bad:
    sys.exit(f"::error::{bad} record finding(s)")
