// Package diesauthzgrid is the atom dies:authz-grid: a pull's Rego gives every cell the authz_v2
// grids enumerate the answer the base's Rego does, over the same data.
package diesauthzgrid

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom. It writes the tree: the checker lays copies of it and unlinks
// files in them (base_tree), and shutil.copytree keeps modes, so a read-only
// tree would give it read-only copies.
var Def = atomkit.Def{
	ID: "dies:authz-grid", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:      checks.GroupLanguage,
	Desc:       "A pull that changes policy Rego moves no answer of the authz_v2 decision, decide or endpoint_decision grids: the pull's Rego and the base's are asked every cell over the head's data with time.now_ns pinned, and the first cells that moved are listed unless a commit carries an Authz-Semantics-Change trailer. A pull that touches no Rego passes unevaluated.",
	Surface:    "any:**",
	Toolchain:  "python",
	Needs:      []string{"tool:git", "tool:opa", "tool:uv"},
	WritesTree: true,
	Check:      check,
}

const id = "dies:authz-grid"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

const (
	// checker is the tree's own, and its exit is the verdict, unmapped (0 holds,
	// 1 an answer moved, 2 could not run).
	checker = "tools/check_authz_grid.py"
	// listed is how many moved cells per plane the verdict names.
	listed = "20"
	// trailer is the acknowledgement: a commit of the pull that carries it has
	// made a change of meaning on purpose. The door parses no trailer but Serves, so
	// this is the atom's own convention, read from the pull's commits.
	trailer = "Authz-Semantics-Change"
)

// check judges the pull's Rego against the base's, both over the head's data.
// There is no stored golden: an approved grant is supposed to move decisions, so
// the question is whether the RULES moved them, and a pull that touches no .rego
// is answered by the checker without evaluating anything.
//
// The atom does the git: the merge base (the same resolve the change set uses)
// and the acknowledgement trailers of the commits since it, handed to the checker
// as --base and --ack, because the checker reads objects and runs no revision
// command. With no base the tip is read against its parent. A base that will not
// resolve is a could-not-run: a comparison nobody made is not one that passed.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{checker, checker + " is absent, so there is no checker to run."},
	}); stop != nil {
		return *stop
	}
	if stop := dieskit.OpaProvisioned(ctx, row, in); stop != nil {
		return *stop
	}
	since, err := baseOf(ctx, in)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+err.Error())
	}
	acks, err := acknowledgements(ctx, in, since)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+err.Error())
	}
	args := []string{checker, "--first", listed, "--base", since}
	for _, a := range acks {
		args = append(args, "--ack", a)
	}
	return dieskit.Verdict(ctx, row, in, "", args...)
}

// baseOf is the commit the pull branched from: the merge base of the run's base
// and the tip, or the tip's parent when the run names no base. A base the history
// does not reach is a could-not-run.
func baseOf(ctx context.Context, in atomkit.In) (string, error) {
	if in.Base == "" {
		return "HEAD~1", nil
	}
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "git", Args: []string{
		"-c", "safe.directory=*", "merge-base", in.Base, "HEAD"}})
	if code != 0 || out == "" {
		return "", fmt.Errorf("the base %s is not in this history (git merge-base exited %d): %s", in.Base, code, out)
	}
	return strings.TrimSpace(out), nil
}

// acknowledgements are the Authz-Semantics-Change values on the commits since the
// base, one per trailer.
func acknowledgements(ctx context.Context, in atomkit.In, since string) ([]string, error) {
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "git", Args: []string{
		"-c", "safe.directory=*", "log",
		"--format=%(trailers:key=" + trailer + ",valueonly,separator=%x1f)", since + "..HEAD"}})
	if code != 0 {
		return nil, fmt.Errorf("the commits since %s could not be read for a %s trailer (git log exited %d): %s", since, trailer, code, out)
	}
	var acks []string
	for _, line := range strings.Split(out, "\n") {
		for _, v := range strings.Split(line, "\x1f") {
			if v = strings.TrimSpace(v); v != "" {
				acks = append(acks, v)
			}
		}
	}
	return acks, nil
}
