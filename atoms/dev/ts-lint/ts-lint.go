// Package devtslint is the atom dev:ts-lint: the repo's lint script is clean
// under the fleet's eslint config, run by hand from the dev lane.
package devtslint

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

// Def is the atom. THE GATE TWIN IS ts:bun-gate, which runs the repo's `gate`
// script; this runs the repo's `lint` script, one of the scripts the gate chains.
// Same toolchain image (the pinned bun), same tool:node first on PATH, the gate's
// frozen install, and THE FLEET'S ESLINT CONFIG laid at the root before the
// script runs, so the dev answer is the gate's (it read the repo's own config
// while it was a command manifest, which has no step that lays a file). The Def's
// fields are literals: daedalus reads them statically.
//
// dev:ts-fmt has no config to share: the twin's `format:check` (prettier) reads
// the repo's own, and the fleet embeds no prettier ruleset
// (internal/checks/rulesets holds eslint, ruff, mypy and the sast rules).
//
// The install writes node_modules, so the verb runs on the atom's own copy of
// the tree, and WritesTree is set; the lint script may fix in place too.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LaneTS,
	Group:      checks.GroupLanguage,
	Desc:       "the repo's lint script is clean under the fleet's eslint config, run by hand from the dev lane.",
	Surface:    "root:package.json",
	Toolchain:  "bun",
	Needs:      []string{"tool:node", "tool:curl"},
	Timeout:    30 * time.Minute,
	WritesTree: true,
	Check:      check,
}

const id = "dev:ts-lint"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LaneTS}

// eslintConfig is the config file the fleet lays at the root (ts:bun-gate's).
const eslintConfig = "eslint.config.mjs"

// check lays the fleet's eslint config, installs frozen, and runs `bun run lint`
// with the caller's arguments. A bare workspace root has no lint script of its
// own: each member's runs, in its directory, and the first that fails is the
// verdict (checks.TSBareWorkspaceMembers, as ts:bun-gate).
//
// THE CONFIG IS LAID FOR THE RUN AND TAKEN BACK. This verb's product is the
// answer, and the executor delivers the diff of the atom's whole tree copy: the
// fleet's file left over the repo's would be delivered as an edit nobody asked
// for. The repo's own file (or its absence) is restored on every exit.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	dirs, stop := lintDirs(in)
	if stop != nil {
		return *stop
	}
	restore, err := layConfig(filepath.Join(in.Root, eslintConfig))
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the fleet's eslint config could not be laid: "+err.Error())
	}
	defer restore()

	for _, dir := range dirs {
		out, code, bad := atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: dir, Name: "bun", Args: []string{"install", "--frozen-lockfile"}})
		if bad != nil {
			return *bad
		}
		if code != 0 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - preflight `bun install --frozen-lockfile` exited "+strconv.Itoa(code)+": "+atoms.LastLine(out))
		}
		out, code, bad = atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: dir, Name: "bun", Args: append([]string{"run", "lint"}, in.Args...)})
		if bad != nil {
			return *bad
		}
		if code != 0 {
			v := checks.VerdictOf(row, code, out)
			if len(dirs) > 1 {
				v.Reason = strings.Replace(v.Reason, id, id+" ["+filepath.Base(dir)+"]", 1)
			}
			return v
		}
	}
	return checks.VerdictOf(row, 0, "")
}

// lintDirs are the directories the script runs in: the root, or a bare
// workspace root's members. A bare root whose globs match no member has no lint
// script to run, which is not a pass.
func lintDirs(in atomkit.In) ([]string, *checks.Verdict) {
	manifest, err := os.ReadFile(filepath.Join(in.Root, "package.json"))
	if err != nil {
		return []string{in.Root}, nil
	}
	members, bare := checks.TSBareWorkspaceMembers(string(manifest), in.Files)
	if !bare {
		return []string{in.Root}, nil
	}
	if len(members) == 0 {
		v := checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the root package.json is a bare workspace root and none of its workspaces matches a package.json in the tree, so there is no lint script to run")
		return nil, &v
	}
	dirs := make([]string, len(members))
	for i, m := range members {
		dirs[i] = filepath.Join(in.Root, filepath.FromSlash(m))
	}
	return dirs, nil
}

// layConfig writes the fleet's eslint config at path and answers the function
// that puts the file back as it was: its bytes and mode, or gone.
func layConfig(path string) (restore func(), err error) {
	old, rerr := os.ReadFile(path)
	mode := fs.FileMode(0o644)
	if fi, serr := os.Stat(path); serr == nil {
		mode = fi.Mode().Perm()
	}
	existed := rerr == nil
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return nil, rerr
	}
	if existed {
		// a tracked file may be read-only; this is the atom's own copy
		if err := os.Chmod(path, mode|0o200); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(path, []byte(rulesets.ESLint), 0o644); err != nil {
		return nil, err
	}
	return func() {
		if existed {
			_ = os.WriteFile(path, old, mode|0o200)
			_ = os.Chmod(path, mode)
			return
		}
		_ = os.Remove(path)
	}, nil
}
