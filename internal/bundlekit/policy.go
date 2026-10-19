package bundlekit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/bundleresign"
	"dagger/foundry-tools/internal/checks"
)

// The policy die's files in the out directory.
const (
	PolicyPlain  = "bundle.tar.gz"
	PolicySigned = "bundle-signed.tar.gz"
	// SigningInputSuffix ends the file beside a twin holding the header.payload
	// the KMS signs.
	SigningInputSuffix = ".signing-input"
)

// Policy is the policy die, built twice and gated, after the Dagger bundle lane's
// policy() (bundle.go): the decision table derived and laid into a copy of
// policy/, the die built plain and as a twin whose signature the KMS replaces
// (bundle:resign), and the artifact gated for what it carries. The unit suite and
// the admission dogfood are the lane's gate atoms (dies:opa-test,
// dies:admission-dogfood), which run before this.
//
// THE COPY IS NAMED policy AND BUILT FROM ITS PARENT: opa names a rego file in a
// bundle by the path it was built from, so `-b policy/` from the tree's root and
// from a copy's parent are the same bytes, and a copy under any other name is not.
func Policy(ctx context.Context, r Run) *Fault {
	sha, bad := r.Sha(ctx)
	if bad != nil {
		return bad
	}
	pin, err := bundlelane.Pin(sha)
	if err != nil {
		return CouldNotRun("%v", err)
	}
	changed, bad := r.Changed(ctx, sha)
	if bad != nil {
		return bad
	}
	publish, _ := bundlelane.Publishes(changed)

	scratch, err := os.MkdirTemp(atoms.WorkDir(), "bundle-policy-")
	if err != nil {
		return CouldNotRun("a scratch directory could not be made: %v", err)
	}
	defer os.RemoveAll(scratch)
	if err := copyTree(filepath.Join(r.In.Root, "policy"), filepath.Join(scratch, "policy"), nil); err != nil {
		return CouldNotRun("policy/ could not be copied: %v", err)
	}
	table, bad := r.decisions(ctx, scratch)
	if bad != nil {
		return bad
	}
	key, bad := ephemeralKey(scratch)
	if bad != nil {
		return bad
	}
	if err := os.MkdirAll(OutDir(), 0o755); err != nil {
		return CouldNotRun("the bundle directory could not be made: %v", err)
	}
	for _, b := range []struct {
		file string
		sign bool
	}{{PolicyPlain, false}, {PolicySigned, true}} {
		args := []string{"build", "-b", "policy/", "-o", filepath.Join(OutDir(), b.file), "--revision", sha, "--ignore", "*_test.rego"}
		if b.sign {
			args = append(args, "--signing-key", key, "--signing-alg", "ES256")
		}
		out, code, bad := r.Exec(ctx, "opa build", scratch, "", "opa", args...)
		if bad != nil {
			return bad
		}
		if code != 0 {
			return Findings("opa build over policy/ failed:\n%s", out)
		}
	}
	if bad := r.gatePolicy(ctx, scratch, table); bad != nil {
		return bad
	}
	if bad := WriteSigningInput(filepath.Join(OutDir(), PolicySigned)); bad != nil {
		return bad
	}
	meta := Meta{
		Die: bundlelane.PolicyDie, Kind: "bundle", Pin: pin, Revision: sha, Sha: sha, Publish: publish,
		Artifacts: []Artifact{{PolicyPlain, "", "stable"}, {PolicySigned, "-signed", "signed"}, {PolicySigned, "-signed-kms", bundlelane.SignedKMSChannel}},
		Signing:   []string{PolicySigned},
	}
	if err := WriteMeta("policy", meta); err != nil {
		return CouldNotRun("%v", err)
	}
	return nil
}

// ephemeralKey writes the throwaway P-256 key the twin is built with: OPA needs
// SOME key to compute the file hashes and the canonical payload, and the
// signature it makes is replaced by the KMS's.
func ephemeralKey(dir string) (string, *Fault) {
	p := filepath.Join(dir, "sign.key")
	pem, err := bundlelane.EphemeralKey()
	if err == nil {
		err = osWrite(p, []byte(pem), 0o600)
	}
	if err != nil {
		return "", CouldNotRun("an ephemeral signing key could not be made: %v", err)
	}
	return p, nil
}

// WriteSigningInput leaves <twin>.signing-input beside a twin: the
// header.payload its throwaway signature covers, with the key id ouranos pins.
func WriteSigningInput(twin string) *Fault {
	tgz, bad := ReadFile(twin)
	if bad != nil {
		return bad
	}
	input, err := bundleresign.SigningInput(tgz, bundlelane.OPAKeyID)
	if err != nil {
		return CouldNotRun("bundleresign input %s: %v", filepath.Base(twin), err)
	}
	if err := osWrite(twin+SigningInputSuffix, []byte(input), 0o644); err != nil {
		return CouldNotRun("the signing input of %s could not be written: %v", filepath.Base(twin), err)
	}
	return nil
}

// decisions derives foundry-dies' decision table (stark42 A1 S4) and lays it
// into the policy copy as data.grants.decisions. It answers whether it did.
// A tree without the derive ships no table; a table someone committed is refused:
// it would be served as if derived (bundle_decisions.go).
func (r Run) decisions(ctx context.Context, scratch string) (bool, *Fault) {
	t := r.In.Tree()
	if !atoms.Present(t, bundlelane.DecisionsDerive) {
		return false, nil
	}
	if atoms.Present(t, bundlelane.DecisionsPath) {
		return false, Findings("%s is committed, and the lane derives it — a committed table would be served as if derived from this commit", bundlelane.DecisionsPath)
	}
	for _, f := range []string{"fleet/data.json", "fleet/derive.rego"} {
		if !atoms.Present(t, f) {
			return false, Findings("%s is absent — the decision table's roster cannot be staged", f)
		}
	}
	entries, bad := readGlob(t, bundlelane.DeclaredGlob)
	if bad != nil {
		return false, bad
	}
	docs, err := bundlelane.Declared(entries)
	if err != nil {
		return false, Findings("%v", err)
	}
	stage := filepath.Join(scratch, bundlelane.DecisionsStage)
	for _, f := range []string{"fleet/data.json", "fleet/derive.rego"} {
		body, _ := t.Read(f) // Present proved it a file a moment ago
		if err := writeFile(stage, f, body); err != nil {
			return false, CouldNotRun("the decision table's roster could not be staged: %v", err)
		}
	}
	if err := writeFile(stage, bundlelane.DeclaredPath, bundlelane.Stage(docs)); err != nil {
		return false, CouldNotRun("the decision table's roster could not be staged: %v", err)
	}
	out, code, bad := r.Exec(ctx, "the decision table's derive", r.In.Root, "", "opa", bundlelane.DecisionsArgs(stage)[1:]...)
	if bad != nil {
		return false, bad
	}
	if code != 0 {
		return false, Findings("the decision table did not derive:\n%s", out)
	}
	stats, err := bundlelane.Decisions(out)
	if err != nil {
		return false, Findings("%v", err)
	}
	if err := writeFile(scratch, bundlelane.DecisionsPath, out); err != nil {
		return false, CouldNotRun("the decision table could not be laid: %v", err)
	}
	fmt.Fprintf(os.Stderr, "bundle: policy: decision table derived — %s\n", stats)
	return true, nil
}

// gatePolicy interrogates the built plain die: it carries its data and still
// hides what it should.
func (r Run) gatePolicy(ctx context.Context, scratch string, table bool) *Fault {
	plain := filepath.Join(OutDir(), PolicyPlain)
	tgz, bad := ReadFile(plain)
	if bad != nil {
		return bad
	}
	data, ok, err := Member(tgz, "/data.json")
	if err != nil || !ok {
		return Findings("%s carries no /data.json: %v", PolicyPlain, err)
	}
	if _, stars, err := checks.DiesDataKeys(data); err != nil {
		return Findings("the built bundle's data.json is not JSON: %v", err)
	} else if stars == 0 {
		return Findings("the built bundle has an empty star_only — publishing it would make every verb visible to every principal")
	}
	if _, problem := bundlelane.BuiltDecisions(data, table); problem != "" {
		return Findings("%s", problem)
	}
	canary := checks.DiesCanary(data)
	if canary == "" {
		return Findings("chaos has no star_only row in the built bundle — the roster did not survive the build")
	}
	// The audience tier, asked of `callable` (atoms_dies.go, dies:canary-visibility, says why not `visible`).
	probe := filepath.Join(scratch, "probe-session.json")
	if err := osWrite(probe, []byte(fmt.Sprintf(`{"principal":{"type":"session","subject":"s"},"verbs":["search",%q]}`, canary)), 0o644); err != nil {
		return CouldNotRun("the session-principal probe could not be written: %v", err)
	}
	out, code, bad := r.Exec(ctx, "the session-principal probe", scratch, "", "opa",
		"eval", "-b", plain, "-i", probe, "--format", "json", "data.authz.callable.allowed")
	if bad != nil {
		return bad
	}
	if code != 0 {
		return Findings("the session-principal probe did not evaluate against the built bundle:\n%s", out)
	}
	allowed, err := checks.OpaAllowed(out)
	if err != nil {
		return CouldNotRun("the session-principal probe answered a shape this lane cannot read: %v", err)
	}
	if len(allowed) != 1 || allowed[0] != "search" {
		return Findings("curated verb %s is visible to a session principal in the artifact: %v", canary, allowed)
	}
	return nil
}

// Say is the lane's log line, on stderr.
func Say(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bundle: "+format+"\n", args...)
}
