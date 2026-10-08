package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// dies:refusal-codes IS A PYTHON CHECKER, NOT A GO FUNCTION. Every refusal code
// a world or a star spells is in the fleet registry, and the checker that holds
// the two together is dies' tools/check_refusal_codes.py, which the chain runs
// through uv; this port runs the same program on the venv's python3 (python.go
// says why nothing resolves at run time) and settles on its exit as the chain
// does: 0 holds, 1 a defect, 2 could not run, unmapped. What is Go is everything
// around it, which is what decides ABSENT.
//
// THE CHAIN'S `--with tomli>=2.0` IS THE VENV: tomli is in the lock, and python
// 3.14 reads TOML with tomllib besides (check_contracts.py's plain import of it
// is why the fallback was dead code there).

// refusalDoorProbe answers a could-not-run when the door does not answer for the
// registry's first remote use, and nil when the checker may run: a checker that
// cannot reach the door has found nothing.
func refusalDoorProbe(ctx context.Context, a checks.AtomDef, in Input, registry string) *checks.Verdict {
	repo, path, found := checks.FirstRegistryUse(registry)
	if !found {
		return nil
	}
	if _, _, err := in.door().Get(ctx, repo, path); err != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A use that could not be fetched is not a use that agrees.", a.ID, repo, path, err))
		return &v
	}
	return nil
}

// refusalTreeName says which registry tree this checkout is: stellar-core by the
// two paths it spells its codes on, daedalus or hermes by the module line of the
// go.mod at its root. "" is none of them.
func refusalTreeName(t tree) (string, error) {
	core := true
	for _, marker := range checks.RefusalCoreMarkers() {
		core = core && present(t, marker)
	}
	if core {
		return "stellar-core", nil
	}
	if !present(t, "go.mod") {
		return "", nil
	}
	gomod, err := t.read("go.mod")
	if err != nil {
		return "", fmt.Errorf("go.mod would not read (%v)", err)
	}
	name, _ := checks.RefusalGoTree(gomod)
	return name, nil
}

// diesRefusalCodes: every refusal code a world or a star spells is in the fleet
// registry. The registry's owner (foundry-dies) runs its own checker over its own
// registry; a spelling tree (stellar-core, daedalus, hermes) runs dies' checker,
// fetched from the door, over its own uses. A door that does not answer is a 2.
func diesRefusalCodes(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	absent := checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree neither owns the refusal registry (foundry-dies), nor spells world codes on tapes (stellar-core: wit/aiws-result.wit with conformance/tapes/*.json), nor is daedalus or hermes (go.mod module), so it has no code to register.")
	stop := diesShape(t, a)
	if stop != nil && stop.State != 0 {
		return *stop
	}
	if stop == nil {
		return refusalOwner(ctx, a, in)
	}
	name, err := refusalTreeName(t)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v.", a.ID, err))
	}
	if name == "" {
		return absent
	}
	return refusalCore(ctx, a, in, name)
}

// refusalOwner is the registry owner's half: its own checker, its own registry,
// every use read off the door.
func refusalOwner(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	if stop := requirePaths(t, a, [][2]string{
		{checks.RefusalChecker, checks.RefusalChecker + " is absent, so there is no checker to run."},
		{checks.RefusalRegistry, checks.RefusalRegistry + " is absent, so there is no registry to check."},
	}); stop != nil {
		return *stop
	}
	registry, err := t.read(checks.RefusalRegistry)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the registry would not read: "+err.Error())
	}
	if v := refusalDoorProbe(ctx, a, in, registry); v != nil {
		return *v
	}
	return pyVerdict(ctx, a, in, checks.RefusalChecker)
}

// refusalCore is the spelling tree's half: dies' checker and registry from the
// door, this tree's uses from the root, graded under the tree's own registry name.
func refusalCore(ctx context.Context, a checks.AtomDef, in Input, tree string) checks.Verdict {
	dir := tempPath("dies-refusal", "")
	defer func() { _ = os.RemoveAll(dir) }()
	var registry string
	for _, path := range checks.RefusalCheckerFiles() {
		status, body, err := in.door().Get(ctx, checks.RefusalRepo, path)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door's archive read is unreachable (%s:%s): %v. A registry that could not be fetched is not a registry the tapes agree with.", a.ID, checks.RefusalRepo, path, err))
		}
		if status != 200 {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s:%s, so the checker's inputs are not whole.", a.ID, status, checks.RefusalRepo, path))
		}
		if path == checks.RefusalRegistry {
			registry = string(body)
		}
		if err := place(filepath.Join(dir, path), body); err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the checker's inputs could not be placed: %v", a.ID, err))
		}
	}
	if v := refusalDoorProbe(ctx, a, in, registry); v != nil {
		return *v
	}
	return pyVerdict(ctx, a, in,
		filepath.Join(dir, checks.RefusalChecker),
		"--tree", checks.RefusalTreeFlag(tree),
		"--door", strings.TrimRight(in.door().Base, "/")+"/archive",
	)
}

// place writes a fetched file, making its directories.
func place(dest string, body []byte) error {
	// The MkdirAll error is not branched on: a directory that could not be made
	// fails the write below, with the same cause.
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	return os.WriteFile(dest, body, 0o644)
}
