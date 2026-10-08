package atoms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"dagger/foundry-tools/internal/checks"
)

// ops:ansible — every playbook syntax-checked, then ansible-lint at the gating
// profile (min) and a count at the report profile (basic).
//
// A TREE THAT DECLARES COLLECTIONS CANNOT BE SYNTAX-CHECKED WITHOUT THEM:
// ansible-core resolves a module against the collections installed, and
// rob/infra went red on main for exactly that the moment its first collection
// landed (2026-09-09). The chain installed them per run, from galaxy, and
// MEASURED over every gate receipt 2026-09-09 to 17 the install was where
// ops:ansible could not run: 32 trees, 31 of them 2026-09-14, the proxy answering
// 404 on a cold miss. The collections are in the tools container now, installed
// at build (atoms_tools_python.go), and what is left to decide at run time is
// whether the tree wants only what the container carries.
//
// IT WRITES, SO IT RUNS IN A PRIVATE TREE. ansible-lint makes .ansible/ and its
// cache in the project it lints, and ansible keeps its temp under ANSIBLE_HOME
// (the user's ~/.ansible by default): neither belongs in the tree the other atoms
// are reading. The tree it grades is a copy of every file the repository would
// commit, made a repository, in a directory of this run's own; the cache homes
// are inside it. The chain's /src was an engine copy and had the same git index
// (`git init`, `git add -A`) for the same reason: ansible-lint discovers its files
// through git.
//
// THE PROGRAMS ARE THE VENV'S: ansible-playbook and ansible-lint are entry points
// of the packages the container installed, run by name. The chain's argv was
// `uv run --isolated --no-project --with ansible-core [--with ansible-lint] <tool>`,
// which resolved both on every cold cache; the arguments after the tool are
// unchanged.

// ansibleCollectionsDir is where the container's collections are; a variable so a
// test lays a directory of its own.
var ansibleCollectionsDir = AnsibleCollectionsDir

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
		version, _ := e["version"].(string)
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
		if c.Version == "" || c.Version == "*" || c.Version == have {
			continue
		}
		if strings.ContainsAny(c.Version, "<>=!~,") {
			gaps = append(gaps, fmt.Sprintf("%s %s (a range; the container carries %s)", c.Name, c.Version, have))
			continue
		}
		gaps = append(gaps, fmt.Sprintf("%s %s (the container carries %s)", c.Name, c.Version, have))
	}
	return gaps
}

// opsAnsible: see the file's header.
func opsAnsible(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
		return *stop
	}
	files := checks.OpsFiles(in.Committable)
	playbooks := checks.OpsPlaybooks(files)
	if len(playbooks) == 0 {
		return opsPhaseAbsent(a, "no ansible/playbooks in this tree")
	}
	if stop := probeTool(ctx, a, in, "ansible-playbook", "--version"); stop != nil {
		return *stop
	}
	if stop := probeTool(ctx, a, in, "python3", "-c", "import ansiblelint"); stop != nil {
		return *stop
	}
	tracked := func(p string) bool { return slices.Contains(in.Committable, p) }
	env := slices.Clone(pythonEnv)
	var out strings.Builder
	if tracked("ansible/requirements.yml") {
		declared, err := in.tree().read("ansible/requirements.yml")
		if err != nil {
			return neverRan(a, err.Error())
		}
		refs, err := declaredCollections(declared)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("ansible/requirements.yml does not parse (%v)\nansible: declares collections that would not install — did not look", err))
		}
		if gaps := collectionGaps(refs, ansibleCollectionsDir); len(gaps) > 0 {
			return checks.VerdictOf(a, int(checks.StateCannotRun), "the tools container does not carry what ansible/requirements.yml declares: "+strings.Join(gaps, "; ")+"\nansible: declares collections that would not install — did not look")
		}
		env = append(env, "ANSIBLE_COLLECTIONS_PATH="+ansibleCollectionsDir)
	}
	dir, cleanup, err := privateTree(ctx, in, "ansible")
	if err != nil {
		return neverRan(a, "the private tree ansible runs in could not be made: "+err.Error())
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
		o, rc := in.run(ctx, Cmd{Dir: wd, Name: "ansible-playbook", Args: append(append([]string{"--syntax-check"}, inventory...), pb), Env: env, Both: true})
		if rc < 0 {
			return neverRan(a, o)
		}
		out.WriteString("syntax-check " + pb + "\n" + o)
		if rc != 0 {
			state, report := opsSettled("ansible", rc, out.String())
			return checks.VerdictOf(a, state, report)
		}
	}
	lint := func(profile string) []string { return []string{"--offline", "-q", "--profile", profile, "."} }
	o, rc := in.run(ctx, Cmd{Dir: wd, Name: "ansible-lint", Args: lint("min"), Env: env, Both: true})
	if rc < 0 {
		return neverRan(a, o)
	}
	out.WriteString("ansible-lint --profile min (gates)\n" + o)
	// The debt is counted off STDOUT alone, on whatever exit: as the chain took it.
	debt, drc := in.run(ctx, Cmd{Dir: wd, Name: "ansible-lint", Args: lint("basic"), Env: env, StdoutOnly: true})
	if drc < 0 {
		return neverRan(a, debt)
	}
	state, report := opsSettled("ansible", rc, out.String())
	report += fmt.Sprintf("\nansible-lint --profile basic: %d finding(s) — reported, not gating", checks.OpsAnsibleDebt(debt))
	return checks.VerdictOf(a, state, report)
}
