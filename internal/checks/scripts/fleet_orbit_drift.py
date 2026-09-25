# SPDX-FileCopyrightText: 2026 Rob Fischer
#
# SPDX-License-Identifier: Apache-2.0
"""Check this repo's declared seams against the canonical contracts.

Every edge in orbit.toml names a contract and pins its digest. The canonical
copy lives in foundry-dies/orbits, so this fetches each one and compares. A
digest that disagrees is drift: the contract moved and this repo's declaration
did not.

Exit 0 is agreement, 1 is drift or an unpinned edge, 2 is CANNOT RUN — the
door unreachable, or answering something that is not a contract at all, which
is not the same as a contract that disagrees.
"""

import hashlib
import sys
import urllib.error
import urllib.request
from pathlib import Path

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

# The host is spelled at the CALL, not held in a constant — see the comment
# on the urlopen below. This is the rest of the path.
ORBITS = "foundry/foundry-dies/raw/orbits"
TIMEOUT_SECONDS = 30
HTTP_NOT_FOUND = 404

try:
    doc = tomllib.loads(Path("orbit.toml").read_text(encoding="utf-8"))
except (OSError, tomllib.TOMLDecodeError) as exc:
    print(f"fleet:orbit-drift: CANNOT RUN - orbit.toml did not parse: {exc}", file=sys.stderr)
    sys.exit(2)

edges = [(d, e) for d in ("consumes", "produces") for e in doc.get(d, [])]
if not edges:
    print("fleet:orbit-drift: orbit.toml declares no seams")
    sys.exit(0)

agree, drift, unpinned = [], [], []
for direction, edge in edges:
    peer = edge.get("from") or edge.get("to") or "?"
    name = edge.get("contract")
    if not name:
        unpinned.append(f"{direction} {peer} names no contract")
        continue
    url = f"https://forgejo.notusmi.com/api/v1/repos/{ORBITS}/{name}.toml"
    try:
        # THE SCHEME IS A LITERAL AT THE CALL, and the duplication with `url`
        # above is the price. ruff's S310 asks whether this can be talked into
        # opening a file: or a custom scheme; an f-string starting with the
        # https literal cannot, and a constant holding the same text is opaque
        # to the rule. `url` stays because five messages below name it.
        with urllib.request.urlopen(
            f"https://forgejo.notusmi.com/api/v1/repos/{ORBITS}/{name}.toml",
            timeout=TIMEOUT_SECONDS,
        ) as resp:
            body = resp.read()
    except urllib.error.HTTPError as exc:
        if exc.code == HTTP_NOT_FOUND:
            drift.append(
                f"{peer}: names contract '{name}', which is not in foundry-dies/orbits"
            )
            continue
        print(
            f"fleet:orbit-drift: CANNOT RUN - the door answered HTTP {exc.code} for "
            f"{url}. A contract that could not be fetched is not a contract that "
            f"agrees.",
            file=sys.stderr,
        )
        sys.exit(2)
    except OSError as exc:
        print(
            f"fleet:orbit-drift: CANNOT RUN - the door is unreachable ({url}): {exc}",
            file=sys.stderr,
        )
        sys.exit(2)
    # A BODY THAT IS NOT A CONTRACT IS NOT A CONTRACT THAT DISAGREES. The door
    # 301s a moved repo and urlopen follows it, but any proxy, login wall or
    # error page in the path answers 200 with HTML — and hashing that yields a
    # confident digest mismatch pointing at the wrong thing entirely. Measured
    # while writing this atom: the rob/ org now redirects to foundry/, and a
    # fetch that did not follow it hashed 199 bytes of "Moved Permanently".
    try:
        parsed = tomllib.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, tomllib.TOMLDecodeError) as exc:
        print(
            f"fleet:orbit-drift: CANNOT RUN - {url} did not answer a TOML document "
            f"({exc}). A body that is not a contract is not a contract that "
            f"disagrees.",
            file=sys.stderr,
        )
        sys.exit(2)
    if "verbs" not in parsed:
        print(
            f"fleet:orbit-drift: CANNOT RUN - {url} parsed as TOML but carries no "
            f"verbs key, so it is not a seam contract.",
            file=sys.stderr,
        )
        sys.exit(2)

    want = edge.get("digest")
    got = "sha256:" + hashlib.sha256(body).hexdigest()
    if not want:
        unpinned.append(
            f"{peer}: contract '{name}' carries no digest, so nothing was compared"
        )
    elif want != got:
        drift.append(
            f"{peer}: contract '{name}' hashes to {got}, orbit.toml records {want}"
        )
    else:
        agree.append(peer)

if drift or unpinned:
    for line in drift:
        print("orbit-drift: " + line, file=sys.stderr)
    for line in unpinned:
        print("orbit-drift: " + line, file=sys.stderr)
    if drift:
        print(
            "The canonical contract moved and this repo's declaration did not. "
            "Re-read foundry-dies/orbits and update orbit.toml, or say why the "
            "seam changed.",
            file=sys.stderr,
        )
    if unpinned:
        print(
            "An edge with no digest pins nothing, so this atom compared nothing "
            "for it - and nothing to check is not checked and clean.",
            file=sys.stderr,
        )
    sys.exit(1)

print(f"fleet:orbit-drift: {len(agree)} seam(s) agree with foundry-dies/orbits")
