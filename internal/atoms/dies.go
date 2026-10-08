package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE DIES ATOMS. foundry-dies is the policy die's source: the repo that owns
// the policy bundle and the star roster it is built from. Every atom here asks
// first whether the tree IS that repo (diesShape) and is ABSENT, saying why,
// anywhere else.

// diesShape is the condition every dies: atom shares. TWO MARKERS, BOTH
// REQUIRED (policy/.manifest and fleet/stars/): policy/ turns up in more than one
// repo and fleet/stars/ is a name an inventory could take. The root listing is
// read first so a missing marker is an absence and not a descent into a
// directory that is not there. Nil means the tree is the die's source.
func diesShape(t tree, a checks.AtomDef) *checks.Verdict {
	absent := checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree is not the policy die's source. It needs both policy/.manifest and fleet/stars/, and this one does not carry both.")
	roots, err := t.entries(".")
	if err != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the repository root could not be read (%v), so this tree's shape is unknown rather than wrong.", a.ID, err))
		return &v
	}
	if !checks.HasEntry(roots, "policy") || !checks.HasEntry(roots, "fleet") {
		return &absent
	}
	if _, err := t.read("policy/.manifest"); err != nil {
		return &absent
	}
	fleet, err := t.entries("fleet")
	if err != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - fleet/ is in the root listing but could not be read (%v).", a.ID, err))
		return &v
	}
	if !checks.HasEntry(fleet, "stars") {
		return &absent
	}
	return nil
}

// present answers whether the tree carries a path (or a pattern's match).
func present(t tree, path string) bool { return len(t.glob(path)) > 0 }

// requirePaths is the run of `must be there or CANNOT RUN` each atom opens with;
// each entry keeps its own sentence, which says what cannot be claimed without
// that file.
func requirePaths(t tree, a checks.AtomDef, required [][2]string) *checks.Verdict {
	for _, req := range required {
		if !present(t, req[0]) {
			v := checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+req[1])
			return &v
		}
	}
	return nil
}

// opaVersion proves the opa on PATH runs and is the PINNED one. The rego
// language version is a property of the binary: a suite written for v1 graded by
// another major answers a different question, so a mirror that served something
// else cannot silently supply a different language.
func opaVersion(ctx context.Context, in Input) error {
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opa", Args: []string{"version"}})
	if code != 0 {
		return fmt.Errorf("opa is on disk but does not run (exit %d): %s", code, out)
	}
	if !checks.OpaVersionOK(out, checks.OpaVersion) {
		return fmt.Errorf("the binary on disk does not answer %q, so the rego semantics are not the pinned ones: %s", "Version: "+checks.OpaVersion, out)
	}
	return nil
}

// diesBundle builds the artifact and takes its data document out, because THE
// SOURCE TREE IS NOT A PROXY FOR THE ARTIFACT: `opa test policy/` passes on a
// tree whose BUILT BUNDLE is empty. A build that did not complete is a 2; a
// bundle with no data.json is a 1 (the defect arriving as described). The
// revision is best-effort, stamped into the manifest and graded by nothing.
func diesBundle(ctx context.Context, a checks.AtomDef, in Input) (string, *checks.Verdict) {
	rev := "unknown"
	if out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"-c", "safe.directory=*", "rev-parse", "HEAD"}}); code == 0 && out != "" {
		rev = out
	}
	bundle := filepath.Join(os.TempDir(), fmt.Sprintf("dies-bundle-%d.tar.gz", os.Getpid()))
	defer func() { _ = os.Remove(bundle) }()
	if out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opa", Args: []string{
		"build", "-b", "policy/", "-o", bundle, "--revision", rev, "--ignore", "*_test.rego"}}); code != 0 {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - opa build did not produce a bundle, so there is no artifact to interrogate.\nopa build exited %d: %s", a.ID, code, out))
		return "", &v
	}
	data, code := in.run(ctx, Cmd{Dir: in.Root, Name: "tar", Args: []string{"xzOf", bundle, "/data.json"}})
	if code != 0 {
		v := checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - the built bundle carries NO data.json member at all. Bundle mode reads only files literally named data.json, so a tree using arbitrary JSON names tests green and ships an empty data document - star_only undefined, the visibility comprehension collecting nothing, every verb visible to every principal.\n"+data)
		return "", &v
	}
	return data, nil
}

// diesDataKeys: the BUILT bundle carries every data root the policy reads,
// non-empty. A data.json that will not parse is a 2: a shape the atom cannot
// read is a could-not-run, not a count nobody computed.
func diesDataKeys(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := diesShape(in.tree(), a); stop != nil {
		return *stop
	}
	if err := opaVersion(ctx, in); err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. A policy suite that never ran is not a policy suite that passed.", a.ID, err))
	}
	data, stop := diesBundle(ctx, a, in)
	if stop != nil {
		return *stop
	}
	missing, stars, err := checks.DiesDataKeys(data)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the built bundle's data.json is a shape this atom cannot read (%v), so there is no data document to grade.", a.ID, err))
	}
	if len(missing) > 0 {
		return checks.VerdictOf(a, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - ::error::bundle data.json is missing/empty: %v. Bundle mode reads ONLY files named data.json - check the policy/<root>/data.json layout.", a.ID, missing))
	}
	return checks.VerdictOf(a, int(checks.StatePass), fmt.Sprintf("%s: data roots ok; star_only carries %d stars", a.ID, stars))
}

// diesCanonical grades the FORM of every committed record: each
// fleet/stars/<star>/slag.json re-emitted through checks.CanonicalJSON must come
// back byte for byte. A record that does not parse is a finding too.
func diesCanonical(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	if stop := diesShape(t, a); stop != nil {
		return *stop
	}
	records := t.glob("fleet/stars/*/slag.json")
	if len(records) == 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - fleet/stars/ carries no slag.json, so there is no record whose form could be graded.")
	}
	sort.Strings(records)
	var findings []string
	for _, p := range records {
		committed, err := t.read(p)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s could not be read (%v).", a.ID, p, err))
		}
		canon, err := checks.CanonicalJSON([]byte(committed))
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: not one JSON document (%v)", p, err))
			continue
		}
		if string(canon) != committed {
			findings = append(findings, p+": diverges from its canonical form — re-emit it with json.dumps(doc, sort_keys=True, indent=2) + \"\\n\" (keys sorted at every depth, two-space indent, non-ASCII escaped, one trailing LF)")
		}
	}
	if len(findings) > 0 {
		return checks.VerdictOf(a, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - %d of %d record(s) are not their own canonical form:\n%s", a.ID, len(findings), len(records), strings.Join(findings, "\n")))
	}
	return checks.VerdictOf(a, int(checks.StatePass), fmt.Sprintf("%s: %d record(s) are each their own canonical form.", a.ID, len(records)))
}
