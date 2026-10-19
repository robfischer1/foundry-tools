// Package catalogue is the one place the atoms that live in their own files are
// gathered: every logic atom (atoms/<ns>/<name>/<name>.go) and every command
// manifest (atoms/<ns>/<name>.toml). It sits ABOVE the atoms, because they
// import internal/atomkit and internal/checks, which therefore cannot import
// them. registry_gen.go is generated from the tree (generate.go); this file
// reads it and hands the rest of the module what it needs:
//
//   - internal/checks the catalogue rows (checks.UseDefs), so AtomByID, Atoms()
//     and the stage lists see a ported atom exactly as they saw its table row;
//   - internal/atoms the in-process atoms (atoms.UseExtra), so the atoms binary
//     votes for them in its one process, from the same files the adapter compiles;
//   - package main the entry for an id (Lookup), for the Dagger adapter.
//
// Linking this package is what wires both hooks, in init, ahead of every init
// in the binary that imports it.
package catalogue

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// ManifestSource is one command manifest's path and bytes, as registry_gen.go
// carries them: a module cannot embed a file from a directory above its own
// package, so the generator copies the manifests in.
type ManifestSource struct{ Path, Body string }

// Entry is one ported atom: a logic atom's Def, or a command atom's Manifest.
type Entry struct {
	ID string
	// Def is the logic atom's declaration; nil for a command atom.
	Def *atomkit.Def
	// Manifest is the command atom's manifest; nil for a logic atom.
	Manifest *atomkit.Manifest
	// ImportPath is the logic atom's package, the one its stub imports.
	ImportPath string
}

// entries is every ported atom, sorted by id.
var entries = build()

// build parses the manifests and joins them with the Defs. A manifest that does
// not parse, or an id given twice, is an authoring error and panics at load: the
// module's tests are the first thing to link this package.
func build() []Entry {
	var out []Entry
	for _, d := range Defs {
		out = append(out, Entry{ID: d.ID, Def: d, ImportPath: importPath(d.ID)})
	}
	for _, s := range ManifestSources {
		m, err := atomkit.ParseManifest([]byte(s.Body))
		if err != nil {
			panic(fmt.Sprintf("catalogue: manifest %s: %v", s.Path, err))
		}
		out = append(out, Entry{ID: m.ID, Manifest: &m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for i := 1; i < len(out); i++ {
		if out[i].ID == out[i-1].ID {
			panic("catalogue: atom " + out[i].ID + " is declared twice")
		}
	}
	return out
}

// importPath is the package a logic atom lives in: atoms/<ns>/<name>.
func importPath(id string) string {
	return "dagger/foundry-tools/atoms/" + strings.Replace(id, ":", "/", 1)
}

// Entries answers every ported atom, sorted by id.
func Entries() []Entry { return slices.Clone(entries) }

// Lookup answers the entry for an id.
func Lookup(id string) (Entry, bool) {
	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Ported reports whether an atom lives in its own file.
func Ported(id string) bool {
	_, ok := Lookup(id)
	return ok
}

// toolchainImages are the lane images the toolchains name run in today.
var toolchainImages = map[string]string{
	"go":          checks.ImageGo,
	"python":      checks.ImagePython,
	"rust":        checks.ImageRust,
	"rust-trixie": checks.ImageRust, // the Dagger lane builds cargo-mutants from source on it
	"rust-wasm":   checks.ImageRustWasm,
	"bun":         checks.ImageTS,
	// the pod runs ts:visual in a Playwright image; the Dagger runner installs the
	// browsers into the ts lane
	"playwright": checks.ImageTS,
	"node":       checks.ImageNode,
	"renovate":   checks.ImageRenovate,
}

// imageOf is the lane image an atom of this toolchain runs in today. "none"
// runs in the fleet lane's.
func imageOf(toolchain string) string {
	if img, ok := toolchainImages[toolchain]; ok {
		return img
	}
	return checks.ImageFleet
}

// Rows are the catalogue rows of the ported atoms, sorted by id.
func Rows() []checks.AtomDef {
	rows := make([]checks.AtomDef, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, e.row())
	}
	return rows
}

// row is the AtomDef the planner, the vector and the absent sentence read.
func (e Entry) row() checks.AtomDef {
	if d := e.Def; d != nil {
		return checks.AtomDef{
			ID: d.ID, Stage: d.Stage, Lane: d.Lane, Image: imageOf(d.Toolchain), Desc: d.Desc,
			SubsumedBy: d.SubsumedBy, NeedsDies: slices.Contains(d.Needs, "dies"),
			NeedsManifest: d.NeedsManifest, NotOnForks: d.NotOnForks,
		}
	}
	m := e.Manifest
	lane := checks.Lane(m.Lane)
	return checks.AtomDef{
		ID: m.ID, Stage: m.Stage, Lane: lane, Image: imageOf(m.Toolchain), Desc: m.Desc,
		SubsumedBy: m.SubsumedBy, NeedsDies: slices.Contains(m.Needs, "dies"),
		NeedsManifest: needsManifest(lane, m.Surface), NotOnForks: m.NotOnForks,
	}
}

// needsManifest is FN-085 step 2's rule read backwards: a lane that is declared
// by its files (go, python) has a root-manifest surface only for an atom whose
// subject is the project, and that atom's absent sentence names the manifest.
// For a lane the root manifest itself declares (rust, ts) root:<manifest> is
// the lane's own surface and says nothing more.
func needsManifest(lane checks.Lane, surface string) bool {
	m := checks.ManifestFor(lane)
	return m != "" && surface == "root:"+m && checks.LaneDeclaredByFiles(lane)
}

// Atoms are the in-process atoms the atoms binary carries: every logic atom
// whose toolchain is none and the declared ones that were in-process before
// they moved (binary.go). They run in its one process, from the same package
// the adapter compiles, so no atom exists twice.
func Atoms() []atoms.Atom {
	var out []atoms.Atom
	for _, e := range entries {
		if d := e.Def; d != nil && votesInBinary(d) {
			out = append(out, inProcess(*d))
		}
	}
	return out
}

// inProcess wraps a Def as the binary's registration: the Check, with the
// identity a verdict may leave empty filled in from the catalogue row.
func inProcess(d atomkit.Def) atoms.Atom {
	a := atoms.Atom{
		ID: d.ID, Scope: atoms.ScopeTree,
		Run: func(ctx context.Context, row checks.AtomDef, in atoms.Input) checks.Verdict {
			v := d.Check(ctx, in)
			if v.Atom == "" {
				v.Atom = row.ID
			}
			if v.Stage == "" {
				v.Stage = row.Stage
			}
			if v.Lane == "" {
				v.Lane = string(row.Lane)
			}
			return v
		},
	}
	switch {
	case d.Deadline != nil:
		a.Deadline = func(in atoms.Input) time.Duration { return max(d.Timeout, d.Deadline(in)) }
	case d.Timeout > 0:
		a.Deadline = func(atoms.Input) time.Duration { return d.Timeout }
	}
	return a
}

func init() {
	checks.UseDefs(Rows())
	atoms.UseExtra(Atoms)
}
