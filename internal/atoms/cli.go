package atoms

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"
)

// Run is the atoms binary's process: parse the flags, read the tree once, run
// the registry, print the vector as JSON on stdout. It answers the exit code.
//
// IT EXITS 0 WHATEVER THE VECTOR SAYS. The vector is the result, and a verdict
// of 2 inside it is a result; a non-zero exit would mean "the program broke",
// which the consumer must be able to tell apart from "an atom found something".
// The only non-zero exit is 2, for a bad flag, an invalid registry or a stage
// no atom belongs to — the consumer then gets no JSON to mistake for a vector.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) int {
	return run(ctx, args, stdout, stderr, now, func() (*Registry, error) { return NewRegistry(Builtin()...) })
}

// run is Run with the registry supplied, so the one way building it can fail is
// reachable from a test.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time, build func() (*Registry, error)) int {
	fs := flag.NewFlagSet("atoms", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "the repository root")
	base := fs.String("base", "", "the change set's base sha; empty reads HEAD against its parent")
	origin := fs.String("origin", "", "where the repository was fetched from; empty asks git for origin")
	stage := fs.String("stage", "", "the stage whose atoms to run (precommit, prepush, orbit); empty is the pull path, precommit and prepush")
	dies := fs.String("dies", "", "a checkout of foundry-dies at main, for the atoms that grade against the fleet's contracts")
	spire := fs.String("spire", "", "the SPIRE agent socket the lane pod forwarded, for fleet:witness's identified ask; empty asks in the clear")
	narcErr := fs.String("narc-err", "", "why narc was not provisioned; empty means it was")
	timeout := fs.Duration("timeout", DefaultTimeout, "each atom's deadline")
	workers := fs.Int("workers", 0, "worker pool size; 0 is GOMAXPROCS")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	reg, err := build()
	if err != nil {
		fmt.Fprintln(stderr, "atoms: the registry is invalid:", err)
		return 2
	}
	// THE STAGE SELECTS WHAT RUNS, the way the module's AtomsForStage selects
	// what a lane grades, so a lane's shadow compares exactly its own atoms.
	reg, err = reg.ForStage(*stage)
	if err != nil {
		fmt.Fprintln(stderr, "atoms:", err)
		return 2
	}
	in := Collect(ctx, *root, *base, *origin, now())
	in.Dies = *dies
	in.Spire = *spire
	in.NarcErr = *narcErr
	vector := Execute(ctx, reg, in, Options{Workers: *workers, Timeout: *timeout, Clock: now})

	// THE MARSHAL ERROR IS DROPPED, not branched on (gate.go says why): Verdict
	// is strings, ints, bools and slices of the same, so json cannot fail on it,
	// and a branch no test can take is a branch that should not exist.
	b, _ := json.MarshalIndent(vector, "", "  ")
	fmt.Fprintln(stdout, string(b))
	return 0
}
