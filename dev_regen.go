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
	e, ok := regenEntry(regenAtom)
	if !ok {
		return nil, fmt.Errorf("atom %s is not in the generated registry", regenAtom)
	}
	r := newRun(d.Source, d.Repo, "").reasked(devlane.Nonce(fresh, time.Now().UnixNano()))
	r.dev, r.tree, r.args = true, d.Tree, args
	ran, out, v := logicRan(ctx, r, e)
	if v == nil {
		read := readVerdict(ctx, checks.AtomByID(regenAtom), ran, out)
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
	return d.Source.Diff(ran.Directory("/src")), nil
}
