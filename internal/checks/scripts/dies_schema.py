import glob, json, sys
from jsonschema import Draft202012Validator

schema = json.load(open("schema/slag.schema.json"))
Draft202012Validator.check_schema(schema)
props = set(schema.get("properties", {}))
missing = [k for k in schema.get("required", []) if k not in props]
if missing:
    sys.exit("::error::required names properties that are not defined: %s" % missing)
print("valid Draft 2020-12 schema - %s" % schema.get("$id"))
print("%d required keys, all defined in properties" % len(schema.get("required", [])))

v2 = json.load(open("schema/slag-v2.schema.json"))
Draft202012Validator.check_schema(v2)
v = Draft202012Validator(v2)
bad = 0
records = sorted(glob.glob("fleet/stars/*/*.slag"))
for rec in records:
    name = rec.split("/")[2]
    doc = json.load(open(rec))
    findings = [
        "%s: %s" % ("/".join(str(x) for x in e.path) or "<root>", e.message)
        for e in sorted(v.iter_errors(doc), key=lambda e: list(e.path))
    ]
    if rec != "fleet/stars/%s/%s.slag" % (name, name):
        findings.append("<root>: a v2 record is named <name>.slag beside its own directory, not %s" % rec)
    if doc.get("meta", {}).get("name") != name:
        findings.append("meta/name: meta.name must equal the directory name %r" % name)
    for f in findings:
        bad += 1
        print("::error file=%s::%s" % (rec, f), file=sys.stderr)
print("%d v2 record(s) validated against %s" % (len(records), v2.get("$id")))
if bad:
    sys.exit("::error::%d v2 record finding(s)" % bad)
