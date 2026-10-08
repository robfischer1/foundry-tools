package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("dies:contract-copies", diesContractCopies)
}

// contractsDir is where a tree that is not dies holds dies' contract checker
// for the run.
const contractsDir = "/tmp/dies-contracts"

// Every vendored copy THIS tree holds agrees with its authority
// (foundry-tools#15237).
//
// WHY IT EXISTS. dies:contracts grades the whole manifest, but only in
// foundry-dies. A star that vendors aiws-result.wit, refusal.schema.json or
// internal/verbs/outputschema/* landed a drifted copy green in its own repo, and
// the next foundry-dies pull went red for a dies author who cannot fix it.
//
// WHERE IT RUNS. In every tree that is not foundry-dies. Which repo the tree is
// comes from the manifest (checks.ContractTreeNames: the copies it holds), the
// checker, schema_stamp and the manifest are dies' main off the door, and the run
// is `--tree <repo>=.`: the tree's copies against the authority, no sibling read.
// A tree that holds no copy is ABSENT.
//
// THE MANIFEST IS FETCHED BEFORE THE TREE CAN BE NAMED, so a door that does not
// answer is a 2 even where there is nothing to grade: a tree whose copies cannot
// be known is not a tree with none.
func diesContractCopies(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:contract-copies")
	stop := diesShape(ctx, r, a)
	if stop != nil && stop.State != 0 {
		return *stop
	}
	if stop == nil {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this is foundry-dies, which grades every copy whole under dies:contracts.")
	}

	ctr := r.lane(checks.ImageFleet).WithExec([]string{"uv", "--version"})
	var manifest string
	for _, path := range checks.ContractCopyFiles() {
		status, body, err := oureaDoor.Get(ctx, checks.RefusalRepo, path)
		if err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A manifest that could not be fetched is not a tree with no copies.", a.ID, checks.RefusalRepo, path, err))
		}
		if status != 200 {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s:%s, so the checker's inputs are not whole.", a.ID, status, checks.RefusalRepo, path))
		}
		if path == checks.DoorManifest {
			manifest = string(body)
		}
		ctr = ctr.WithNewFile(contractsDir+"/"+path, string(body))
	}

	names, err := checks.ContractTreeNames(manifest, func(path string) (bool, error) { return present(ctx, r, path) })
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v.", a.ID, err))
	}
	if len(names) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this tree holds none of the copies contracts.toml declares for any repo, so it has no copy to grade.")
	}

	if code, out := checks.DoorProbe(ctx, manifest, oureaDoor); code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the door's archive read is unreachable, so the authority cannot be read. A copy that could not be compared is not a copy that agrees.\n"+out)
	}

	args := []string{
		contractsDir + "/tools/check_contracts.py",
		"--manifest", contractsDir + "/" + checks.DoorManifest,
		"--door", strings.TrimRight(oureaDoor.Base, "/") + "/archive",
	}
	for _, n := range names {
		args = append(args, "--tree", checks.RefusalTreeFlag(n))
	}
	return verdict(ctx, a, ctr.WithExec(tomlpy(args...), anyExit))
}
