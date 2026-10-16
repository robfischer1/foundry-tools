// Package composeconfig is the atom compose:config: every tracked compose spec
// parses and its schema validates. One file holds the atom and both runners
// execute it (lld.md FN-085).
package composeconfig

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. docker-compose is the pinned v2 client the tools container
// carries; `config` is a client-side parse.
var Def = atomkit.Def{
	ID: "compose:config", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every tracked compose spec parses and its schema validates.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:docker-compose"},
	Check:     check,
}

const id = "compose:config"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: every tracked compose spec parses and its schema validates.
//
// --no-interpolate IS LOAD-BEARING: the specs use ${VAR:?message} to make a
// missing variable a DEPLOY-TIME error, which is fatal anywhere no variable is
// set. THE env_file TARGETS ARE STUBBED FIRST (compose hard-errors on a missing
// one before it reaches the schema, and nothing here reads a value), and THE
// STUBS ARE WRITTEN TO A PRIVATE COPY OF EVERY TRACKED FILE, the stubs laid over
// them: an env_file, a label_file, an extends or an include the specs name is
// resolved against that copy, and which of them the parse will touch is
// compose's to say, not this atom's to guess (the copy was once only the YAML
// and the env files it could see named, and a target outside both was a parse
// the chain would have made and the binary could not). The compose client is the
// pinned v2 binary; `config` is a client-side parse and needs no daemon.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	files, stop := atomlib.ComposeSurface(row, in)
	if stop != nil {
		return *stop
	}
	t := in.Tree()
	yaml := checks.ComposeYAMLFiles(files)
	bodies := make(map[string]string, len(yaml))
	for _, f := range yaml {
		body, err := t.Read(f)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the env_file scan failed (could not read %s: %v). Refusing to report 'nothing to stub' from a scan that did not run, and then to parse a tree missing every file it was supposed to create.", id, f, err))
		}
		bodies[f] = body
	}
	// A target the repository actually tracks is left alone and copied; one it
	// does not is stubbed.
	tracking := map[string]bool{}
	for _, f := range files {
		tracking[strings.TrimPrefix(f, "./")] = true
	}
	stubs := []string{}
	for _, p := range checks.EnvFileRefs(bodies) {
		if !tracking[p] {
			stubs = append(stubs, p)
		}
	}
	if out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "docker-compose", Args: []string{"version"}}); code != 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the pinned docker/compose client (v%s) did not run. Refusing to report a parsed tree that was never parsed.\n%s", id, checks.ComposeVersion, out))
	}
	dir := atoms.TempPath("copy", "")
	defer func() { _ = os.RemoveAll(dir) }()
	if err := atomlib.PrivateCopy(dir, in.Root, slices.Compact(slices.Sorted(slices.Values(files))), stubs); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the private copy the parse runs in could not be made (%v).", id, err))
	}

	lines := []string{fmt.Sprintf("%d env_file reference(s) stubbed", len(stubs))}
	fail := false
	for _, spec := range checks.ComposeSpecs(files) {
		out, code := in.Run(ctx, atoms.Cmd{Dir: dir, Name: "docker-compose", Args: []string{"-f", spec, "config", "--no-interpolate", "--quiet"}})
		if code < 0 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the parse of %s never ran: %s", id, spec, out))
		}
		if code == 0 {
			lines = append(lines, fmt.Sprintf("%-48sOK", spec))
			continue
		}
		fail = true
		lines = append(lines, fmt.Sprintf("%-48sFAIL", spec))
		lines = append(lines, checks.Indent(out, "      ")...)
	}
	table := strings.Join(lines, "\n")
	if fail {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - a tracked compose spec does not parse:\n"+table)
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": every tracked compose spec parses\n"+table)
}
