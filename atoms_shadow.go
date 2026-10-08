package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE SHADOW OF THE ATOMS BINARY. internal/atoms runs the cheap fleet atoms
// in one process; before any repository's verdict is allowed to come from it,
// CI runs it BESIDE the chains over the same tree and reads whether the two
// agree. This file is that run, and it is built so that it CANNOT vote:
//
//   - It is its own function, not a stage. gate, gate-file, Verdicts and Push
//     never call it, so no record, vector or exit a door reads contains it.
//   - It RETURNS A STRING, with no error: a chain that would not evaluate, a
//     binary that would not build, an output that will not parse are all lines
//     of the report, and the function still answers. The caller prints it.
//   - It reuses the lane's chains for the "today" side (m.vector, selected by
//     id), so the engine's cache serves them when the gate ran the same tree.

// atomsSourceInclude is the part of this module the atoms binary is built
// from, and nothing else. The chains' helper binaries mount
// dag.CurrentModule().Source() whole (moduleBinary: standard library only,
// GOPROXY=off, cold), which makes the build's cache key a function of every
// file in the module — an edit to a README rebuilds it. This binary takes a
// real dependency (the YAML parser), so it is built in goToolchain() with the
// Go cache volumes mounted, from go.mod, go.sum and the packages it imports.
// TestAtomsSourceCoversImports holds this list to the import closure: a
// package the binary imports and this list omits is a build that fails in the
// engine and nowhere a test can see.
var atomsSourceInclude = []string{
	"go.mod", "go.sum",
	"atoms/**",
	"internal/atoms/**",
	"internal/checks/**",
	"internal/execmem/**",
	"internal/unitkey/**",
}

// atomsBinPath is where the binary sits in the fleet lane's container.
const atomsBinPath = "/usr/local/bin/atoms"

// atomsBinary builds ./atoms from the filtered source. Tests are excluded: they
// are not compiled into the binary, and a test edit must not rebuild it.
func atomsBinary() *dagger.File {
	src := dag.CurrentModule().Source().Filter(dagger.DirectoryFilterOpts{
		Include: atomsSourceInclude,
		Exclude: []string{"**/*_test.go"},
	})
	return goToolchain().
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-trimpath", "-o", "/out/atoms", "./atoms"}).
		File("/out/atoms")
}

// ShadowAtoms runs the in-process atoms beside the same atoms as Dagger chains
// and reports whether they agree. NON-VOTING: it returns text and nothing that
// a gate reads, and it answers even when either side could not run.
//
// +cache="never"
func (m *FoundryTools) ShadowAtoms(
	ctx context.Context,
	// The change set's base, as Verdicts takes it. Empty reads HEAD against
	// its parent.
	// +optional
	base string,
) string {
	var ids []string
	for _, a := range atoms.Builtin() {
		ids = append(ids, a.ID)
	}
	today, todayErr := shadowToday(ctx, m, strings.Join(ids, ","), base)
	raw, rawErr := shadowBinary(ctx, m, base)
	return renderShadow(today, todayErr, raw, rawErr)
}

// The two sides of the comparison, as variables so a test names what each
// answers without an engine.
var (
	shadowToday = func(ctx context.Context, m *FoundryTools, only, base string) ([]checks.Verdict, error) {
		return m.vector(ctx, "", only, base)
	}
	shadowBinary = func(ctx context.Context, m *FoundryTools, base string) (string, error) {
		return m.atomsVector(ctx, base)
	}
)

// atomsVector runs the binary in the fleet lane image over the same tree the
// chains read, and answers its stdout: the vector as JSON.
//
// THE LANE IS THE CHAINS' OWN (r.lane, r.gitReady, r.withBase), so the binary
// sees the tree and the history the chains see — including a linked worktree's
// rebuilt repository and the base fetched by sha. A non-zero exit is an error
// here, not a vector: the binary exits 0 whatever its atoms found.
func (m *FoundryTools) atomsVector(ctx context.Context, base string) (string, error) {
	r := newRun(m.Source, m.Repo, base).fromOrigin(m.Origin)
	ctr := r.gitReady(ctx, r.withBase(r.lane(checks.ImageFleet))).
		WithFile(atomsBinPath, atomsBinary(), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithExec([]string{atomsBinPath, "-root", "/src", "-base", base, "-origin", r.repo}, anyExit)
	out, code, err := output(ctx, ctr)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("the atoms binary exited %d: %.300s", code, out)
	}
	return out, nil
}

// renderShadow is the report from the two sides' answers. It is pure so the
// ways either side can fail are tested without an engine.
func renderShadow(today []checks.Verdict, todayErr error, raw string, rawErr error) string {
	if todayErr != nil {
		return "shadow atoms: not compared - the chains did not answer: " + todayErr.Error()
	}
	if rawErr != nil {
		return "shadow atoms: not compared - the binary did not answer: " + rawErr.Error()
	}
	shadow, err := checks.ParseVector(raw)
	if err != nil {
		return "shadow atoms: not compared - " + err.Error()
	}
	return atoms.Compare(today, shadow).Render()
}

// shadowTimeout bounds the shadow run, its own deadline apart from the gate's.
// A variable so a test can hang the shadow without waiting out five minutes.
var shadowTimeout = 5 * time.Minute

// shadowRun is the shadow itself, a variable so the tests can make it hang,
// panic or answer garbage and prove the gate does not notice.
var shadowRun = func(ctx context.Context, m *FoundryTools, base string) string {
	return m.ShadowAtoms(ctx, base)
}

// shadowOut is where the report goes: stderr, read at call time. Never the
// record and never the exit.
var shadowOut = func() io.Writer { return os.Stderr }

// shadowBeside runs the shadow after the gate's record is settled, and says
// what it found on stderr. IT CANNOT VOTE: it returns nothing, runs under its
// own deadline, recovers a panic into a line of the report, and is called only
// after the record exists (Gate) or has been posted (GateFile). The pull path
// only - the mutation, orbit and visual lanes have no atoms in the binary.
func (m *FoundryTools) shadowBeside(ctx context.Context, stage, base string) {
	if laneOf(stage) != "gate" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, shadowTimeout)
	defer cancel()
	// Buffered: a shadow that answers after its deadline can still return.
	done := make(chan string, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Sprintf("shadow atoms: not compared - the shadow panicked: %v", p)
			}
		}()
		done <- shadowRun(ctx, m, base)
	}()
	var report string
	select {
	case report = <-done:
	case <-ctx.Done():
		report = fmt.Sprintf("shadow atoms: not compared - no answer within %s (%v)", shadowTimeout, ctx.Err())
	}
	fmt.Fprintln(shadowOut(), report)
}
