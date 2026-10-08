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
// stub, into dir, which the caller made the name of and removes. It is how an
// atom that has to WRITE beside what it reads (compose:config stubs the env_file
// targets that are correctly not in the repository) leaves the shared tree
// alone: in the chain the stubs went into the engine's copy of the tree, and
// the binary runs on the tree itself, where a stub would be a file the
// developer's next `git status` shows.
//
// THE STUBS GO IN AFTER THE FILES, so a stub is written over whatever the copy
// already holds at its path. A symlink is copied as a symlink (the chain's copy
// of the tree kept them, and a link that points nowhere is not an error in a
// tree that never follows it), and a file keeps its permission bits.
//
// A STUB'S PATH IS CLEANED AS IF ROOTED, so `../x.env` lands inside the copy as
// `x.env` and never beside it.
func privateCopy(dir, root string, tracked, stubs []string) error {
	for _, f := range tracked {
		if err := copyTracked(filepath.Join(root, f), filepath.Join(dir, f)); err != nil {
			return err
		}
	}
	for _, s := range stubs {
		if err := place(filepath.Join(dir, strings.TrimPrefix(path.Clean("/"+s), "/")), nil); err != nil {
			return err
		}
	}
	return nil
}

// copyTracked copies one path of the tree to dst: a link as a link, a file with
// its bytes and its permission bits.
func copyTracked(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	// The directory is made first and its error is not branched on: it fails the
	// write below with the same cause, and one test reaches that.
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	if fi.Mode()&os.ModeSymlink != 0 {
		// A link that vanished since the Lstat reads as an empty target, which
		// os.Symlink refuses with the same error a failed read would have been.
		target, _ := os.Readlink(src)
		return os.Symlink(target, dst)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, fi.Mode().Perm())
}

// privateTree lays every file the repository would commit into a directory of
// this run's own and makes it a repository (init, then add everything),
// answering the directory and the call that removes it. It is the chain's tree
// for an atom that runs a recipe or a tool which may WRITE (a cache, a lock, a
// rendered file) or which reads the index (`git ls-files`): the chain ran those
// in the engine's copy of the tree, and the binary must not run them in the tree
// its other atoms are reading at the same moment. Git ignores the developer's own
// configuration, so a global ignore file cannot change what the index holds.
func privateTree(ctx context.Context, in Input, kind string) (string, func(), error) {
	dir := tempPath(kind, "")
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := privateCopy(dir, in.Root, slices.Compact(slices.Sorted(slices.Values(in.Committable))), nil); err != nil {
		cleanup()
		return "", nil, err
	}
	env := []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	for _, args := range [][]string{{"init", "-q", "."}, {"add", "-A"}} {
		if out, code := in.run(ctx, Cmd{Dir: dir, Name: "git", Args: args, Env: env, Both: true}); code != 0 {
			cleanup()
			return "", nil, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(out))
		}
	}
	return dir, cleanup, nil
}

// composeConfig: every tracked compose spec parses and its schema validates.
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
	stubs := []string{}
	for _, p := range checks.EnvFileRefs(bodies) {
		if !tracking[p] {
			stubs = append(stubs, p)
		}
	}
	if out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "docker-compose", Args: []string{"version"}}); code != 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the pinned docker/compose client (v%s) did not run. Refusing to report a parsed tree that was never parsed.\n%s", a.ID, checks.ComposeVersion, out))
	}
	dir := tempPath("copy", "")
	defer func() { _ = os.RemoveAll(dir) }()
	if err := privateCopy(dir, in.Root, slices.Compact(slices.Sorted(slices.Values(files))), stubs); err != nil {
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
// else could-not-run.
//
// THE RECIPE RUNS IN A PRIVATE TREE, not the shared one. It is the repository's
// own program, and a recipe that writes (a generated file, a lock, a cache) is
// allowed to: in the chain that was the engine's copy of the tree, and the binary
// runs its atoms at once over one tree, so a write there is a file the atoms
// reading beside it may or may not see.
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
	dir, cleanup, err := privateTree(ctx, in, "wit")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the private tree the recipe runs in could not be made (%v).", a.ID, err))
	}
	defer cleanup()
	out, code := in.run(ctx, Cmd{Dir: dir, Name: "just", Args: []string{"validate"}, Both: true})
	if code < 0 {
		return neverRan(a, out)
	}
	return checks.VerdictOf(a, code, out)
}
