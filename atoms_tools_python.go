package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE PYTHON LAYERS OF THE TOOLS CONTAINER. The atoms that run a repository's
// own checkers (tools/check_*.py, infra's tools/dup-check) and the two that
// exec a Python program (ansible, copier) used to provision all of it in the
// chain, per atom, per gate: `uv run --with jsonschema`, `uvx --from copier`,
// `ansible-galaxy collection install` with a retry for galaxy's cold-miss 404s.
// Here it is built ONCE, in the Dagger pipeline, and nothing resolves a package
// when an atom runs:
//
//	uv            the pinned image's binary (checks.ImageUV)
//	python        the pinned python-build-standalone release (checks.PythonStandaloneURL),
//	              fetched by the ENGINE and verified against checks.ToolSHA256
//	packages      pytools/requirements.txt, a lock with hashes, installed with
//	              --require-hashes and --no-build into one venv
//	collections   pytools/ansible-collections.yml, exact versions, into
//	              atoms.AnsibleCollectionsDir
//
// Each stage is built in a container of its own and Synced, so a stage that
// fails is LEFT OUT (with its error on stderr) and the atoms that need it settle
// 2 on their own probe, instead of one registry's bad hour failing the whole
// container and every atom with it. The stages are separate execs, so a lock
// edit rebuilds the packages and the collections and not the interpreter.

// pytoolsDir holds the python layers' inputs in this module.
const pytoolsDir = "pytools"

// pythonLayers are the outputs of the python stages; nil is a stage that did
// not build.
type pythonLayers struct {
	uv                             *dagger.File
	interpreter, venv, collections *dagger.Directory
}

// pythonInstallDir is where the interpreter is unpacked, in the stage and in the
// container alike: a venv is not relocatable (its scripts and its interpreter
// link name absolute paths), so every output sits where it was built.
//
// THE INTERPRETER IS NOT INSTALLED BY uv. `uv python install` ran inside the
// container, which reached github.com through whatever the container could
// reach, and it failed in the cluster's engine on every lane (2026-10-08,
// "exit code: 2" with the cause cut from the log). The engine's own fetch
// (dag.HTTP, as every tool above is) reaches it, so the release tarball comes
// that way, checksummed, and uv is told never to download one.
const pythonInstallDir = "/opt/python"

// toolsLog is where the build says what it left out. A variable so a test reads
// it; stderr in the module, where the shadow's own report goes. The tools are
// fetched at once, so the lines are written under a lock: a line is whole.
var (
	toolsLog   io.Writer = os.Stderr
	toolsLogMu sync.Mutex
)

// logDropped says, on one stderr line, why a tool or a layer is not in the
// container: the fetch or verify error, so "executable file not found" in an
// atom's verdict has a cause a reader can find.
func logDropped(what string, err error) {
	toolsLogMu.Lock()
	defer toolsLogMu.Unlock()
	fmt.Fprintf(toolsLog, "atoms tools: %s left out of the container: %v\n", what, err)
}

// buildPython builds the python stages on the OS layer. It never fails: a stage
// that does not build is logged and nil, and so is every stage above it (the
// named result is what the stages below it built, and each early return hands it
// back as it stands).
func buildPython(ctx context.Context, base *dagger.Container) (out pythonLayers) {
	src := dag.CurrentModule().Source()
	venv := atoms.PythonVenvDir
	uv := dag.Container().From(checks.ImageUV).File("/uv")

	interp, err := pythonInterpreter(ctx, base)
	if err != nil {
		logDropped("the python interpreter", err)
		return
	}
	interp = interp.
		// uv verifies against the platform store the engine's CA is in (laneBase
		// says why this is not its default). It never fetches an interpreter: the
		// one it is handed below is the pinned one.
		WithEnvVariable("UV_NATIVE_TLS", "1").
		WithEnvVariable("UV_PYTHON_DOWNLOADS", "never").
		WithFile(toolsBinDir+"uv", uv, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithExec([]string{"uv", "--version"}).
		WithExec([]string{"uv", "venv", venv, "--python", pythonInstallDir + "/bin/python3", "--verbose"}).
		WithExec([]string{venv + "/bin/python", "--version"})
	if _, err := interp.Sync(ctx); err != nil {
		logDropped("the python interpreter", withExecEvidence(err))
		return
	}
	out.uv = uv
	out.interpreter = interp.Directory(pythonInstallDir)

	pkgs := interp.
		WithFile("/tmp/"+pytoolsDir+"/requirements.txt", src.File(pytoolsDir+"/requirements.txt")).
		WithExec([]string{
			"uv", "pip", "install", "--python", venv + "/bin/python",
			"--require-hashes", "--no-build", "--compile-bytecode",
			"-r", "/tmp/" + pytoolsDir + "/requirements.txt", "--verbose",
		}).
		// Every module an atom's script imports, and the two entry points, by
		// name: a lock that installs and does not import is a build that fails
		// here and not an atom that finds a traceback.
		WithExec([]string{venv + "/bin/python", "-c", "import yaml, jsonschema, tomllib, tomli, ansible, ansiblelint, copier"}).
		WithExec([]string{venv + "/bin/ansible-playbook", "--version"}).
		WithExec([]string{venv + "/bin/copier", "--version"})
	if _, err := pkgs.Sync(ctx); err != nil {
		logDropped("the python packages", withExecEvidence(err))
		return
	}
	out.venv = pkgs.Directory(venv)

	collections, err := ansibleCollections(ctx, pkgs, src)
	if err != nil {
		logDropped("the ansible collections", withExecEvidence(err))
		return
	}
	out.collections = collections
	return
}

// pythonInterpreter unpacks the pinned python-build-standalone release into
// pythonInstallDir on the OS layer and proves it runs. The tarball is fetched by
// the engine and checked against checks.ToolSHA256 in a container of its own
// (toolFile's reason: a download that fails its checksum leaves the interpreter
// out and not the whole container); nothing is fetched when the stage runs.
func pythonInterpreter(ctx context.Context, base *dagger.Container) (*dagger.Container, error) {
	url := checks.PythonStandaloneURL
	sum, ok := checks.ToolSHA256[url]
	if !ok {
		return nil, fmt.Errorf("%s has no checksum in checks.ToolSHA256", url)
	}
	f, err := fetchTool(ctx, url)
	if err != nil {
		return nil, err
	}
	// --strip-components=1: the tarball's one top directory is python/.
	ctr := base.
		WithFile("/tmp/python.tar.gz", f).
		WithNewFile("/tmp/python.sha256", sum+"  /tmp/python.tar.gz\n").
		WithExec([]string{"sha256sum", "-c", "/tmp/python.sha256"}).
		WithExec([]string{"mkdir", "-p", pythonInstallDir}).
		WithExec([]string{"tar", "xzf", "/tmp/python.tar.gz", "-C", pythonInstallDir, "--strip-components=1"}).
		WithExec([]string{pythonInstallDir + "/bin/python3", "--version"})
	if _, err := ctr.Sync(ctx); err != nil {
		return nil, fmt.Errorf("%s did not verify and unpack: %w", url, withExecEvidence(err))
	}
	return ctr, nil
}

// ansibleCollections installs the declared collections into the stage, and
// answers the directory they landed in.
//
// A FAILED INSTALL IS NEVER CACHED, and is retried once with
// --clear-response-cache, the flag galaxy's own error names for a bad cached
// answer: MEASURED over every gate receipt 2026-09-09 to 17, ops:ansible could
// not run on 32 trees (31 on 2026-09-14) because the galaxy proxy answered 404
// on a cold miss, each time on a run that changed nothing about the
// collections. Here the retry is paid once per pin, at build, and a run that
// follows reads a directory.
func ansibleCollections(ctx context.Context, stage *dagger.Container, src *dagger.Directory) (*dagger.Directory, error) {
	declared := stage.WithFile("/tmp/"+pytoolsDir+"/ansible-collections.yml", src.File(pytoolsDir+"/ansible-collections.yml"))
	var last error
	for _, flags := range [][]string{nil, {"--clear-response-cache"}} {
		args := append([]string{atoms.PythonVenvDir + "/bin/ansible-galaxy", "collection", "install"}, flags...)
		args = append(args, "-r", "/tmp/"+pytoolsDir+"/ansible-collections.yml", "-p", atoms.AnsibleCollectionsDir)
		installed := declared.WithExec(args)
		if _, last = installed.Sync(ctx); last == nil {
			return installed.Directory(atoms.AnsibleCollectionsDir), nil
		}
	}
	return nil, last
}

// apply lays one python layer on the container, by the plan's name; a stage
// that did not build leaves the container as it was.
func (p pythonLayers) apply(ctr *dagger.Container, name string) *dagger.Container {
	switch name {
	case "uv":
		if p.uv != nil {
			return ctr.WithFile(toolsBinDir+"uv", p.uv, dagger.ContainerWithFileOpts{Permissions: 0o755})
		}
	case "python":
		if p.interpreter != nil {
			return ctr.WithDirectory(pythonInstallDir, p.interpreter)
		}
	case "python-packages":
		if p.venv != nil {
			// The venv's bin first on PATH, so `python3` and the entry points are
			// the venv's; the flags expand the image's own PATH after it.
			return ctr.WithDirectory(atoms.PythonVenvDir, p.venv).
				WithEnvVariable("PATH", atoms.PythonBinDir+":${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true})
		}
	case "ansible-collections":
		if p.collections != nil {
			return ctr.WithDirectory(atoms.AnsibleCollectionsDir, p.collections)
		}
	}
	return ctr
}
