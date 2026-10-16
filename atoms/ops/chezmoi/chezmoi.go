// Package opschezmoi is the atom ops:chezmoi: every tracked chezmoi template
// renders. One file holds the atom and both runners execute it (lld.md FN-085).
package opschezmoi

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. chezmoi is the pinned binary the tools container carries.
var Def = atomkit.Def{
	ID: "ops:chezmoi", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every tracked chezmoi *.tmpl renders with `chezmoi execute-template` — the one check a dotfiles source tree has.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:chezmoi"},
	Check:     check,
}

const id = "ops:chezmoi"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: every tracked *.tmpl of a chezmoi source tree renders, the check a
// dotfiles tree has no other way to make.
//
// `--source .` IS LOAD-BEARING: chezmoi resolves `include` against its SOURCE
// directory, which defaults to ~/.local/share/chezmoi. Rendering has no host
// config, so a template that reads custom data fails here and works on the host
// that has it; none does today.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if stop := atomlib.OpsSurface(row, in); stop != nil {
		return *stop
	}
	templates := checks.OpsChezmoiTemplates(checks.OpsFiles(in.Committable))
	if len(templates) == 0 {
		return atomlib.PhaseAbsent(row, "no chezmoi template in this tree")
	}
	if stop := atomlib.ProbeTool(ctx, row, in, "chezmoi", "--version"); stop != nil {
		return *stop
	}
	t := in.Tree()
	var out strings.Builder
	rc := 0
	for _, tmpl := range templates {
		src, err := t.Read(tmpl)
		if err != nil {
			return atomlib.NeverRan(row, err.Error())
		}
		o, c := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "chezmoi", Args: []string{"--source", ".", "execute-template"}, Stdin: src, Both: true})
		if c < 0 {
			return atomlib.NeverRan(row, o)
		}
		if c != 0 {
			rc = 1
			fmt.Fprintf(&out, "FAIL %s\n    %s\n", tmpl, strings.ReplaceAll(strings.TrimRight(o, "\n"), "\n", "\n    "))
		}
	}
	fmt.Fprintf(&out, "chezmoi: %d template(s) checked", len(templates))
	state, report := atomlib.OpsSettled("chezmoi", rc, out.String())
	return checks.VerdictOf(row, state, report)
}
