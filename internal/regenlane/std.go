package regenlane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/checks"
)

// layStd installs the wasm32 standard library into the toolchain of the rustup
// home `to`, as `rustup target add` would have: the pinned dist tarball's lib/
// over the toolchain's, its manifest beside the others, and the component named
// in the two lists rustup reads to know what is installed. Without the lists
// the star's own add would not see it and would download it.
func (p *provision) layStd(ctx context.Context, exec execFunc, to string) error {
	tc, err := toolchainOf(to)
	if err != nil {
		return err
	}
	// THE POD'S HOME MAY ALREADY CARRY IT: serve composes the rust containers'
	// rustup home per run with the pinned std laid and registered (daedalus
	// serve/rustup.go), and laying it twice would list the component twice.
	if _, err := os.Stat(filepath.Join(tc, "lib", "rustlib", "manifest-"+checks.RustStdWasmComponent)); err == nil {
		return nil
	}
	dir := filepath.Join(p.work, checks.RustStdWasmComponent)
	if err := p.fetch(ctx, checks.RustStdWasmURL, dir+".tar.gz"); err != nil {
		return fmt.Errorf("rust-std %s: %v", checks.RustWasmComponentsTag, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, argv := range [][]string{
		checks.RegenTar(dir+".tar.gz", dir, true, 2, checks.RustStdWasmMember),
		checks.RegenCopyTree(filepath.Join(dir, "lib"), filepath.Join(tc, "lib")),
	} {
		if err := step(exec, nil, argv...); err != nil {
			return fmt.Errorf("rust-std %s: %v", checks.RustWasmComponentsTag, err)
		}
	}
	return register(dir, filepath.Join(tc, "lib", "rustlib"))
}

// register records the component as installed under rustlib: its file manifest,
// its line in `components`, and its entry in rustup's multirust-config.toml.
func register(dir, rustlib string) error {
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.in"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rustlib, "manifest-"+checks.RustStdWasmComponent), manifest, 0o644); err != nil {
		return err
	}
	if err := appendTo(filepath.Join(rustlib, "components"), checks.RustStdWasmComponent+"\n"); err != nil {
		return err
	}
	return appendTo(filepath.Join(rustlib, "multirust-config.toml"),
		"\n[[components]]\npkg = \"rust-std\"\ntarget = \"wasm32-unknown-unknown\"\nis_extension = false\n")
}

// appendTo adds text to a file the copy of the image's home already holds,
// whatever mode the image gave it.
func appendTo(path, text string) error {
	if err := os.Chmod(path, 0o644); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// toolchainOf is the one toolchain a rustup home holds.
func toolchainOf(home string) (string, error) {
	ents, err := os.ReadDir(filepath.Join(home, "toolchains"))
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("%d toolchains in %s, want the one the image installed", len(dirs), home)
	}
	return filepath.Join(home, "toolchains", dirs[0]), nil
}
