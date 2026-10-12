# SPDX-FileCopyrightText: 2026 Rob Fischer
#
# SPDX-License-Identifier: Apache-2.0
"""Validate foundry-dies' catalog entries, and refuse a tree that still carries fleet/stars/.

Every catalog entry, fleet/repos/<name>.json, is held to schema/fleet-star.schema.json and named
for its file. That is the whole check: slag is retired (slag-by-kind F14), so there are no slag
schemas and no slag records to validate. A fleet/stars/ directory in the tree is a stale writer
(or a revert) bringing back the record the fleet no longer reads, and it is a FINDING, not
something to validate: the bundle lane serves nothing from it.

THE EXIT LADDER IS THREE-STATE. 0 is clean, 1 is a finding, 2 is CANNOT RUN. The instrument's
own faults (a schema file missing, unreadable or not JSON; nothing to validate) are 2, because
zero documents validated is not the same fact as every document valid; everything the schema and
entries are found to be wrong about is 1.
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
        sys.exit(
            f"::error file={path}::required names properties that are not defined: {missing}"
        )
    print(f"valid Draft 2020-12 schema - {doc.get('$id')}")
    print(f"{len(doc.get('required', []))} required keys, all defined in properties")
    return doc


# fleet/stars/ IS RETIRED. Slag and the lean rows lived there; the bundle lane stages nothing from it
# and daedalus writes nothing to it, so anything under it is a writer that did not get the news.
if Path("fleet/stars").exists():
    print(
        "::error::fleet/stars/ is present, and slag is retired (slag-by-kind F14): the fleet record is "
        "fleet/repos/<name>.json. Delete fleet/stars/ and find the writer that put it back.",
        file=sys.stderr,
    )
    sys.exit(1)

bad = 0
CATALOG = "schema/fleet-star.schema.json"
entries = sorted(str(p) for p in Path("fleet/repos").glob("*.json"))
if not entries:
    print(
        "::error::fleet/repos/ carries no catalog entry, so there is nothing to validate",
        file=sys.stderr,
    )
    sys.exit(2)
if entries:
    if not Path(CATALOG).exists():
        print(
            f"::error::the tree carries catalog entries and there is no {CATALOG}: could not read {CATALOG}",
            file=sys.stderr,
        )
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
            for e in sorted(
                catalog.iter_errors(doc), key=lambda e: [str(x) for x in e.path]
            )
        ]
        if not isinstance(doc, dict) or doc.get("name") != stem:
            findings.append(f"name: name must equal the filename {stem!r}")
        for f in findings:
            bad += 1
            print(f"::error file={entry}::{f}", file=sys.stderr)
print(
    f"{len(entries)} catalog entr{'y' if len(entries) == 1 else 'ies'} validated against {CATALOG}"
)
if bad:
    sys.exit(f"::error::{bad} record finding(s)")
