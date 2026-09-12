import sys, urllib.error, urllib.request
try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib
DOOR = "https://forgejo.notusmi.com/api/v1/repos/rob"
try:
    doc = tomllib.loads(open("contracts/contracts.toml", encoding="utf-8").read())
except Exception as exc:
    print("could not read the manifest for the reachability probe (%s); letting the checker be the judge" % exc)
    sys.exit(0)
target = None
for spec in doc.get("contracts", {}).values():
    for copy in spec.get("copies", []):
        src = copy.get("source", {})
        if "repo" in src and "path" in src:
            target = "%s/%s/raw/%s" % (DOOR, src["repo"], src["path"])
            break
    if target:
        break
if target is None:
    print("every declared copy is local; the live check needs no network")
    sys.exit(0)
try:
    with urllib.request.urlopen(target, timeout=30) as resp:
        print("door raw API reachable (%s -> HTTP %s)" % (target, resp.status))
except urllib.error.HTTPError as exc:
    print("door raw API answered HTTP %s for %s; that is the checker's finding to make, not a provisioning failure" % (exc.code, target))
except Exception as exc:
    print("the door's raw API is unreachable (%s): %s" % (target, exc), file=sys.stderr)
    sys.exit(2)
