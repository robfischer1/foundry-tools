# SPDX-FileCopyrightText: 2026 Rob Fischer
#
# SPDX-License-Identifier: Apache-2.0
"""Provisioning probe for dies:contracts: can this container reach the door.

A contract copy is fetched from the door's raw API, so a checker that cannot
reach it has not found a drifted contract; it has found nothing. This probe
runs first and exits 2 so that unreachability reads as CANNOT RUN rather than
as the checker's finding.

Exit 0 is "reachable, or nothing to reach"; exit 2 is "the door is not there".
"""

import sys
import urllib.error
import urllib.request
from pathlib import Path

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

TIMEOUT_SECONDS = 30

try:
    doc = tomllib.loads(Path("contracts/contracts.toml").read_text(encoding="utf-8"))
except (OSError, tomllib.TOMLDecodeError) as exc:
    print(
        f"could not read the manifest for the reachability probe ({exc}); "
        f"letting the checker be the judge"
    )
    sys.exit(0)

target = None
for spec in doc.get("contracts", {}).values():
    for copy in spec.get("copies", []):
        src = copy.get("source", {})
        if "repo" in src and "path" in src:
            target = f"{src['repo']}/raw/{src['path']}"
            break
    if target:
        break

if target is None:
    print("every declared copy is local; the live check needs no network")
    sys.exit(0)

try:
    # THE SCHEME IS A LITERAL AT THE CALL, which is a real property and not a
    # formality: ruff's S310 asks whether this can be talked into opening a
    # file: or a custom scheme, and an f-string that starts with the https
    # literal cannot. The host lived in a DOOR constant before and the rule
    # fired, correctly — it could not see what the constant held.
    with urllib.request.urlopen(
        f"https://forgejo.notusmi.com/api/v1/repos/{target}",
        timeout=TIMEOUT_SECONDS,
    ) as resp:
        print(f"door raw API reachable ({target} -> HTTP {resp.status})")
except urllib.error.HTTPError as exc:
    print(
        f"door raw API answered HTTP {exc.code} for {target}; that is the "
        f"checker's finding to make, not a provisioning failure"
    )
except OSError as exc:
    print(f"the door's raw API is unreachable ({target}): {exc}", file=sys.stderr)
    sys.exit(2)
