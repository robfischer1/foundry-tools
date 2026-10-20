package atomkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// In is what an atom's Check reads: internal/atoms' Input, whose population
// fields tree@1 carries. It is an alias until internal/atoms is deleted, and
// its own copy after (lld.md FN-080).
type In = atoms.Input

// The seams Main runs through, so a test can run it: the exit, the directory
// the pod's /work is, the clock, and the streams.
var (
	exit               = os.Exit
	workRoot           = "/work"
	now                = time.Now
	stdout   io.Writer = os.Stdout
	stderr   io.Writer = os.Stderr
)

// defaultRoot is where the pod mounts the tree.
const defaultRoot = "/src"

// reasonCap is the longest reason a verdict keeps, in bytes, before the ellipsis (E6).
const reasonCap = 2000

// Main is every logic atom's main(): `atom --describe` prints its index entry,
// and `atom [root [arg...]]` runs its Check over the tree and writes the verdict
// to ATOM_OUT. The arguments after the root are the dev request's, which a dev
// atom reads from In.Args (PlanEntry.Args, appended to the command line). It exits with the verdict's state (lld.md FN-079). Run through a link
// named for one of the helpers the mutation atoms carry (atoms.RunHelper) it is
// that helper instead.
func Main(d Def) {
	if code, ok := atoms.RunHelper(os.Args); ok {
		exit(code)
		return
	}
	exit(run(d, os.Args))
}

func run(d Def, args []string) int {
	if len(args) > 1 && args[1] == "--describe" {
		b, err := Describe(d)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintf(stdout, "%s\n", b)
		return 0
	}
	root := defaultRoot
	if len(args) > 1 {
		root = args[1]
	}
	out := os.Getenv("ATOM_OUT")
	if out == "" {
		out = workRoot + "/out/" + EscapeID(d.ID) + ".json"
	}
	var extra []string
	if len(args) > 2 {
		extra = args[2:]
	}
	v := verdictFor(d, root, extra)
	if err := WriteVerdict(out, v); err != nil {
		fmt.Fprintf(stderr, "atomkit: verdict unwritable: %v\n", err)
		return 2
	}
	return v.State
}

// verdictFor loads the tree, runs the Check under its deadline and fills in
// what the atom may leave empty: its identity, its lines and its times.
func verdictFor(d Def, root string, args []string) checks.Verdict {
	started := now()
	var v checks.Verdict
	if in, err := LoadIn(root); err != nil {
		v = cannotRun(d, err.Error())
	} else {
		in.Args = args
		v = check(d, in)
	}
	if v.Atom == "" {
		v.Atom = d.ID
	}
	if v.Stage == "" {
		v.Stage = d.Stage
	}
	if v.Lane == "" {
		v.Lane = string(d.Lane)
	}
	if v.StartedAt == "" {
		v.StartedAt = started.UTC().Format(checks.TimeLayout)
	}
	if v.FinishedAt == "" {
		v.FinishedAt = now().UTC().Format(checks.TimeLayout)
	}
	return v
}

// check runs d.Check under d.Timeout (600 s when 0). A panic is a could-not-run
// naming it; a Check still running at the deadline is abandoned, since the
// process is about to exit with the verdict.
func check(d Def, in In) checks.Verdict {
	limit := d.Timeout
	if limit == 0 {
		limit = defaultTimeout
	}
	if d.Deadline != nil {
		limit = max(limit, d.Deadline(in))
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	done := make(chan checks.Verdict, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- cannotRun(d, fmt.Sprintf("panic: %v", r))
			}
		}()
		done <- d.Check(ctx, in)
	}()
	select {
	case v := <-done:
		return v
	case <-ctx.Done():
		return cannotRun(d, fmt.Sprintf("deadline %gs passed", limit.Seconds()))
	}
}

// cannotRun is the verdict for an atom that could not judge (E6).
func cannotRun(d Def, reason string) checks.Verdict {
	return CannotRun(d.ID, d.Stage, d.Lane, reason)
}

// CannotRun is cannotRun for a caller that holds an atom's identity and not its
// Def: the Dagger adapter, which answers it when an atom did not compile or
// wrote no verdict.
func CannotRun(id, stage string, lane checks.Lane, reason string) checks.Verdict {
	return checks.Verdict{
		Atom: id, Stage: stage, Lane: string(lane),
		State: int(checks.StateCannotRun), Result: checks.StateCannotRun.String(),
		Reason: oneLine(reason), Logs: []string{},
	}
}

// oneLine makes a reason one line: every \n becomes a space, and a reason over
// 2000 bytes is cut there, on a rune boundary, with "…" appended (E6).
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= reasonCap {
		return s
	}
	cut := reasonCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// LoadIn is the Input an atom reads (lld.md FN-080): from the tree facts
// ATOM_TREE names when it is set (its Root is replaced by root, the checkout
// the atom runs over), else computed from the checkout at root
// when git is on PATH, with ATOM_BASE, ATOM_ORIGIN and ATOM_FORK as the
// change set's base, the origin URL and the fork record (all optional).
func LoadIn(root string) (In, error) {
	var in In
	if p := os.Getenv("ATOM_TREE"); p != "" {
		t, err := ReadTree(p)
		if ve := new(VersionError); errors.As(err, &ve) {
			return In{}, fmt.Errorf("tree@1 version %d", ve.Got)
		} else if err != nil {
			return In{}, err
		}
		in = inOf(t)
		// The root the atom was run at is the tree it reads and writes. The facts
		// were collected once over /src; a writes_tree atom runs over its own copy
		// (/work/<id>/src) while /src is read-only (DEF-B4-writes-tree). The
		// Dagger adapter runs at /src, which is the facts' root: nothing moves.
		in.Root = root
	} else {
		if _, err := exec.LookPath("git"); err != nil {
			return In{}, errors.New("no tree facts and no git")
		}
		// No tree facts: the checkout is read here, against the base, origin and
		// fork the caller named (the Dagger adapter has no tree@1 to hand over).
		in = atoms.Collect(context.Background(), root, os.Getenv("ATOM_BASE"), os.Getenv("ATOM_ORIGIN"), now())
		in.Fork = os.Getenv("ATOM_FORK")
	}
	in.Dies = os.Getenv("ATOM_DIES")
	in.Spire = strings.TrimPrefix(os.Getenv("SPIFFE_ENDPOINT_SOCKET"), "unix://")
	in.Now = now()
	in.Door = checks.Door{Base: os.Getenv("ATOM_DOOR")}
	return in, nil
}

// WriteVerdict writes v to path as atom-verdict@1: one JSON object and a
// newline, written atomically (lld.md FN-081). Logs is [] when the atom left it
// nil: an atom that printed nothing says so (checks.Verdict.Logs).
func WriteVerdict(path string, v checks.Verdict) error {
	if v.Logs == nil {
		v.Logs = []string{}
	}
	return WriteFile(path, v)
}
