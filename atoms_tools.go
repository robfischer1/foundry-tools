package main

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE TOOLS CONTAINER. The atoms binary execs the third-party programs its
// atoms grade with (opengrep, hadolint, opa, kubectl, ...) from PATH, so the
// container it runs in carries them. It is built here, in the Dagger pipeline,
// from a pinned Debian digest and cached layers; nothing is published and the
// fleet owns no base image for it.
//
// THE LAYERS ARE ORDERED BY HOW OFTEN THEY CHANGE, least first, so an edit
// deep in the stack rebuilds only what sits above it, and an edit to an atom
// rebuilds the top layer alone:
//
//	the OS packages (a base digest bump)
//	each pinned third-party tool, the least-moved pin first
//	the sidecar reader and the witness helper, built from source
//	uv, then the python interpreter, the python packages, the ansible collections
//	the atoms binary (every atom edit)
//
// THE TOOLS' ORDER IS A DATED SNAPSHOT of how often each pin moved: the number
// of days on which the tool's pin line changed in internal/checks over the 31
// days with commits between 2026-09-08 and 2026-10-08 (git log -G on the pin's
// line; creation counts as a change). It is not a forecast: a tool with the
// same count keeps the order it was written in, and a count taken at another
// date may order them differently. The counts are in pinnedTools.
//
// THE PYTHON LAYERS HAVE NO PIN HISTORY YET: the lock is new. They sit above the
// tools because a lock that renovate bumps is expected to move more than a
// release asset's pin does; that is an expectation, to be replaced by a count
// once the lock has a month of history. Within them the order is dependency
// order (uv, interpreter, packages, collections), which is also least-moved
// first.

// layerKind says what a step of the container is.
type layerKind int

const (
	layerOS layerKind = iota
	layerTool
	layerPython
	layerBinary
)

// pinnedTool is one program the container carries. Exactly one source is set:
// a release asset (url, with member naming the binary inside a tarball), a file
// in a pinned image, or a build from this module's source.
type pinnedTool struct {
	name string
	// moves is how many days the tool's pin changed on (see above).
	moves int
	// url / member: a release asset whose checksum is in checks.ToolSHA256. An
	// empty member says the download IS the program.
	url, member string
	// image / imagePath: the program is a file of an image pinned by digest.
	image, imagePath string
	// build: the program is built from this module's source with goToolchain().
	build func() *dagger.File
}

// pinnedTools is the container's third-party set, in layer order. The version
// and the URL of each are the module's existing pins (internal/checks); the
// chains fetch the same URLs through fetchTool.
var pinnedTools = []pinnedTool{
	{name: "shellcheck", moves: 0, url: checks.ShellcheckURL, member: checks.ShellcheckMember},
	{name: "just", moves: 1, url: checks.JustURL, member: checks.JustMember},
	{name: "wasm-tools", moves: 1, url: checks.WasmToolsURL, member: checks.WasmToolsMember},
	{name: "hadolint", moves: 1, url: checks.HadolintURL},
	{name: "chezmoi", moves: 2, url: checks.ChezmoiURL},
	{name: "docker-compose", moves: 2, url: checks.ComposeURL},
	{name: "kubectl", moves: 2, url: checks.KubectlURL},
	{name: "opa", moves: 2, url: checks.OpaURL},
	{name: "kubeconform", moves: 3, image: checks.ImageKubeconform, imagePath: "/kubeconform"},
	{name: "opengrep", moves: 3, url: checks.OpengrepURL},
	{name: "orbitparse", moves: 4, build: orbitParse},
	// witnesscall is built from source like orbitparse, so its count is the days
	// with commits under witnesscall/, internal/witnesscall and internal/hadescall
	// in the same window, not a pin line.
	{name: "witnesscall", moves: 4, build: func() *dagger.File { return helperBinary("witnesscall") }},
}

// layer is one step of the container, in the order it is applied.
type layer struct {
	name string
	kind layerKind
	tool *pinnedTool
	// provides are the programs on PATH a python layer is the reason for, when
	// they are not its name (the interpreter, the venv's entry points).
	provides []string
}

// pythonPlan is the python layers, in the order they are applied (see
// pythonLayers.apply). The programs they put on PATH are named in provides, and
// TestToolsContainerCarriesEveryProgram holds them to atoms.Programs.
var pythonPlan = []layer{
	{name: "uv", kind: layerPython},
	{name: "python", kind: layerPython, provides: []string{"python3", "python"}},
	{name: "python-packages", kind: layerPython, provides: []string{"copier", "ansible-playbook", "ansible-lint"}},
	{name: "ansible-collections", kind: layerPython},
}

// toolsPlan is the container's layers, bottom to top. It is data so the order
// is held by a test and followed by the build: the OS first, the tools in
// pinnedTools' order, the python layers, the atoms binary LAST.
func toolsPlan() []layer {
	plan := []layer{{name: "os", kind: layerOS}}
	for i := range pinnedTools {
		plan = append(plan, layer{name: pinnedTools[i].name, kind: layerTool, tool: &pinnedTools[i]})
	}
	plan = append(plan, pythonPlan...)
	return append(plan, layer{name: "atoms", kind: layerBinary})
}

// toolsOS is the base and the packages the binary and its tools need that the
// slim image lacks: git (the change set, the bundle revision), ca-certificates
// (the engine installs its CA into the store that package creates) and xz-utils
// (shellcheck ships as .tar.xz). The lists are removed in the same layer.
func toolsOS() *dagger.Container {
	return dag.Container().From(checks.ImageTools).
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "-y", "--no-install-recommends", "git", "ca-certificates", "xz-utils"}).
		WithExec([]string{"rm", "-rf", "/var/lib/apt/lists"})
}

// toolFile is the tool's program, fetched and VERIFIED, or why it is not
// available. Verification runs in a container of its own on the OS layer
// (sha256sum is coreutils, tar and xz are the base's), not in the chain that
// becomes the image: a download that fails its checksum then leaves this tool
// out, and its atoms settle 2 on "executable file not found", instead of
// failing the whole container and every atom with it.
func toolFile(ctx context.Context, base *dagger.Container, t pinnedTool) (*dagger.File, error) {
	switch {
	case t.build != nil:
		return t.build(), nil
	case t.image != "":
		return dag.Container().From(t.image).File(t.imagePath), nil
	}
	sum, ok := checks.ToolSHA256[t.url]
	if !ok {
		return nil, fmt.Errorf("%s has no checksum in checks.ToolSHA256", t.url)
	}
	f, err := fetchTool(ctx, t.url)
	if err != nil {
		return nil, err
	}
	ctr := base.
		WithFile("/tmp/pkg", f).
		WithNewFile("/tmp/pkg.sha256", sum+"  /tmp/pkg\n").
		WithExec([]string{"sha256sum", "-c", "/tmp/pkg.sha256"})
	out := "/tmp/pkg"
	if t.member != "" {
		// A release tarball: one member is extracted, with its leading
		// directories stripped, so the program lands at /out/<name>.
		args := []string{"tar", "xf", "/tmp/pkg", "-C", "/out"}
		if strip := strings.Count(t.member, "/"); strip > 0 {
			args = append(args, fmt.Sprintf("--strip-components=%d", strip))
		}
		ctr = ctr.WithExec([]string{"mkdir", "-p", "/out"}).WithExec(append(args, t.member))
		out = "/out/" + t.name
	}
	if _, err := ctr.Sync(ctx); err != nil {
		return nil, fmt.Errorf("%s did not verify against its pinned checksum: %w", t.url, err)
	}
	return ctr.File(out), nil
}

// toolFiles fetches and verifies every tool at once, in the plan's order. A
// tool that is not available is nil: one tool that will not fetch must not take
// the whole shadow's answer with it. Its absence is not silent: one stderr line
// per dropped tool carries the fetch or verify error, because the atom that
// needed it only says "executable file not found".
func toolFiles(ctx context.Context, base *dagger.Container) map[string]*dagger.File {
	files := make([]*dagger.File, len(pinnedTools))
	var g errgroup.Group
	for i := range pinnedTools {
		g.Go(func() error {
			// Each tool's failure is that tool's absence, never the group's.
			f, err := toolFile(ctx, base, pinnedTools[i])
			if err != nil {
				logDropped(pinnedTools[i].name, err)
				return nil
			}
			files[i] = f
			return nil
		})
	}
	_ = g.Wait() // every goroutine files its own result; none returns an error
	out := make(map[string]*dagger.File, len(files))
	for i, f := range files {
		if f != nil {
			out[pinnedTools[i].name] = f
		}
	}
	return out
}

// toolsBinDir is where every tool, and the atoms binary, sit on PATH.
const toolsBinDir = "/usr/local/bin/"

// atomsTools is the container the atoms binary runs in: the plan applied in
// order, the binary on top. The tree is not mounted here; see run.onTools.
func atomsTools(ctx context.Context) *dagger.Container {
	ctr := toolsOS()
	// The python stages build beside the tool downloads, not after them.
	var py pythonLayers
	built := make(chan struct{})
	go func() {
		defer close(built)
		py = buildPython(ctx, ctr)
	}()
	files := toolFiles(ctx, ctr)
	<-built
	for _, l := range toolsPlan() {
		switch l.kind {
		case layerTool:
			if f, ok := files[l.name]; ok {
				ctr = ctr.WithFile(toolsBinDir+l.name, f, dagger.ContainerWithFileOpts{Permissions: 0o755})
			}
		case layerPython:
			ctr = py.apply(ctr, l.name)
		case layerBinary:
			ctr = ctr.WithFile(atomsBinPath, atomsBinary(), dagger.ContainerWithFileOpts{Permissions: 0o755})
		}
	}
	return ctr
}

// onTools puts the run's tree and environment on the tools container: the
// same /src, CI and OTEL_SDK_DISABLED a lane has (laneBase says why each), and
// the reask key when the call has one. These sit above every layer the tools
// and the binary are, so the per-call values never rebuild one.
func (r *run) onTools(ctr *dagger.Container) *dagger.Container {
	ctr = ctr.
		WithEnvVariable("CI", "true").
		WithEnvVariable("OTEL_SDK_DISABLED", "true")
	if r.reask != "" {
		ctr = ctr.WithEnvVariable("CA_REASK", r.reask)
	}
	return ctr.WithMountedDirectory("/src", r.src).WithWorkdir("/src")
}
