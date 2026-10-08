package atoms

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// privateCopy lays the named tracked files of a tree, and an empty file for each
// stub, into a directory of its own and answers the directory. It is how an atom
// that has to WRITE beside what it reads (compose:config stubs the env_file
// targets that are correctly not in the repository) leaves the shared tree
// alone: in the chain the stubs went into the engine's copy of the tree, and
// the binary runs on the tree itself, where a stub would be a file the
// developer's next `git status` shows.
//
// A STUB'S PATH IS CLEANED AS IF ROOTED, so `../x.env` lands inside the copy as
// `x.env` and never beside it. The caller removes the directory.
func privateCopy(root string, tracked, stubs []string) (string, error) {
	dir := tempPath("copy", "")
	for _, f := range tracked {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return dir, err
		}
		if err := place(filepath.Join(dir, f), body); err != nil {
			return dir, err
		}
	}
	for _, s := range stubs {
		if err := place(filepath.Join(dir, strings.TrimPrefix(path.Clean("/"+s), "/")), nil); err != nil {
			return dir, err
		}
	}
	return dir, nil
}

// composeConfig: every tracked compose spec parses and its schema validates.
//
// --no-interpolate IS LOAD-BEARING: the specs use ${VAR:?message} to make a
// missing variable a DEPLOY-TIME error, which is fatal anywhere no variable is
// set. THE env_file TARGETS ARE STUBBED FIRST (compose hard-errors on a missing
// one before it reaches the schema, and nothing here reads a value), and THE
// STUBS ARE WRITTEN TO A PRIVATE COPY of only what the parse touches: the
// tracked YAML (the specs and whatever they extend or include) and the env
// files the repository does track. The compose client is the pinned v2 binary;
// `config` is a client-side parse and needs no daemon.
func composeConfig(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	files, stop := composeSurface(a, in)
	if stop != nil {
		return *stop
	}
	t := in.tree()
	yaml := checks.ComposeYAMLFiles(files)
	bodies := make(map[string]string, len(yaml))
	for _, f := range yaml {
		body, err := t.read(f)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the env_file scan failed (could not read %s: %v). Refusing to report 'nothing to stub' from a scan that did not run, and then to parse a tree missing every file it was supposed to create.", a.ID, f, err))
		}
		bodies[f] = body
	}
	// A target the repository actually tracks is left alone and copied; one it
	// does not is stubbed.
	tracking := map[string]bool{}
	for _, f := range files {
		tracking[strings.TrimPrefix(f, "./")] = true
	}
	stubs, copied := []string{}, slices.Clone(yaml)
	for _, p := range checks.EnvFileRefs(bodies) {
		if tracking[p] {
			copied = append(copied, p)
			continue
		}
		stubs = append(stubs, p)
	}
	if out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "docker-compose", Args: []string{"version"}}); code != 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the pinned docker/compose client (v%s) did not run. Refusing to report a parsed tree that was never parsed.\n%s", a.ID, checks.ComposeVersion, out))
	}
	dir, err := privateCopy(in.Root, slices.Compact(slices.Sorted(slices.Values(copied))), stubs)
	defer func() { _ = os.RemoveAll(dir) }()
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the private copy the parse runs in could not be made (%v).", a.ID, err))
	}

	lines := []string{fmt.Sprintf("%d env_file reference(s) stubbed", len(stubs))}
	fail := false
	for _, spec := range checks.ComposeSpecs(files) {
		out, code := in.run(ctx, Cmd{Dir: dir, Name: "docker-compose", Args: []string{"-f", spec, "config", "--no-interpolate", "--quiet"}})
		if code < 0 {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the parse of %s never ran: %s", a.ID, spec, out))
		}
		if code == 0 {
			lines = append(lines, fmt.Sprintf("%-48sOK", spec))
			continue
		}
		fail = true
		lines = append(lines, fmt.Sprintf("%-48sFAIL", spec))
		lines = append(lines, indent(out, "      ")...)
	}
	table := strings.Join(lines, "\n")
	if fail {
		return checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - a tracked compose spec does not parse:\n"+table)
	}
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": every tracked compose spec parses\n"+table)
}

// witValidate: the repository's own `just validate` passes.
//
// ABSENT UNLESS THE TREE TRACKS WIT AND A VALIDATE RECIPE, decided before
// wasm-tools or just is touched. wit/*.wit says there is an interface to
// resolve; a root justfile defining `validate` says the repository has its own
// answer for what resolving means. The recipe's own exit is the verdict: 0
// pass, 1 findings (just passes the failing command's code through), anything
// else could-not-run. The recipe runs in the tree it is handed; in the tools
// container that is the engine's copy of it, as the chain's was.
func witValidate(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	if !checks.HasWitFiles(in.Files) {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this repository tracks no wit/*.wit, so it declares no interface for wasm-tools to resolve")
	}
	if !slices.Contains(in.Files, "justfile") {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - the repository tracks WIT but no root justfile, so it declares no validate recipe to run")
	}
	text, err := in.tree().read("justfile")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the justfile would not read: %v", a.ID, err))
	}
	if !checks.HasJustValidate(text) {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - the justfile defines no validate recipe, so the repository declares no check of its WIT for the gate to run")
	}
	for _, tool := range []string{"wasm-tools", "just"} {
		if out, code := in.run(ctx, Cmd{Dir: in.Root, Name: tool, Args: []string{"--version"}}); code != 0 {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s --version exited %d: %s. WIT that was never resolved is not WIT that passed.", a.ID, tool, code, out))
		}
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "just", Args: []string{"validate"}, Both: true})
	if code < 0 {
		return neverRan(a, out)
	}
	return checks.VerdictOf(a, code, out)
}
