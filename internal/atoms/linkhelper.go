package atoms

import (
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/execmem"
	"dagger/foundry-tools/internal/gitextra"
	"dagger/foundry-tools/internal/psshim"
)

// THE HELPERS AN ATOM CARRIES IN ITS OWN BINARY. The mutation atoms run two
// small programs beside the tool they drive: `ps` for Stryker (psshim),
// `execmem` for cargo-mutants, and `git` for gomutants' nightly scope (gitextra). They were built into the lane's container as
// files of their own. An atom is one file now, and the executor's toolchain
// image is upstream's, with nothing of ours in it, so the atom's binary is the
// helper: it makes a link to itself under the helper's name, puts the link's
// directory where the tool will look, and answers as the helper when it is run
// through the link (atomkit.Main asks RunHelper first). One program, no second
// artifact to pin, build or ship.

// helpers are the programs a link to an atom's binary can be, by the name the
// link carries.
var helpers = map[string]func(args []string) int{
	"ps":      psshim.Run,
	"execmem": func(args []string) int { return execmem.Main(args, os.Stderr) },
	"git":     gitextra.Run,
}

// RunHelper runs the helper argv[0] names and answers its exit code, or false
// when the process was started as an atom.
func RunHelper(argv []string) (int, bool) {
	if len(argv) == 0 {
		return 0, false
	}
	h, ok := helpers[filepath.Base(argv[0])]
	if !ok {
		return 0, false
	}
	return h(argv[1:]), true
}

// executable is this process's own file, a variable so a test can say which.
var executable = os.Executable

// LinkHelper makes dir/name a link to this atom's binary, and answers the path
// it made. name must be one RunHelper knows. A link that is already there is
// replaced: two asks of one atom in a pod leave the same link.
func LinkHelper(dir, name string) (string, error) {
	self, err := executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	link := filepath.Join(dir, name)
	_ = os.Remove(link) // absent is the usual case, and Symlink reports a link that cannot be made
	return link, os.Symlink(self, link)
}
