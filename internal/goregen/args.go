// Package goregen is the work of the atom dev:go-regen (atoms/dev/go-regen): it
// regenerates the cores of stellar-core-go that gravity generates, on the
// cluster, and writes what it regenerated into the tree it was handed. The atom's
// file holds its declaration only, because an atom's directory holds one file
// (DEF-B4-021); the steps live here.
//
// A gravity core is a function of four pinned things: stellar-core-rust at a
// commit, the rustc its provenance.json records, the wasm-tools and wac that
// compose it, and the gravity commit that binds it. This runs the chain a core's
// own build-guest.sh and go:generate lines describe, without a shell: fetch the
// commit, build the guest the recorded way under the recorded compiler, run the
// recorded generator over it, and rewrite provenance.json's hashes in place. It
// is the person's `just <core>-regen` run by the module.
package goregen

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// NewCore is a core that has no provenance.json or build-guest.sh yet:
// `--new <core> --rev <tag-or-commit> --world <aiws:pkg/world@ver>`.
type NewCore struct {
	// Core is the directory of the tree the core is written to, and the Go
	// package gravity is told to name.
	Core string
	// Rev is a stellar-core-rust tag or a 40-hex commit.
	Rev string
	// World is the WIT world the guest exports, as aiws:<package>/<world>@<version>.
	World string
}

// Request is what the atom was asked for: the cores to regenerate, in the order
// named, and the cores to bring into being.
type Request struct {
	Existing []string
	New      []NewCore
}

// World is a parsed `aiws:<package>/<world>@<version>`. The package names the
// stellar-core-rust feature (wit-guest-<package>) and the compose.sh world; the
// world's own name is what gravity is told to call the bindings (--name).
type World struct{ Pkg, Name, Version string }

var (
	coreRE  = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	revRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	worldRE = regexp.MustCompile(`^aiws:([a-z][a-z0-9-]*)/([a-z][a-z0-9-]*)@([0-9]+\.[0-9]+\.[0-9]+)$`)
)

// ParseWorld reads an aiws:<package>/<world>@<version> world id.
func ParseWorld(s string) (World, error) {
	m := worldRE.FindStringSubmatch(s)
	if m == nil {
		return World{}, fmt.Errorf("%q is not aiws:<package>/<world>@<version>", s)
	}
	return World{Pkg: m[1], Name: m[2], Version: m[3]}, nil
}

// ParseArgs reads the dev request's arguments. A bare word is an existing core.
// `--new` opens a new core, and the `--rev` and `--world` that follow belong to
// it (each once, in either order, spaced or joined with `=`); the option may be
// repeated, and mixes with bare cores anywhere. An empty request is not an
// error: the atom answers it with the cores it could have regenerated.
func ParseArgs(args []string) (Request, error) {
	var req Request
	seen := map[string]bool{}
	name := func(core string) error {
		if !coreRE.MatchString(core) {
			return fmt.Errorf("%q is not a core directory name (lower-case letters and digits)", core)
		}
		if seen[core] {
			return fmt.Errorf("%s is named twice", core)
		}
		seen[core] = true
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			if err := name(arg); err != nil {
				return Request{}, err
			}
			req.Existing = append(req.Existing, arg)
			continue
		}
		opt, value, joined := strings.Cut(arg, "=")
		if opt != "--new" && opt != "--rev" && opt != "--world" {
			return Request{}, fmt.Errorf("unknown option %s", opt)
		}
		if !joined {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return Request{}, fmt.Errorf("%s needs a value", opt)
			}
			i++
			value = args[i]
		}
		if opt == "--new" {
			if err := name(value); err != nil {
				return Request{}, err
			}
			req.New = append(req.New, NewCore{Core: value})
			continue
		}
		if len(req.New) == 0 {
			return Request{}, fmt.Errorf("%s names no core: it follows a --new", opt)
		}
		cur := &req.New[len(req.New)-1]
		field := &cur.Rev
		if opt == "--world" {
			field = &cur.World
		}
		if *field != "" {
			return Request{}, fmt.Errorf("%s given twice for %s", opt, cur.Core)
		}
		*field = value
	}
	return req, checkNew(req.New)
}

// checkNew holds each new core to a rev and a world that read.
func checkNew(cores []NewCore) error {
	var errs []error
	for _, c := range cores {
		switch {
		case c.Rev == "":
			errs = append(errs, fmt.Errorf("--new %s needs --rev", c.Core))
		case !revRE.MatchString(c.Rev) || strings.Contains(c.Rev, ".."):
			errs = append(errs, fmt.Errorf("--rev %q is not a tag or a commit", c.Rev))
		case c.World == "":
			errs = append(errs, fmt.Errorf("--new %s needs --world", c.Core))
		default:
			if _, err := ParseWorld(c.World); err != nil {
				errs = append(errs, fmt.Errorf("--world for %s: %v", c.Core, err))
			}
		}
	}
	return errors.Join(errs...)
}
