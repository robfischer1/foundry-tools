// Package publishpack is the publish lane's builds, run by the executor: the
// wheels and sdists (python:artifacts) and the npm tarballs (ts:pack) the publish
// capability uploads. The tree's code (build backends, bun scripts) runs HERE,
// in the toolchain containers, and never in the container that holds the
// registry credentials (DEF-7-006); the capability only uploads what these
// stage under the release directory (releasekit.OutDir): dist/ and npm/.
//
// It is the Dagger publish lane's build half (publish.go, publish_npm.go) as
// os/exec argvs, with the lane's sentences and classification copied (R20).
package publishpack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/publishlane"
	"dagger/foundry-tools/internal/releasekit"
)

// Python is python:artifacts: `uv build --out-dir <release>/dist`, the Python half of
// the publish lane. A tree decides which half runs, as the Dagger lane has it: a
// pyproject.toml or setup.py at the root is Python.
//
// uv resolves the build backend from its default index (pypi.org), which is
// what the Dagger lane's --index-url is set to in the fleet's recipe
// (flux ca-recipe-env UV_INDEX_URL), so the executor passes none.
//
// `--python python3` is the toolchain image's own interpreter: every atom's
// environment carries UV_PYTHON_DOWNLOADS=never, so a .python-version the image
// cannot satisfy (svid-auth pins 3.15 on a 3.14 image) would otherwise fail the
// build. A wheel's tags are the project's (py3-none-any), not the build
// interpreter's.
func Python(ctx context.Context, in atoms.Input, a checks.AtomDef) checks.Verdict {
	entries, err := atoms.RootEntries(in.Root)
	if err != nil {
		return cannot(a, fmt.Sprintf("could not run: the tree's root could not be listed: %v", err))
	}
	if !pythonRoot(entries) {
		return absent(a, "the tree has neither a pyproject.toml nor a setup.py, so it publishes no Python package")
	}
	dist := filepath.Join(releasekit.OutDir(), "dist")
	// what an earlier build left there is not this build's
	_ = os.RemoveAll(dist)
	if bad := mkdir(a, dist); bad != nil {
		return *bad
	}
	out, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "uv", Args: []string{"build", "--python", "python3", "--out-dir", dist}, Both: true})
	if _, cls, why := ran("uv build", "uv build", out, code); cls != buildlane.Clean {
		return checks.VerdictOf(a, cls, why)
	}
	files, err := os.ReadDir(dist)
	if err != nil {
		return cannot(a, fmt.Sprintf("could not run: what uv build wrote could not be read: %v", err))
	}
	var names, artifacts []string
	for _, f := range files {
		names = append(names, f.Name())
		if _, ok := publishlane.ArtifactOf(f.Name()); ok {
			artifacts = append(artifacts, f.Name())
		}
	}
	if len(artifacts) == 0 {
		return checks.VerdictOf(a, buildlane.Findings, fmt.Sprintf("findings: uv build exited clean and wrote no wheel or sdist to %s (it wrote: %s)", dist, list(names)))
	}
	return checks.VerdictOf(a, 0, fmt.Sprintf("publish build: uv build wrote %s to %s", list(artifacts), dist))
}
