// Package diesrefusalcodes is the atom dies:refusal-codes: every refusal code a world or a star spells
// is in the fleet registry (stellar-core F17-3b, foundry-dies#14947).
//
// IT IS A PYTHON CHECKER, NOT A GO FUNCTION. The checker that holds the codes and
// the registry together is dies' tools/check_refusal_codes.py, run under uv and
// settled on its exit as the chain did: 0 holds, 1 a defect, 2 could not run,
// unmapped. What is Go is everything around it, which is what decides ABSENT.
//
// WHERE IT RUNS, AND WHY THERE. Registry owner and spelling trees, because a code
// is spelled in one and registered in the other and a pull that edits either has
// to go red BEFORE it lands, in the repo of the person who made it
// (check_contracts.py's alarm asymmetry).
//
//	foundry-dies    owns the registry and the checker. It grades the WHOLE
//	                registry against every use, read off the door's main.
//	stellar-core    owns the tapes and the WIT enums the worlds' codes are
//	                spelled in. The checker, its two imports and the registry are
//	                fetched from the door's main and run with
//	                `--tree stellar-core=.`, which grades ONLY stellar-core's uses.
//	daedalus, hermes
//	                Go stars whose go-err-codes uses were graded only from dies,
//	                after they landed (foundry-tools#15237). Same fetch-and-scope
//	                with `--tree daedalus=.` or `--tree hermes=.`, keyed on the
//	                module line of the go.mod at the tree's root.
//
// A DOOR THAT DOES NOT ANSWER IS A 2. A use that could not be fetched is not a
// use that agrees.
package diesrefusalcodes

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
	ID: "dies:refusal-codes", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every refusal code the fleet spells - on a world's tape or WIT enum (stellar-core) or in any declared use (foundry-dies) - is in the registry, and every registered code is still spelled by its owner.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:refusal-codes"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// doorProbe answers a could-not-run when the door does not answer for the
// registry's first remote use, and nil when the checker may run: a checker that
// cannot reach the door has found nothing.
func doorProbe(ctx context.Context, in atomkit.In, registry string) *checks.Verdict {
	repo, path, found := checks.FirstRegistryUse(registry)
	if !found {
		return nil
	}
	if _, _, err := in.DoorOf().Get(ctx, repo, path); err != nil {
		v := checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A use that could not be fetched is not a use that agrees.", id, repo, path, err))
		return &v
	}
	return nil
}

// treeName says which registry tree this checkout is: stellar-core by the two
// paths it spells its codes on, daedalus or hermes by the module line of the
// go.mod at its root. "" is none of them.
func treeName(t atoms.Tree) (string, error) {
	spelled := true
	for _, marker := range checks.RefusalCoreMarkers() {
		spelled = spelled && atoms.Present(t, marker)
	}
	if spelled {
		return "stellar-core", nil
	}
	if !atoms.Present(t, "go.mod") {
		return "", nil
	}
	gomod, err := t.Read("go.mod")
	if err != nil {
		return "", fmt.Errorf("go.mod would not read (%v)", err)
	}
	name, _ := checks.RefusalGoTree(gomod)
	return name, nil
}

// check is the registry owner's half or the spelling tree's, by the tree's shape.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	absent := checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree neither owns the refusal registry (foundry-dies), nor spells world codes on tapes (stellar-core: wit/aiws-result.wit with conformance/tapes/*.json), nor is daedalus or hermes (go.mod module), so it has no code to register.")
	stop := dieskit.Shape(t, row)
	if stop != nil && stop.State != int(checks.StatePass) {
		return *stop
	}
	if stop == nil {
		return owner(ctx, in)
	}
	name, err := treeName(t)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v.", id, err))
	}
	if name == "" {
		return absent
	}
	return core(ctx, in, name)
}

// owner is the registry owner's half: its own checker, its own registry, every
// use read off the door.
func owner(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{checks.RefusalChecker, checks.RefusalChecker + " is absent, so there is no checker to run."},
		{checks.RefusalRegistry, checks.RefusalRegistry + " is absent, so there is no registry to check."},
	}); stop != nil {
		return *stop
	}
	registry, err := t.Read(checks.RefusalRegistry)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the registry would not read: "+err.Error())
	}
	if v := doorProbe(ctx, in, registry); v != nil {
		return *v
	}
	return dieskit.Verdict(ctx, row, in, dieskit.WithTOML, checks.RefusalChecker)
}

// core is the spelling tree's half: dies' checker and registry from the door,
// this tree's uses from the root, graded under the tree's own registry name.
func core(ctx context.Context, in atomkit.In, tree string) checks.Verdict {
	dir := atoms.TempPath("dies-refusal", "")
	defer func() { _ = os.RemoveAll(dir) }()
	var registry string
	for _, path := range checks.RefusalCheckerFiles() {
		status, body, err := in.DoorOf().Get(ctx, checks.RefusalRepo, path)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A registry that could not be fetched is not a registry the tapes agree with.", id, checks.RefusalRepo, path, err))
		}
		if status != 200 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s:%s, so the checker's inputs are not whole.", id, status, checks.RefusalRepo, path))
		}
		if path == checks.RefusalRegistry {
			registry = string(body)
		}
		if err := atoms.Place(filepath.Join(dir, path), body); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the checker's inputs could not be placed: %v", id, err))
		}
	}
	if v := doorProbe(ctx, in, registry); v != nil {
		return *v
	}
	return dieskit.Verdict(ctx, row, in, dieskit.WithTOML,
		filepath.Join(dir, checks.RefusalChecker),
		"--tree", checks.RefusalTreeFlag(tree),
		"--door", strings.TrimRight(in.DoorOf().Base, "/")+"/archive",
	)
}
