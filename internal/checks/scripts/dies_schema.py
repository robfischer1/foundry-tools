# SPDX-FileCopyrightText: 2026 Rob Fischer
#
# SPDX-License-Identifier: Apache-2.0
"""Validate foundry-dies' slag schemas and every v2 record against them.

Two halves. The v1 schema is checked for being a well-formed Draft 2020-12
document whose `required` names only properties it defines — a schema that
requires a key it never describes validates nothing about that key. Then every
`fleet/stars/*/*.slag` is validated against the v2 schema, plus the two naming
invariants the schema itself cannot express: the record is named after its own
directory, and `meta.name` agrees with it.

THE EXIT LADDER IS THREE-STATE, AND IT WAS TWO. 0 is clean, 1 is a finding, 2
is CANNOT RUN. Every fault used to leave by `sys.exit("...")` — which is exit
1 with the message on stderr — or by an uncaught raise, whose traceback also
exits 1. So a schema file that could not be read and a record that failed
validation were the same code, and the dies lane reads 1 as findings: "the
check did not run" was reported as "the check found something", in the one
script that should know better. Noticed by Fox32, 2026-09-25, and verified
before it was changed.

The instrument's own faults — a schema file missing, unreadable or not JSON —
are 2. Everything the schemas and records are found to be wrong about is 1.
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


schema = load_schema("schema/slag.schema.json")
well_formed(schema, "schema/slag.schema.json")
props = set(schema.get("properties", {}))
missing = [k for k in schema.get("required", []) if k not in props]
if missing:
    sys.exit(f"::error::required names properties that are not defined: {missing}")
print(f"valid Draft 2020-12 schema - {schema.get('$id')}")
print(f"{len(schema.get('required', []))} required keys, all defined in properties")

v2 = load_schema("schema/slag-v2.schema.json")
well_formed(v2, "schema/slag-v2.schema.json")
v = Draft202012Validator(v2)
bad = 0
records = sorted(str(p) for p in Path("fleet/stars").glob("*/*.slag"))
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
    findings = [
        f"{'/'.join(str(x) for x in e.path) or '<root>'}: {e.message}"
        for e in sorted(v.iter_errors(doc), key=lambda e: list(e.path))
    ]
    if rec != f"fleet/stars/{name}/{name}.slag":
        findings.append(
            f"<root>: a v2 record is named <name>.slag beside its own "
            f"directory, not {rec}"
        )
    if doc.get("meta", {}).get("name") != name:
        findings.append(f"meta/name: meta.name must equal the directory name {name!r}")
    for f in findings:
        bad += 1
        print(f"::error file={rec}::{f}", file=sys.stderr)
print(f"{len(records)} v2 record(s) validated against {v2.get('$id')}")
if bad:
    sys.exit(f"::error::{bad} v2 record finding(s)")
