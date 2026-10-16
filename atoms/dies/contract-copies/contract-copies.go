// Package diescontractcopies is the atom dies:contract-copies: every vendored copy THIS tree holds agrees
// with its authority (foundry-tools#15237).
//
// WHY IT EXISTS. dies:contracts grades the whole manifest, but only in
// foundry-dies. A star that vendors aiws-result.wit, refusal.schema.json or
// internal/verbs/outputschema/* landed a drifted copy green in its own repo, and
// the next foundry-dies pull went red for a dies author who cannot fix it.
package diescontractcopies

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:contract-copies", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every vendored copy this tree holds (a WIT, a schema, a verb's output schema) is the closed set and the bytes foundry-dies' authority declares — graded here, in the repo that holds it, instead of on the next dies pull.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:contract-copies"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// checker is dies' checker, fetched off the door.
const checker = "tools/check_contracts.py"

// check grades the copies a tree holds against the authority.
//
// WHERE IT RUNS. In every tree that is not foundry-dies. Which repo the tree is
// comes from the manifest (checks.ContractTreeNames: the copies it holds), the
// checker, schema_stamp and the manifest are dies' main off the door, and the
// run is `--tree <repo>=.`: the tree's copies against the authority, no sibling
// read. A tree that holds no copy is ABSENT.
//
// THE MANIFEST IS FETCHED BEFORE THE TREE CAN BE NAMED, so a door that does not
// answer is a 2 even where there is nothing to grade: a tree whose copies cannot
// be known is not a tree with none. The fetched files are placed in a directory
// of this run's own, outside the tree.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	stop := dieskit.Shape(t, row)
	if stop != nil && stop.State != int(checks.StatePass) {
		return *stop
	}
	if stop == nil {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this is foundry-dies, which grades every copy whole under dies:contracts.")
	}
	dir := atoms.TempPath("dies-contracts", "")
	defer func() { _ = os.RemoveAll(dir) }()
	var manifest string
	for _, path := range checks.ContractCopyFiles() {
		status, body, err := in.DoorOf().Get(ctx, checks.RefusalRepo, path)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A manifest that could not be fetched is not a tree with no copies.", id, checks.RefusalRepo, path, err))
		}
		if status != 200 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s:%s, so the checker's inputs are not whole.", id, status, checks.RefusalRepo, path))
		}
		if path == checks.DoorManifest {
			manifest = string(body)
		}
		if err := atoms.Place(filepath.Join(dir, path), body); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the checker's inputs could not be placed: %v", id, err))
		}
	}
	names, err := checks.ContractTreeNames(manifest, func(path string) (bool, error) { return atoms.Present(t, path), nil })
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v.", id, err))
	}
	if len(names) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree holds none of the copies contracts.toml declares for any repo, so it has no copy to grade.")
	}
	if code, out := checks.DoorProbe(ctx, manifest, in.DoorOf()); code != 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the door's archive read is unreachable, so the authority cannot be read. A copy that could not be compared is not a copy that agrees.\n"+out)
	}
	args := []string{
		filepath.Join(dir, checker),
		"--manifest", filepath.Join(dir, checks.DoorManifest),
		"--door", strings.TrimRight(in.DoorOf().Base, "/") + "/archive",
	}
	for _, n := range names {
		args = append(args, "--tree", checks.RefusalTreeFlag(n))
	}
	return dieskit.Verdict(ctx, row, in, dieskit.WithTOML, args...)
}
