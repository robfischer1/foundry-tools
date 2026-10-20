// Package bundlekit is the bundle lane's build, as the executor runs it: the
// atoms bundle:policy, bundle:fleet and bundle:resign build and
// gate the dies foundry-dies publishes, and leave them in the bundle out
// directory for the lane's capabilities to sign and push. The decisions are
// internal/bundlelane's, which the Dagger bundle lane (bundle.go at the module
// root) runs too; this package is the chain.
//
// WHAT IS LEFT FOR THE CAPABILITIES is everything that touches the registry or
// the KMS: pushing, the immutable pins, the cosign signature of every pushed
// digest, and the KMS signature over the OPA twins' signing inputs. The atoms
// read no credential and write no registry.
package bundlekit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// The out directory, the staged tree and the flux checkout are pod paths the
// capabilities and the fetch role share; each has a variable so a test can say
// where it is.
const (
	// OutEnv names the directory the bundle atoms leave the dies in. The pod
	// gives the bundle lane's atoms /work/bundle (daedalus serve atomenv).
	OutEnv = "ATOM_BUNDLE_OUT"
	// StageEnv and StageDefault are where the roster is staged. The path is part
	// of the artifact: opa names a rego file in a bundle by the path it was
	// built from, so the Dagger lane's /work/stage is the executor's.
	StageEnv     = "BUNDLE_STAGE_DIR"
	StageDefault = "/work/stage"
	// FluxEnv and FluxDefault are the foundry/flux checkout the fetch role lays
	// for the bundle lane: its sparse tree and FLUX_SHA, the commit main
	// resolved to.
	FluxEnv     = "BUNDLE_FLUX_DIR"
	FluxDefault = "/work/flux"
	// FluxShaFile is the file in that checkout naming the commit.
	FluxShaFile = "FLUX_SHA"
)

// osWrite writes a file; a variable so a test can fail a write by its path.
var osWrite = os.WriteFile

// OutDir is where the dies are left.
func OutDir() string {
	if d := os.Getenv(OutEnv); d != "" {
		return d
	}
	return filepath.Join(atoms.WorkDir(), "bundle")
}

// StageDir is where the roster is staged.
func StageDir() string { return envOr(StageEnv, StageDefault) }

// FluxDir is the flux checkout.
func FluxDir() string { return envOr(FluxEnv, FluxDefault) }

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// MetaSchema names the file the atoms leave beside each die; daedalus'
// push-bundle capability reads the same shape (internal/runner/capability).
const MetaSchema = "bundle-meta@1"

// MetaSuffix ends the meta file of a die: <name>.meta.json.
const MetaSuffix = ".meta.json"

// Artifact is one tarball of a bundle die: its file, the suffix its immutable
// pin carries and the channel that is pointed at the pin.
type Artifact struct {
	File    string `json:"file"`
	Suffix  string `json:"suffix"`
	Channel string `json:"channel"`
}

// Meta is what an atom says about a die it built.
type Meta struct {
	Schema string `json:"schema"`
	// Die is the repository the die is pushed to, host qualified.
	Die string `json:"die"`
	// Kind is "bundle" (tarballs, pushed as one layer each) or "tree" (a
	// directory of files, one layer per file).
	Kind string `json:"kind"`
	// Pin is the immutable tag, Revision the annotation it carries.
	Pin      string `json:"pin"`
	Revision string `json:"revision"`
	// Sha is the foundry-dies commit.
	Sha string `json:"sha"`
	// Publish says this landing owes the die a push; PublishIfMissing says it
	// is owed one when the pin does not stand yet (the fleet die, which is a
	// function of flux as well).
	Publish          bool `json:"publish"`
	PublishIfMissing bool `json:"publish_if_missing,omitempty"`
	// Artifacts are a bundle die's tarballs, in push order.
	Artifacts []Artifact `json:"artifacts,omitempty"`
	// Dir and Files are a tree die's: the directory under the out directory and
	// the files in it.
	Dir   string   `json:"dir,omitempty"`
	Files []string `json:"files,omitempty"`
	// Signing names the tarballs whose OPA signature the KMS makes: each has a
	// <file>.signing-input beside it.
	Signing []string `json:"signing,omitempty"`
}

// WriteMeta leaves m as <name>.meta.json in the out directory.
func WriteMeta(name string, m Meta) error {
	m.Schema = MetaSchema
	b, _ := json.MarshalIndent(m, "", " ") // a struct of strings and booleans always encodes
	if err := os.MkdirAll(OutDir(), 0o755); err != nil {
		return fmt.Errorf("the bundle directory could not be made: %w", err)
	}
	if err := osWrite(filepath.Join(OutDir(), name+MetaSuffix), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("%s%s could not be written: %w", name, MetaSuffix, err)
	}
	return nil
}

// ReadMeta reads the meta file of die name.
func ReadMeta(name string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(filepath.Join(OutDir(), name+MetaSuffix))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s%s is not JSON: %w", name, MetaSuffix, err)
	}
	return m, nil
}

// Fault is a step that did not hold: Findings is a claim about the tree, and
// anything else is a step that could not run (the lane's code 1 and 2).
type Fault struct {
	Findings bool
	Msg      string
}

func (f *Fault) Error() string { return f.Msg }

// Findings is a fault about the tree.
func Findings(format string, a ...any) *Fault {
	return &Fault{Findings: true, Msg: fmt.Sprintf(format, a...)}
}

// CouldNotRun is a fault of the lane.
func CouldNotRun(format string, a ...any) *Fault { return &Fault{Msg: fmt.Sprintf(format, a...)} }

// Verdict folds a fault into the atom's verdict; nil is the pass with reason.
func Verdict(row checks.AtomDef, f *Fault, pass string) checks.Verdict {
	switch {
	case f == nil:
		return checks.VerdictOf(row, int(checks.StatePass), row.ID+": "+pass)
	case f.Findings:
		return checks.VerdictOf(row, int(checks.StateFindings), row.ID+": FINDINGS - "+f.Msg)
	}
	return checks.VerdictOf(row, int(checks.StateCannotRun), row.ID+": CANNOT RUN - "+f.Msg)
}
