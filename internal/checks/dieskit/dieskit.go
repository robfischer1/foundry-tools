// Package dieskit is what the dies atoms (atoms/dies/<name>) share: the shape
// every one of them asks first, the pinned opa the four that grade the policy
// run, the bundle two of them interrogate, and the uv-resolved python the
// tree's own checkers run under. A helper two or more atoms use is written
// once; the question each atom asks stays in its own file.
package dieskit

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Shape is the condition every dies: atom shares. TWO MARKERS, BOTH REQUIRED
// (policy/.manifest and fleet/repos/): policy/ turns up in more than one repo
// and fleet/repos/ is a name an inventory could take. The root listing is read
// first so a missing marker is an absence and not a descent into a directory
// that is not there. Nil means the tree is the die's source.
func Shape(t atoms.Tree, a checks.AtomDef) *checks.Verdict {
	absent := checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree is not the policy die's source. It needs both policy/.manifest and fleet/repos/, and this one does not carry both.")
	roots, err := t.Entries(".")
	if err != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the repository root could not be read (%v), so this tree's shape is unknown rather than wrong.", a.ID, err))
		return &v
	}
	// policy/ is not asked of the listing: the manifest read below says it, and
	// a policy/ without the manifest is the same absence.
	if !checks.HasEntry(roots, "fleet") {
		return &absent
	}
	if _, err := t.Read("policy/.manifest"); err != nil {
		return &absent
	}
	fleet, err := t.Entries("fleet")
	if err != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - fleet/ is in the root listing but could not be read (%v).", a.ID, err))
		return &v
	}
	if !checks.HasEntry(fleet, "repos") {
		return &absent
	}
	return nil
}

// opaVersion proves the opa on PATH runs and is the PINNED one. The rego
// language version is a property of the binary: a suite written for v1 graded by
// another major answers a different question, so a mirror that served something
// else cannot silently supply a different language.
func opaVersion(ctx context.Context, in atomkit.In) error {
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "opa", Args: []string{"version"}})
	if code != 0 {
		return fmt.Errorf("opa is on disk but does not run (exit %d): %s", code, out)
	}
	if !checks.OpaVersionOK(out, checks.OpaVersion) {
		return fmt.Errorf("the binary on disk does not answer %q, so the rego semantics are not the pinned ones: %s", "Version: "+checks.OpaVersion, out)
	}
	return nil
}

// OpaProvisioned is opaVersion with the atom's own refusal attached: nil when
// the opa on PATH is the pinned one.
func OpaProvisioned(ctx context.Context, a checks.AtomDef, in atomkit.In) *checks.Verdict {
	err := opaVersion(ctx, in)
	if err == nil {
		return nil
	}
	v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. A policy suite that never ran is not a policy suite that passed.", a.ID, err))
	return &v
}

// Bundle builds the artifact and takes its data document out, because THE
// SOURCE TREE IS NOT A PROXY FOR THE ARTIFACT: `opa test policy/` passes on a
// tree whose BUILT BUNDLE is empty. A build that did not complete is a 2; a
// bundle with no data.json is a 1 (the defect arriving as described). The
// revision is best-effort, stamped into the manifest and graded by nothing.
//
// THE BUNDLE IS WHERE THE CALLER SAYS and the caller removes it: the canary
// atom interrogates the same file again, and two atoms run at once in one
// process, so a name shared between them (the pid alone was one) is a bundle
// one atom reads while the other rewrites it.
func Bundle(ctx context.Context, a checks.AtomDef, in atomkit.In, bundle string) (string, *checks.Verdict) {
	rev := "unknown"
	if out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "git", Args: []string{"-c", "safe.directory=*", "rev-parse", "HEAD"}}); code == 0 && out != "" {
		rev = out
	}
	if out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "opa", Args: []string{
		"build", "-b", "policy/", "-o", bundle, "--revision", rev, "--ignore", "*_test.rego"}}); code != 0 {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - opa build did not produce a bundle, so there is no artifact to interrogate.\nopa build exited %d: %s", a.ID, code, out))
		return "", &v
	}
	data, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "tar", Args: []string{"xzOf", bundle, "/data.json"}})
	if code != 0 {
		v := checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - the built bundle carries NO data.json member at all. Bundle mode reads only files literally named data.json, so a tree using arbitrary JSON names tests green and ships an empty data document - star_only undefined, the visibility comprehension collecting nothing, every verb visible to every principal.\n"+data)
		return "", &v
	}
	return data, nil
}
