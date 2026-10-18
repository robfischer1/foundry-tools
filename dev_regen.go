package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/devlane"
)

// regenAtom is the logic atom the regen verb runs.
const regenAtom = "dev:go-regen"

// regenEntry is the catalogue lookup; a test replaces it.
var regenEntry = catalogue.Lookup

// Regen regenerates the tree's gravity-generated cores and answers the files it
// wrote: `dagger call dev --source=. regen --args=alarm export --path=.`.
//
// IT IS THE ATOM, NOT A SECOND IMPLEMENTATION. dev:go-regen (atoms/dev/go-regen)
// is run by the in-pod executor for the regen verb; this is the same atom
// compiled from the same file and run on the engine, over this source, with the
// arguments given. Its environment is the atom's own (atom_env_goregen.go): the
// rust lane with the pinned wasm-tools, wac, gravity and gofmt. What it answers
// is the source diffed against the tree after the atom, so only what moved is
// exported, and a failure exits non-zero with the verdict's reason.
//
// +cache="never"
func (d *Dev) Regen(ctx context.Context,
	// The cores to regenerate, as bare words, and cores to bring into being:
	// `--new <core> --rev <tag-or-commit> --world <aiws:pkg/world@ver>`,
	// repeatable and mixable with bare cores.
	// +optional
	args []string,
	// Run past the engine's cache even on an unchanged tree.
	// +optional
	fresh bool,
) (*dagger.Directory, error) {
	if d.Lang != devlane.Go {
		return nil, fmt.Errorf("regen regenerates a Go tree's gravity cores: this source is %s", d.Lang)
	}
	return regenOver(ctx, regenAtom, d.Source, d.Repo, true, d.Tree, args, fresh)
}

// tsRegenAtom is the logic atom the TypeScript regen verb runs.
const tsRegenAtom = "dev:ts-regen"

// RegenTs regenerates a stellar-core-ts tree's worlds and answers the files it
// wrote: `dagger call -m . --source=. regen-ts --args=stamp,tail export --path=.`.
// It is dev:ts-regen (atoms/dev/ts-regen), the write twin of ts:regen, compiled
// from the same file the executor runs and run on the engine over this source.
// Only what moved is answered (the source diffed against the tree after the
// atom), and a pin that does not hold exits non-zero with the verdict's reason.
//
// +cache="never"
func (m *FoundryTools) RegenTs(ctx context.Context,
	// The worlds to regenerate: the <world> of src/<world>core-gen.
	// +optional
	args []string,
	// Run past the engine's cache even on an unchanged tree.
	// +optional
	fresh bool,
) (*dagger.Directory, error) {
	return regenOver(ctx, tsRegenAtom, m.Source, m.Repo, false, "", args, fresh)
}

// regenOver runs one regen atom over a source with the arguments, and answers
// the diff of the source against the tree the atom left. A verdict that is not a
// pass ends the call with the verdict's exit code and its reason.
func regenOver(ctx context.Context, atom string, src *dagger.Directory, repo string, dev bool, tree string, args []string, fresh bool) (*dagger.Directory, error) {
	e, ok := regenEntry(atom)
	if !ok {
		return nil, fmt.Errorf("atom %s is not in the generated registry", atom)
	}
	r := newRun(src, repo, "").reasked(devlane.Nonce(fresh, time.Now().UnixNano()))
	r.dev, r.tree, r.args = dev, tree, args
	ran, out, v := logicRan(ctx, r, e)
	if v == nil {
		read := readVerdict(ctx, checks.AtomByID(atom), ran, out)
		v = &read
	}
	if v.State != 0 {
		reason := strings.Join(append([]string{v.Reason}, v.Logs...), "\n")
		// the atom's 2 is the lane's could-not-run, which SettleCode reads as 126 and up
		code := v.State
		if code >= 2 {
			code = 126
		}
		if err := (devRan{code: code}).settle(ctx, reason); err != nil {
			return nil, err
		}
		return nil, errors.New(reason)
	}
	return src.Diff(ran.Directory("/src")), nil
}
