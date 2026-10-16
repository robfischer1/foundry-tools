// Package opsansible is the atom ops:ansible: every playbook is syntax-checked
// and linted at the gating profile. One file holds the atom and both runners
// execute it (lld.md FN-085).
package opsansible

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. ansible-core and ansible-lint are the venv's, with the
// collections the tools container installed at build.
var Def = atomkit.Def{
	ID: "ops:ansible", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every playbook under ansible/playbooks passes ansible-playbook --syntax-check and ansible-lint at profile min; the basic-profile count is reported, not gated.",
	Surface:   "any:**",
	Toolchain: "python",
	Check:     check,
}

const id = "ops:ansible"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// ansibleCollectionsDir is where the container's collections are; a variable so a
// test lays a directory of its own.
var ansibleCollectionsDir = atoms.AnsibleCollectionsDir

// collectionRef is one collection a tree's requirements.yml declares.
type collectionRef struct {
	Name, Version string
}

// declaredCollections reads the collections a requirements.yml declares: each
// entry is a bare name or a mapping with a name and, usually, a version. Roles
// are not read; the file's only other key.
func declaredCollections(requirements string) ([]collectionRef, error) {
	var doc struct {
		Collections []any `yaml:"collections"`
	}
	if err := yaml.Unmarshal([]byte(requirements), &doc); err != nil {
		return nil, err
	}
	var out []collectionRef
	for _, entry := range doc.Collections {
		if bare, ok := entry.(string); ok {
			out = append(out, collectionRef{Name: bare})
			continue
		}
		e, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("a collection entry is neither a name nor a mapping: %v", entry)
		}
		name, _ := e["name"].(string)
		// Sprint, not a string assertion: an unquoted `version: 6` is an int, and
		// dropping it would read as "any version" and pass.
		var version string
		if v := e["version"]; v != nil {
			version = fmt.Sprint(v)
		}
		if name == "" {
			return nil, fmt.Errorf("a collection entry names none: %v", e)
		}
		out = append(out, collectionRef{Name: name, Version: version})
	}
	return out, nil
}

// provisionedVersion is the version of a collection the container installed, off
// the MANIFEST.json galaxy writes into it; "" when it is not there. A name with no
// dot, a manifest that is missing and one that is not JSON all leave the version
// empty (json.Unmarshal checks the whole body before it sets anything), so none
// of them is a branch of its own.
func provisionedVersion(dir, name string) string {
	namespace, collection, _ := strings.Cut(name, ".")
	body, _ := os.ReadFile(filepath.Join(dir, "ansible_collections", namespace, collection, "MANIFEST.json"))
	var manifest struct {
		Info struct {
			Version string `json:"version"`
		} `json:"collection_info"`
	}
	_ = json.Unmarshal(body, &manifest)
	return manifest.Info.Version
}

// collectionGaps are the declared collections the container does not carry at the
// version the tree asks for, each with what it does carry. A tree that names no
// version takes whatever is installed; a version that is a range rather than a
// release is not one this check can hold the container to, so it is a gap that
// says so.
func collectionGaps(declared []collectionRef, dir string) []string {
	var gaps []string
	for _, c := range declared {
		have := provisionedVersion(dir, c.Name)
		if have == "" {
			gaps = append(gaps, fmt.Sprintf("%s %s (the container carries none)", c.Name, c.Version))
			continue
		}
		// A leading == pins one release, as galaxy reads it: not a range.
		want := strings.TrimPrefix(c.Version, "==")
		if want == "" || want == "*" || want == have {
			continue
		}
		if strings.ContainsAny(want, "<>=!~,") {
			gaps = append(gaps, fmt.Sprintf("%s %s (unsupported version range; the container carries %s)", c.Name, c.Version, have))
			continue
		}
		gaps = append(gaps, fmt.Sprintf("%s %s (the container carries %s)", c.Name, c.Version, have))
	}
	return gaps
}

// check: see the file's header.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if stop := atomlib.OpsSurface(row, in); stop != nil {
		return *stop
	}
	files := checks.OpsFiles(in.Committable)
	playbooks := checks.OpsPlaybooks(files)
	if len(playbooks) == 0 {
		return atomlib.PhaseAbsent(row, "no ansible/playbooks in this tree")
	}
	if stop := atomlib.ProbeTool(ctx, row, in, "ansible-playbook", "--version"); stop != nil {
		return *stop
	}
	if stop := atomlib.ProbeTool(ctx, row, in, "python3", "-c", "import ansiblelint"); stop != nil {
		return *stop
	}
	tracked := func(p string) bool { return slices.Contains(in.Committable, p) }
	env := slices.Clone(atoms.PythonEnv())
	var out strings.Builder
	if tracked("ansible/requirements.yml") {
		declared, err := in.Tree().Read("ansible/requirements.yml")
		if err != nil {
			return atomlib.NeverRan(row, err.Error())
		}
		refs, err := declaredCollections(declared)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("ansible/requirements.yml does not parse (%v)\nansible: declares collections that would not install — did not look", err))
		}
		if gaps := collectionGaps(refs, ansibleCollectionsDir); len(gaps) > 0 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), "the tools container does not carry what ansible/requirements.yml declares: "+strings.Join(gaps, "; ")+"\nansible: declares collections that would not install — did not look")
		}
		env = append(env, "ANSIBLE_COLLECTIONS_PATH="+ansibleCollectionsDir)
	}
	dir, cleanup, err := atomlib.PrivateTree(ctx, in, "ansible")
	if err != nil {
		return atomlib.NeverRan(row, "the private tree ansible runs in could not be made: "+err.Error())
	}
	defer cleanup()
	// The user-level homes, inside the private tree: ansible's temp and the
	// lint's cache would otherwise be the invoking user's.
	env = append(env, "ANSIBLE_HOME="+filepath.Join(dir, ".ansible-home"), "XDG_CACHE_HOME="+filepath.Join(dir, ".cache-home"))
	wd := filepath.Join(dir, "ansible")

	var inventory []string
	if tracked("ansible/inventory/hosts.yml") {
		inventory = []string{"-i", "inventory/hosts.yml"}
	}
	for _, pb := range playbooks {
		o, rc := in.Run(ctx, atoms.Cmd{Dir: wd, Name: "ansible-playbook", Args: append(append([]string{"--syntax-check"}, inventory...), pb), Env: env, Both: true})
		if rc < 0 {
			return atomlib.NeverRan(row, o)
		}
		out.WriteString("syntax-check " + pb + "\n" + o)
		if rc != 0 {
			state, report := atomlib.OpsSettled("ansible", rc, out.String())
			return checks.VerdictOf(row, state, report)
		}
	}
	lint := func(profile string) []string { return []string{"--offline", "-q", "--profile", profile, "."} }
	o, rc := in.Run(ctx, atoms.Cmd{Dir: wd, Name: "ansible-lint", Args: lint("min"), Env: env, Both: true})
	if rc < 0 {
		return atomlib.NeverRan(row, o)
	}
	out.WriteString("ansible-lint --profile min (gates)\n" + o)
	// The debt is counted off STDOUT alone, on whatever exit: as the chain took it.
	debt, drc := in.Run(ctx, atoms.Cmd{Dir: wd, Name: "ansible-lint", Args: lint("basic"), Env: env, StdoutOnly: true})
	if drc < 0 {
		return atomlib.NeverRan(row, debt)
	}
	state, report := atomlib.OpsSettled("ansible", rc, out.String())
	report += fmt.Sprintf("\nansible-lint --profile basic: %d finding(s) — reported, not gating", checks.OpsAnsibleDebt(debt))
	return checks.VerdictOf(row, state, report)
}
