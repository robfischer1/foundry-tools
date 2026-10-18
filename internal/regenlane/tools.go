package regenlane

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenkit"
)

// fetchPinned is the checksummed download; a test replaces it.
var fetchPinned regenkit.FetchFunc = checks.FetchPinned

// execFunc is the world's exec: what a program said and its exit code.
type execFunc = regenkit.ExecFunc

// provision is what the worlds share and must not build twice: the rustup home
// they may write to, the node, and a wasm-tools per recorded version. A pod's
// root is read-only and its user is not root, so each of these is placed under
// the atom's own work directory.
type provision struct {
	work   string
	rustup once
	node   once
	mu     sync.Mutex
	wasm   map[string]*once
}

func newProvision(atomkit.In) *provision {
	return &provision{work: filepath.Join(atoms.WorkDir(), "ts-regen-tools"), wasm: map[string]*once{}}
}

// once computes a directory once and keeps its error.
type once struct {
	o   sync.Once
	dir string
	err error
}

func (m *once) do(f func() (string, error)) (string, error) {
	m.o.Do(func() { m.dir, m.err = f() })
	return m.dir, m.err
}

// step runs one provisioning program; a failure names it and what it said.
func step(exec execFunc, env []string, argv ...string) error {
	return regenkit.Step(exec, env, argv...)
}

// rustupHome is a copy of the toolchain image's rustup home that this run may
// write to: `rustup target add wasm32-unknown-unknown`, the first line of every
// star's build script, writes into the toolchain's own directory, which the
// image's is not writable to a pod's user. The target is laid in it already
// (layStd): the add would otherwise download it.
func (p *provision) rustupHome(ctx context.Context, exec execFunc) (string, error) {
	return p.rustup.do(func() (string, error) {
		from := os.Getenv("RUSTUP_HOME")
		if from == "" {
			from = "/usr/local/rustup"
		}
		to := filepath.Join(p.work, "rustup")
		if err := os.MkdirAll(to, 0o755); err != nil {
			return "", err
		}
		if err := step(exec, nil, checks.RegenCopyTree(from, to)...); err != nil {
			return "", fmt.Errorf("rustup home: %v", err)
		}
		// THE TARGET IS LAID HERE, ONCE. The star's script adds it too, and
		// worlds that build side by side would add it to one home at once,
		// racing on its installed files; with it registered the script's own
		// add finds nothing to do, and reaches for no network.
		if err := p.layStd(ctx, exec, to); err != nil {
			return "", fmt.Errorf("rustup home: %v", err)
		}
		return to, nil
	})
}

// nodeDir is the pinned node, unpacked whole (npm and its modules ride with it).
func (p *provision) nodeDir(ctx context.Context, exec execFunc) (string, error) {
	return p.node.do(func() (string, error) {
		dir, tgz := filepath.Join(p.work, "node"), filepath.Join(p.work, "node.tar.gz")
		if err := p.fetch(ctx, checks.NodeRegenURL, tgz); err != nil {
			return "", fmt.Errorf("node %s: %v", checks.Node24Version, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		for _, argv := range [][]string{checks.RegenTar(tgz, dir, true, 1, ""), {filepath.Join(dir, "bin", "node"), "--version"}} {
			if err := step(exec, nil, argv...); err != nil {
				return "", fmt.Errorf("node %s: %v", checks.Node24Version, err)
			}
		}
		return dir, nil
	})
}

// wasmTools is the directory holding the recorded version's release binary, or
// "" when none is pinned or it would not arrive: the star's script then compiles
// it, as the lane always could. Slower, never different.
func (p *provision) wasmTools(ctx context.Context, exec execFunc, version string) string {
	p.mu.Lock()
	m, ok := p.wasm[version]
	if !ok {
		m = &once{}
		p.wasm[version] = m
	}
	p.mu.Unlock()
	dir, _ := m.do(func() (string, error) {
		url, member, ok := checks.WasmToolsRelease(version)
		if !ok {
			return "", nil
		}
		dir, tgz := filepath.Join(p.work, "wasm-tools-"+version), filepath.Join(p.work, "wasm-tools-"+version+".tar.gz")
		if err := p.fetch(ctx, url, tgz); err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		for _, argv := range [][]string{checks.RegenTar(tgz, dir, true, strings.Count(member, "/"), member), {filepath.Join(dir, path.Base(member)), "--version"}} {
			if err := step(exec, nil, argv...); err != nil {
				return "", err
			}
		}
		return dir, nil
	})
	return dir
}

// fetch downloads a pinned asset: the mirror first, upstream second, both held
// to the pinned sum (checks.ToolSources).
func (p *provision) fetch(ctx context.Context, url, dest string) error {
	return regenkit.Fetch(ctx, fetchPinned, url, dest)
}
