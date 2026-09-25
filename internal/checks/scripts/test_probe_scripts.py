# SPDX-FileCopyrightText: 2026 Rob Fischer
#
# SPDX-License-Identifier: Apache-2.0
"""The embedded probe scripts' EXIT LADDER, which is their whole contract.

These three files are //go:embed'd into the gate binary and rendered into a
container, where nothing about them is visible to the caller except the code
they leave with. The lane reads that code as a verdict — 0 pass, 1 findings,
2 CANNOT RUN — so the ladder is the interface, and it is what these tests pin.

WHY BY SUBPROCESS. They are scripts, not modules: the work happens at import
time and leaves by sys.exit. Importing one runs it, so the only honest way to
assert what it exits with is to run it the way the container does and read the
code.

THREE PROPERTIES OF THE HARNESS, each load-bearing:

- Every script is staged into the tree under ONE name, `probe.py`, so the argv
  is `[sys.executable, "probe.py"]` — literals, which is what makes this free
  of ruff's S603 without a suppression. The ruleset's escape needs a ratified
  signoff from Rob and a real conflict between two tools; neither applies to
  "my own test runs my own script", so the shape changed instead.
- Every case settles BEFORE any network call, so the suite never depends on
  the door being up.
- dies_schema.py imports jsonschema, and the gate runs pytest under
  `uv run --no-project --with pytest`, which does not carry it. The tree gets
  a minimal stub instead. That is a deliberate narrowing: these tests pin THIS
  SCRIPT'S ladder — which fault leaves by which code — and not jsonschema's
  correctness, which is not ours to assert.

They exist because the python lane became declared by its files on 2026-09-25
and foundry-tools' own three .py were suddenly in it with nothing to collect —
Rob's rule, unchanged since 2026-09-11: nothing is built without tests.
"""

import json
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPTS = Path(__file__).parent

PASS = 0
FINDINGS = 1
CANNOT_RUN = 2

# The stub's whole job is to let `import jsonschema` succeed and to let a
# schema be rejected on demand. check_schema raises when `type` is not a
# string, which is enough to drive the malformed-schema case; iter_errors
# yields nothing, so record findings come from the naming invariants the
# script checks itself and not from validation we would be faking.
STUB_INIT = '''
from .exceptions import SchemaError


class Draft202012Validator:
    def __init__(self, schema):
        self.schema = schema

    @staticmethod
    def check_schema(schema):
        if not isinstance(schema.get("type", ""), str):
            raise SchemaError("type must be a string")

    def iter_errors(self, doc):
        return iter(())
'''

STUB_EXCEPTIONS = '''
class SchemaError(Exception):
    pass
'''


def stage(script: str, tree: Path) -> None:
    """Put one probe script in *tree* as probe.py, the way the container does.

    Args:
        script: the script's file name in this directory.
        tree: the temporary repository to run it against.
    """
    (tree / "probe.py").write_text(
        (SCRIPTS / script).read_text(encoding="utf-8"), encoding="utf-8"
    )


def stub_jsonschema(tree: Path) -> None:
    """Give *tree* the slice of jsonschema this ladder needs.

    Args:
        tree: the temporary repository to run against.
    """
    pkg = tree / "jsonschema"
    pkg.mkdir()
    (pkg / "__init__.py").write_text(STUB_INIT, encoding="utf-8")
    (pkg / "exceptions.py").write_text(STUB_EXCEPTIONS, encoding="utf-8")


def probe(tree: Path) -> subprocess.CompletedProcess[str]:
    """Run the staged probe and hand back its verdict.

    Args:
        tree: the temporary repository, with probe.py already staged.

    Returns:
        The finished process, whose returncode is the verdict.
    """
    return subprocess.run(
        [sys.executable, "probe.py"],
        cwd=tree,
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )


# ---- dies_door_probe.py: a probe that cannot reach the door found nothing ----


def test_the_door_probe_lets_the_checker_judge_when_there_is_no_manifest(tmp_path):
    stage("dies_door_probe.py", tmp_path)
    done = probe(tmp_path)
    assert done.returncode == PASS, done.stderr
    assert "letting the checker be the judge" in done.stdout


def test_the_door_probe_needs_no_network_for_local_copies(tmp_path):
    # Every declared copy is local, so there is nothing to reach and the probe
    # must not try — a probe that touched the network here would make the
    # whole dies lane depend on the door being up to check nothing.
    (tmp_path / "contracts").mkdir()
    (tmp_path / "contracts" / "contracts.toml").write_text(
        '[contracts.a]\ncopies = [{ source = { path = "local/thing.toml" } }]\n',
        encoding="utf-8",
    )
    stage("dies_door_probe.py", tmp_path)
    done = probe(tmp_path)
    assert done.returncode == PASS, done.stderr
    assert "every declared copy is local" in done.stdout


# ---- fleet_orbit_drift.py ----


def test_orbit_drift_cannot_run_without_an_orbit_file(tmp_path):
    # 2, NOT 1. No orbit.toml is "this atom did not run", and reading it as a
    # finding would file drift against a repo whose seams were never read.
    stage("fleet_orbit_drift.py", tmp_path)
    done = probe(tmp_path)
    assert done.returncode == CANNOT_RUN, done.stdout
    assert "CANNOT RUN" in done.stderr
    assert "did not parse" in done.stderr


def test_orbit_drift_is_clean_when_nothing_is_declared(tmp_path):
    (tmp_path / "orbit.toml").write_text("# no seams\n", encoding="utf-8")
    stage("fleet_orbit_drift.py", tmp_path)
    done = probe(tmp_path)
    assert done.returncode == PASS, done.stderr
    assert "declares no seams" in done.stdout


def test_orbit_drift_reports_an_edge_that_names_no_contract(tmp_path):
    # Settled before any fetch: an edge with no contract pins nothing, and
    # nothing to check is not checked and clean.
    (tmp_path / "orbit.toml").write_text(
        '[[consumes]]\nfrom = "hades"\n', encoding="utf-8"
    )
    stage("fleet_orbit_drift.py", tmp_path)
    done = probe(tmp_path)
    assert done.returncode == FINDINGS, done.stdout
    assert "names no contract" in done.stderr


# ---- dies_schema.py: the ladder Fox32 found flat ----

VALID = {"$id": "https://example/slag", "type": "object", "properties": {"a": {}}}


def schema_tree(tmp_path: Path, **docs: object) -> Path:
    """Build a tree with the named schema documents and the stub.

    Args:
        tmp_path: the temporary directory to build in.
        docs: file name (underscored) to document.

    Returns:
        The tree, with dies_schema.py staged and ready to run.
    """
    (tmp_path / "schema").mkdir()
    for name, doc in docs.items():
        target = tmp_path / "schema" / name.replace("_", "-").replace("-json", ".json")
        target.write_text(json.dumps(doc), encoding="utf-8")
    stub_jsonschema(tmp_path)
    stage("dies_schema.py", tmp_path)
    return tmp_path


def test_the_schema_check_cannot_run_without_its_schema(tmp_path):
    # THE INSTRUMENT, NOT THE SUBJECT. A schema file that is not there means
    # this atom could not look; before 2026-09-25 it left by an uncaught
    # raise, whose traceback exits 1 — indistinguishable from having found
    # something wrong with foundry-dies.
    done = probe(schema_tree(tmp_path))
    assert done.returncode == CANNOT_RUN, done.stdout + done.stderr
    assert "could not read schema/slag.schema.json" in done.stderr


def test_a_malformed_schema_is_a_finding_not_a_crash(tmp_path):
    # The other half of the same ladder: a schema that is not a schema IS
    # something wrong with foundry-dies, so 1 — but said, not thrown.
    done = probe(schema_tree(tmp_path, **{"slag.schema.json": {"type": 17}}))
    assert done.returncode == FINDINGS, done.stdout + done.stderr
    assert "not a valid Draft 2020-12 schema" in done.stderr
    assert "Traceback" not in done.stderr


def test_required_naming_an_undefined_property_is_a_finding(tmp_path):
    # A schema that requires a key it never describes validates nothing about
    # that key, which is the defect this half exists to catch.
    done = probe(schema_tree(tmp_path, **{"slag.schema.json": {**VALID, "required": ["b"]}}))
    assert done.returncode == FINDINGS, done.stdout + done.stderr
    assert "required names properties that are not defined" in done.stderr


def test_a_clean_pair_of_schemas_with_no_records_passes(tmp_path):
    done = probe(
        schema_tree(
            tmp_path,
            **{
                "slag.schema.json": VALID,
                "slag-v2.schema.json": {**VALID, "$id": "https://example/v2"},
            },
        )
    )
    assert done.returncode == PASS, done.stdout + done.stderr
    assert "0 v2 record(s) validated" in done.stdout


def test_a_record_that_is_not_json_is_a_finding_about_the_record(tmp_path):
    tree = schema_tree(
        tmp_path,
        **{
            "slag.schema.json": VALID,
            "slag-v2.schema.json": {**VALID, "$id": "https://example/v2"},
        },
    )
    star = tree / "fleet" / "stars" / "hades"
    star.mkdir(parents=True)
    (star / "hades.slag").write_text("not json at all", encoding="utf-8")
    done = probe(tree)
    assert done.returncode == FINDINGS, done.stdout + done.stderr
    assert "not readable as JSON" in done.stderr
    assert "Traceback" not in done.stderr


@pytest.mark.parametrize(
    "script", ["dies_door_probe.py", "dies_schema.py", "fleet_orbit_drift.py"]
)
def test_every_probe_carries_the_fleet_copyright(script):
    # CPY001 is in the fleet ruleset with a notice-rgx, and these three files
    # predate the lane reaching them. The lint atom enforces it; this says so
    # in the suite too, so deleting the header fails something a reader runs.
    head = (SCRIPTS / script).read_text(encoding="utf-8")[:200]
    assert "SPDX-FileCopyrightText" in head
