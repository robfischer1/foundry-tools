package atomkit

import (
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
)

// Default is one default argument group of a command atom that takes caller
// arguments (args = "append"): arguments the manifest supplies unless the
// caller has already decided the matter. A caller who names a package replaces
// the default package; one who passes -p drops --workspace; one who passes
// their own lints after `--` drops the fleet's. With no caller arguments every
// default stands and the argv is argv + defaults, in manifest order.
//
// It is data, not a script: a closed rule over the argument list, so a manifest
// still runs exactly one command (R46) and the same composition runs in the
// Dagger lane and in the pod executor.
type Default struct {
	// Args are the arguments the default supplies.
	Args []string `json:"args" toml:"args"`
	// Unless are the caller arguments that decide the matter, and so drop this
	// default. A token matches an argument that equals it, that starts with it
	// and "=" (--package=foo), or, for a one-letter short flag, that starts
	// with it (-pfoo). The token "@package" matches a Go package pattern.
	Unless []string `json:"unless,omitempty" toml:"unless"`
	// Tail places the default after the caller's arguments; otherwise it sits
	// between the manifest's argv and them.
	Tail bool `json:"tail,omitempty" toml:"tail"`
	// Until is the marker that ends the caller's own arguments (-args for go,
	// -- for cargo): Unless looks only before it, and a Tail default goes in
	// front of it.
	Until string `json:"until,omitempty" toml:"until"`
}

// PackageToken is the Unless token for "the caller named a Go package".
const PackageToken = "@package"

// RunsPreflight says whether the toolchain's preflight runs before this atom:
// yes unless the manifest says preflight = false (dev:rust-lock and
// dev:rust-update exist for the Cargo.lock that `cargo fetch --locked` refuses).
func (m Manifest) RunsPreflight() bool { return m.Preflight == nil || *m.Preflight }

// Compose is the argv to run for the caller's arguments. A manifest that takes
// none (args = "none") runs its argv alone; one that takes them (args =
// "append") runs argv, the head defaults, the caller's arguments and the tail
// defaults. m is not modified.
func (m Manifest) Compose(args []string) []string {
	argv := slices.Clone(m.Argv)
	if m.Args != "append" {
		return argv
	}
	body := slices.Clone(args)
	for _, d := range m.Default {
		own := args
		if d.Until != "" {
			own = ownArgs(args, d.Until)
		}
		if decided(own, d.Unless) {
			continue
		}
		switch {
		case !d.Tail:
			argv = append(argv, d.Args...)
		case d.Until == "":
			body = append(body, d.Args...)
		default:
			// in front of the marker, and after any group already put there.
			at := slices.Index(body, d.Until)
			if at < 0 {
				at = len(body)
			}
			body = slices.Insert(body, at, d.Args...)
		}
	}
	return append(argv, body...)
}

// ownArgs is the caller's arguments up to the first until marker.
func ownArgs(args []string, until string) []string {
	if i := slices.Index(args, until); i >= 0 {
		return args[:i]
	}
	return args
}

// decided says whether any argument matches any of the tokens. @package reads
// the list as a whole (atoms.NamesPackage): the value of a go flag such as
// `-o ./bin/x` is a file, not a package, and keeps the default.
func decided(args, tokens []string) bool {
	for _, tok := range tokens {
		if tok == PackageToken {
			if atoms.NamesPackage(args) {
				return true
			}
			continue
		}
		for _, a := range args {
			if matches(a, tok) {
				return true
			}
		}
	}
	return false
}

// matches is one argument against one Unless token.
func matches(arg, tok string) bool {
	switch {
	case tok == PackageToken:
		return packageLike(arg)
	case arg == tok, strings.HasPrefix(arg, tok+"="):
		return true
	}
	// -pfoo is -p foo: a one-letter short flag takes its value attached.
	return len(tok) == 2 && tok[0] == '-' && tok[1] != '-' && strings.HasPrefix(arg, tok)
}

// packageLike says whether one argument names a Go package pattern (atoms.PackageLike).
func packageLike(arg string) bool { return atoms.PackageLike(arg) }

// checkDefaults is the parse-time rule over a manifest's default groups.
func checkDefaults(m Manifest) error {
	if len(m.Default) > 0 && m.Args != "append" {
		return fmt.Errorf("default needs args = \"append\" (args is %s)", m.Args)
	}
	for i, d := range m.Default {
		if len(d.Args) == 0 {
			return fmt.Errorf("default[%d] has no args", i)
		}
		for _, tok := range d.Unless {
			switch {
			case tok == "":
				return fmt.Errorf("default[%d] has an empty unless token", i)
			case strings.HasPrefix(tok, "@") && tok != PackageToken:
				return fmt.Errorf("default[%d] unless %s is not %s", i, tok, PackageToken)
			}
		}
	}
	return nil
}

// AllArgv is the manifest's argv followed by every default group's arguments:
// everything the manifest can put on the command line of its own, which is
// what the one-command rules (R46) read.
func (m Manifest) AllArgv() []string {
	out := slices.Clone(m.Argv)
	for _, d := range m.Default {
		out = append(out, d.Args...)
	}
	return out
}
