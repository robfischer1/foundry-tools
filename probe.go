package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// Probe reports the shape of .git in the Source the caller handed this module
// — a file (a linked worktree's pointer), a directory, and what git can read.
// A measurement function for foundry-tools#gitdir; not a stage.
func (m *FoundryTools) Probe(ctx context.Context) (string, error) {
	var out []string
	if c, err := m.Source.File(".git").Contents(ctx); err == nil {
		out = append(out, ".git is a FILE: "+strings.TrimSpace(c))
	} else {
		out = append(out, ".git file read: "+err.Error())
	}
	if e, err := m.Source.Directory(".git").Entries(ctx); err == nil {
		out = append(out, ".git dir entries: "+strings.Join(e, " "))
		for _, f := range []string{"commondir", "gitdir", "HEAD"} {
			if c, err := m.Source.File(".git/" + f).Contents(ctx); err == nil {
				out = append(out, ".git/"+f+" = "+strings.TrimSpace(c))
			}
		}
	} else {
		out = append(out, ".git dir read: "+err.Error())
	}
	ctr := dag.Container().From(checks.ImageFleet).
		WithMountedDirectory("/src", m.Source).WithWorkdir("/src").
		WithExec([]string{"sh", "-c", "git config --global --add safe.directory '*'; git rev-parse --verify HEAD; echo rc=$?; git rev-list --count HEAD; echo rc=$?; ls .git/objects 2>&1 | head -3"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	o, _ := ctr.Stdout(ctx)
	e, _ := ctr.Stderr(ctx)
	out = append(out, "git: "+o+" | "+e)
	return strings.Join(out, "\n"), nil
}
