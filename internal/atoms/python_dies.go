package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/scripts"
)

// THE DIES ATOMS THAT RUN A CHECKER. Each opens, as the chain did, with the
// tree's shape (diesShape: absent anywhere but the policy die's source) and with
// the files its checker needs (CANNOT RUN naming the one that is missing), and
// only then touches python.
//
// NONE OF THEM WRITES TO THE TREE, and that was read, not assumed: the
// regenerate-and-compare checkers (check_wit_regenerated.py, which regenerates
// wit/ in memory and compares bytes; check_schema_rendered.py, which re-renders
// the schemas and compares bytes) build their expectation in memory and read the
// committed files. Their one write-adjacent act, bytecode for the sibling modules
// they import, is turned off (pythonEnv). The writers in these checkers' modules
// (wit-from-schema --out, schema_render stamp and materialize) are other
// commands, which no atom runs.

// contractsChecker and contractsFixtures are the files dies:contracts needs in
// the tree beside its manifest (checks.DoorManifest).
const (
	contractsChecker  = "tools/check_contracts.py"
	contractsFixtures = "tests/contracts/fixtures.toml"
)

// diesContracts: every copy of every shared closed set agrees, and the checker
// is proved to detect first.
//
// THE DOOR IS PROBED BEFORE THE LIVE CHECK, the target read out of the manifest.
// check_contracts.py raises ContractError on an unreachable copy and exits 1 for
// it, which is right for a gate whose runner sat on the same network as the door
// and wrong for an atom: a copy that could not be FETCHED is not a copy that
// DISAGREES. So an unreachable door is a 2 here and every other answer stays the
// checker's own.
func diesContracts(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	if stop := diesShape(t, a); stop != nil {
		return *stop
	}
	if stop := requirePaths(t, a, [][2]string{
		{contractsChecker, contractsChecker + " is absent, so there is no checker to run."},
		{contractsFixtures, contractsFixtures + " is absent, and a gate that cannot prove it detects is a gate that is not there."},
		{checks.DoorManifest, checks.DoorManifest + " is absent, so there is no live manifest to check."},
	}); stop != nil {
		return *stop
	}
	if stop := pyProbe(ctx, a, in); stop != nil {
		return *stop
	}
	lines := []string{}
	bad := false
	for _, f := range checks.ContractFixtures {
		out, code := in.run(ctx, pyCmd(in.Root, contractsChecker, "--manifest", contractsFixtures, "--contract", f.Name))
		if code < 0 {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the %s fixture never ran: %s", a.ID, f.Name, out))
		}
		line, isBad := checks.ContractFixtureVerdict(f.Name, f.ExpectFail, code)
		lines = append(lines, line)
		bad = bad || isBad
	}
	if bad {
		return checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - the fixtures no longer prove the gate detects:\n"+strings.Join(lines, "\n"))
	}
	manifest, err := t.read(checks.DoorManifest)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the door reachability probe never ran: "+err.Error())
	}
	if code, out := checks.DoorProbe(ctx, manifest, in.door()); code != 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the door's archive read is unreachable, so the remote copies cannot be read. A copy that could not be fetched is not a copy that agrees.\n"+out)
	}
	return pySettle(ctx, a, in, contractsChecker)
}

// diesContractCopies: every vendored copy THIS tree holds agrees with its
// authority (foundry-tools#15237). It runs in every tree that is not
// foundry-dies, which grades every copy whole under dies:contracts: which repo
// the tree is comes from the manifest (checks.ContractTreeNames), the checker,
// schema_stamp and the manifest are dies' main off the door, and the run is
// `--tree <repo>=.`. A tree that holds no copy is ABSENT.
//
// THE MANIFEST IS FETCHED BEFORE THE TREE CAN BE NAMED, so a door that does not
// answer is a 2 even where there is nothing to grade: a tree whose copies cannot
// be known is not a tree with none. The fetched files are placed in a directory
// of this run's own, outside the tree.
func diesContractCopies(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	stop := diesShape(t, a)
	if stop != nil && stop.State != int(checks.StatePass) {
		return *stop
	}
	if stop == nil {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this is foundry-dies, which grades every copy whole under dies:contracts.")
	}
	dir := tempPath("dies-contracts", "")
	defer func() { _ = os.RemoveAll(dir) }()
	var manifest string
	for _, path := range checks.ContractCopyFiles() {
		status, body, err := in.door().Get(ctx, checks.RefusalRepo, path)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A manifest that could not be fetched is not a tree with no copies.", a.ID, checks.RefusalRepo, path, err))
		}
		if status != 200 {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s:%s, so the checker's inputs are not whole.", a.ID, status, checks.RefusalRepo, path))
		}
		if path == checks.DoorManifest {
			manifest = string(body)
		}
		if err := place(filepath.Join(dir, path), body); err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the checker's inputs could not be placed: %v", a.ID, err))
		}
	}
	names, err := checks.ContractTreeNames(manifest, func(path string) (bool, error) { return present(t, path), nil })
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v.", a.ID, err))
	}
	if len(names) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree holds none of the copies contracts.toml declares for any repo, so it has no copy to grade.")
	}
	if code, out := checks.DoorProbe(ctx, manifest, in.door()); code != 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the door's archive read is unreachable, so the authority cannot be read. A copy that could not be compared is not a copy that agrees.\n"+out)
	}
	args := []string{
		filepath.Join(dir, contractsChecker),
		"--manifest", filepath.Join(dir, checks.DoorManifest),
		"--door", strings.TrimRight(in.door().Base, "/") + "/archive",
	}
	for _, n := range names {
		args = append(args, "--tree", checks.RefusalTreeFlag(n))
	}
	return pyVerdict(ctx, a, in, args...)
}

// diesSchema: the slag v1 and v3 schemas are valid Draft 2020-12 documents and
// every fleet/stars/*/slag.json satisfies v3. The validator is embedded
// (checks/scripts/dies_schema.py) and written to a file of this run's own for
// python to read.
//
// THE PROVISION IS PROBED BEFORE THE GATE RUNS, python and then an
// `import jsonschema`, so a container whose venv lacks the validator is a 2
// rather than a schema finding nobody made.
func diesSchema(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	if stop := diesShape(t, a); stop != nil {
		return *stop
	}
	if stop := requirePaths(t, a, [][2]string{
		{"schema/slag.schema.json", "schema/slag.schema.json is absent, so there is no payload to validate."},
		{"schema/slag-v3.schema.json", "schema/slag-v3.schema.json is absent, so there is no schema to validate the records against."},
	}); stop != nil {
		return *stop
	}
	if stop := pyProbe(ctx, a, in); stop != nil {
		return *stop
	}
	if stop := pyImports(ctx, a, in, "jsonschema"); stop != nil {
		return *stop
	}
	script := tempPath("dies-schema", ".py")
	defer func() { _ = os.Remove(script) }()
	if err := os.WriteFile(script, []byte(scripts.DiesSchema), 0o600); err != nil {
		return neverRan(a, "the validator could not be written for the run: "+err.Error())
	}
	return pySettle(ctx, a, in, script)
}

// pyChecker is the shape of the dies atoms whose checker is the tree's own: the
// shape, the checker's presence, python, and the checker's exit as the verdict.
// Its ladder is the fleet's (0 clean, 1 a finding, 2 the check could not run, and
// could-not-run outranks a finding), which is why nothing translates it.
func pyChecker(ctx context.Context, a checks.AtomDef, in Input, checker string) checks.Verdict {
	t := in.tree()
	if stop := diesShape(t, a); stop != nil {
		return *stop
	}
	if stop := requirePaths(t, a, [][2]string{{checker, checker + " is absent, so there is no checker to run."}}); stop != nil {
		return *stop
	}
	return pyVerdict(ctx, a, in, checker)
}

// diesFindings: the findings schema still says what it is for. THE CHECKER IS
// THE TREE'S: foundry-dies owns the schema and tools/check_findings.py, so the
// repo that owns a contract is the repo that goes red when it breaks. The
// fixtures are not required here, deliberately; an absent tests/findings is a
// could-not-run the script already reports.
func diesFindings(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	if stop := diesShape(t, a); stop != nil {
		return *stop
	}
	if stop := requirePaths(t, a, [][2]string{
		{"schema/findings.schema.json", "schema/findings.schema.json is absent, so there is no schema to validate."},
		{"tools/check_findings.py", "tools/check_findings.py is absent, so there is no checker to run."},
	}); stop != nil {
		return *stop
	}
	return pyVerdict(ctx, a, in, "tools/check_findings.py")
}

// diesSchemas: EVERY schema in the tree is checked, discovered rather than
// listed. It requires no particular schema (a tree with no schema/ is a
// could-not-run the script reports), so foundry-dies' layout is not pinned into
// this module.
func diesSchemas(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return pyChecker(ctx, a, in, "tools/check_schemas.py")
}

// diesWitRegenerated: the committed wit/ is what the generator makes of schema/
// (stellar-core F2). The checker regenerates in memory and compares bytes, so a
// schema edit nobody re-ran, a hand edit of a generated file and a generated
// file the generator stopped emitting are one failure. A refusal in the
// generator's report is NOT a finding here: the report is itself a generated,
// compared file.
func diesWitRegenerated(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return pyChecker(ctx, a, in, "tools/check_wit_regenerated.py")
}

// diesSchemaRendered: every die schema is the render of urania's shapes
// (stellar-core F14). The lane holds this tree and nothing else, so it cannot ask
// the graph: the committed schema/<name>.shapes.json stands for the shapes, and
// the checker re-renders it and compares bytes. Whether the snapshot still equals
// the graph is `--live`, which a session runs.
func diesSchemaRendered(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return pyChecker(ctx, a, in, "tools/check_schema_rendered.py")
}

// diesWireGrammar: the wire grammar's two rules about answers (stellar-core
// F17): every string enum in a tool-output schema folds onto the six findings
// words or is declared kept, and a required non-null measure is attested or
// fixed. THE CHECKER AND ITS MODES ARE THE TREE'S: each section of
// contracts/wire-grammar.toml says warn or fail, so the flip from reporting to
// gating is a dies edit, and this atom passes the exit code straight through.
func diesWireGrammar(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return pyChecker(ctx, a, in, "tools/check_wire_grammar.py")
}
