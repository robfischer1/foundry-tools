import hashlib, sys, urllib.error, urllib.request

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

DOOR = "https://forgejo.notusmi.com/api/v1/repos/foundry/foundry-dies/raw/orbits"

try:
    doc = tomllib.loads(open("orbit.toml", encoding="utf-8").read())
except Exception as exc:
    print("fleet:orbit-drift: CANNOT RUN - orbit.toml did not parse: %s" % exc, file=sys.stderr)
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
        unpinned.append("%s %s names no contract" % (direction, peer))
        continue
    url = "%s/%s.toml" % (DOOR, name)
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            body = resp.read()
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            drift.append("%s: names contract '%s', which is not in foundry-dies/orbits" % (peer, name))
            continue
        print("fleet:orbit-drift: CANNOT RUN - the door answered HTTP %s for %s. A contract that could not be fetched is not a contract that agrees." % (exc.code, url), file=sys.stderr)
        sys.exit(2)
    except Exception as exc:
        print("fleet:orbit-drift: CANNOT RUN - the door is unreachable (%s): %s" % (url, exc), file=sys.stderr)
        sys.exit(2)
    # A BODY THAT IS NOT A CONTRACT IS NOT A CONTRACT THAT DISAGREES. The door
    # 301s a moved repo and urlopen follows it, but any proxy, login wall or
    # error page in the path answers 200 with HTML — and hashing that yields a
    # confident digest mismatch pointing at the wrong thing entirely. Measured
    # while writing this atom: the rob/ org now redirects to foundry/, and a
    # fetch that did not follow it hashed 199 bytes of "Moved Permanently".
    try:
        parsed = tomllib.loads(body.decode("utf-8"))
    except Exception as exc:
        print("fleet:orbit-drift: CANNOT RUN - %s did not answer a TOML document (%s). A body that is not a contract is not a contract that disagrees." % (url, exc), file=sys.stderr)
        sys.exit(2)
    if "verbs" not in parsed:
        print("fleet:orbit-drift: CANNOT RUN - %s parsed as TOML but carries no verbs key, so it is not a seam contract." % url, file=sys.stderr)
        sys.exit(2)

    want = edge.get("digest")
    got = "sha256:" + hashlib.sha256(body).hexdigest()
    if not want:
        unpinned.append("%s: contract '%s' carries no digest, so nothing was compared" % (peer, name))
    elif want != got:
        drift.append("%s: contract '%s' hashes to %s, orbit.toml records %s" % (peer, name, got, want))
    else:
        agree.append(peer)

if drift or unpinned:
    for line in drift:
        print("orbit-drift: " + line, file=sys.stderr)
    for line in unpinned:
        print("orbit-drift: " + line, file=sys.stderr)
    if drift:
        print("The canonical contract moved and this repo's declaration did not. Re-read foundry-dies/orbits and update orbit.toml, or say why the seam changed.", file=sys.stderr)
    if unpinned:
        print("An edge with no digest pins nothing, so this atom compared nothing for it - and nothing to check is not checked and clean.", file=sys.stderr)
    sys.exit(1)

print("fleet:orbit-drift: %d seam(s) agree with foundry-dies/orbits" % len(agree))
