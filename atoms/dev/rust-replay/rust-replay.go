// Package devrustreplay is the atom dev:rust-replay, the dev verb `replay`: what
// `just replay` runs in stellar-core-rust and what rust:wit-compose runs in the
// gate. Every wit-guest world composes into one component and the tapes replay
// through it. It is a logic atom over the same body as the gate atom
// (internal/witreplay, R36), and differs only in what it reports: the tally.
package devrustreplay

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/witreplay"
)

// Def is the atom. Stage dev, so the gate never grades it: `foundry-toolbox ask
// dev replay` asks for it by name (dev:<lane>-<verb>).
//
// WritesTree STAYS TRUE, as rust:wit-compose's is. The shared body dates the tree
// (atoms.RustStamp, checks.Unstale) so cargo builds THIS tree and not an older
// artifact; that touches mtimes under /src, so the tree must be a copy the atom
// may write. The cargo target is already outside the tree (CARGO_TARGET_DIR), and
// the stamp changes no byte, so the dev lane delivers no change for it ("no
// change", not a patch).
//
// IT TAKES NO ARGUMENTS, BY DESIGN. The replay's worlds are declared, not chosen:
// compose.sh takes a world subset (`compose.sh OUT <worlds>`), but the gate
// composes the whole fleet, and a subset would compose a different component than
// the one the gate grades. `ask dev replay` answers the gate's question and
// nothing narrower; any arguments the request carries are ignored.
var Def = atomkit.Def{
	ID: id, Stage: stage, Lane: checks.LaneRust,
	Group:         checks.GroupLanguage,
	Desc:          "Replay every wit-guest world's tape through the fleet composition, run by hand from the dev lane: the same body as rust:wit-compose (build the replay host, tools/compose/compose.sh, tests/tapes/<world>.json, then tests/red/*.json); the reason carries the TOTAL tally and the tape count, the logs every per-tape line.",
	Surface:       "root:Cargo.toml",
	Toolchain:     "rust",
	Needs:         []string{"tool:wasm-tools", "tool:wac"},
	WritesTree:    true,
	NeedsManifest: true,
	Timeout:       45 * time.Minute,
	Check:         check,
}

const (
	id    = "dev:rust-replay"
	stage = "dev"
)

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: stage, Lane: checks.LaneRust}

// tapeLine is one tape's line from the replay host: `<tape>: P/N pass, F fail`.
var tapeLine = regexp.MustCompile(`^\S+: \d+/\d+ pass, \d+ fail$`)

// check runs the shared body and, when the replay got as far as its tally, leads
// the reason with it. The state is the replay's exit, untouched; the logs are the
// replay's output, every per-tape line among them.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	v := witreplay.Run(ctx, in, row)
	total, tapes := "", 0
	for _, ln := range v.Logs {
		switch {
		case strings.HasPrefix(ln, "TOTAL "):
			total = ln
		case tapeLine.MatchString(ln):
			tapes++
		}
	}
	if total == "" || v.Result == "absent" {
		return v
	}
	tally := fmt.Sprintf("%s: %s over %d tapes", id, total, tapes)
	if v.State == int(checks.StatePass) {
		v.Reason = tally
	} else {
		v.Reason = tally + "\n" + v.Reason
	}
	return v
}
