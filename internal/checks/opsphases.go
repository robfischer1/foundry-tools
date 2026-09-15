package checks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// THE OPS LANE'S DECISIONS, in Go. They were foundry-stocks' ci/lib/ops/ops.sh
// — its detect, the eight phases the ops atoms run, and the two python
// heredocs inside them (the strict YAML loader and the Flux path reader). The
// atoms (atoms_ops.go) run each phase's tools as plain execs and settle here.
//
// THREE-STATE, LIKE THE GATE. A phase whose tool faulted on the network (a
// schema it could not fetch, a collection that would not download, a registry
// that answered 5xx) did not look: could-not-run. A phase whose tool looked
// and said no is findings. A facet the tree does not carry is ABSENT, named,
// never a green read as coverage.

// opsFault is a network fault in a tool's output: the substrate, not the work.
var opsFault = regexp.MustCompile(`(?i)connection refused|connection reset|i/o timeout|no such host|server misbehaving|TLS handshake timeout|502 Bad Gateway|503 Service Unavailable|504 Gateway|unexpected EOF|too many requests|429 Too Many Requests|toomanyrequests|context deadline exceeded|failed to do request|Could not resolve host|Temporary failure in name resolution|could not download|Failed to download|error downloading|no such schema|failed to fetch|dial tcp`)

// OpsSettle is a phase's state from its tools' combined exit and output: 0 is
// a pass; anything else is could-not-run when the output names a fault of the
// substrate, and findings otherwise. The line says which.
func OpsSettle(phase string, rc int, out string) (int, string) {
	if rc == 0 {
		return 0, ""
	}
	if hit := opsFault.FindString(out); hit != "" {
		return 2, fmt.Sprintf("%s failed on a fault of the substrate (rc=%d): %s — did not look; run it again", phase, rc, hit)
	}
	return 1, fmt.Sprintf("%s failed (rc=%d) — findings", phase, rc)
}

// OpsFile is one tracked file: its path and its git mode.
type OpsFile struct {
	Path string
	Mode string
}

// OpsTracked reads `git ls-files -s -z` into the tracked files.
func OpsTracked(lsFiles string) []OpsFile {
	var out []OpsFile
	for _, rec := range strings.Split(lsFiles, "\x00") {
		// "<mode> <object> <stage>\t<path>"
		meta, p, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		out = append(out, OpsFile{Path: p, Mode: strings.Fields(meta)[0]})
	}
	return out
}

func opsHas(files []OpsFile, match func(OpsFile) bool) bool {
	for _, f := range files {
		if match(f) {
			return true
		}
	}
	return false
}

func opsIsYAML(p string) bool { return path.Ext(p) == ".yml" || path.Ext(p) == ".yaml" }

// OpsHasTool reports whether the tree carries one of infra's own checkers at
// tools/<name>; executable says it must be tracked executable (dup-check runs
// as a program, the other two through uv).
func OpsHasTool(files []OpsFile, name string, executable bool) bool {
	return opsHas(files, func(f OpsFile) bool {
		return f.Path == "tools/"+name && (!executable || f.Mode == "100755")
	})
}

// OpsPlaybooks is every ansible/playbooks/*.yml, sorted, relative to ansible/.
func OpsPlaybooks(files []OpsFile) []string {
	var out []string
	for _, f := range files {
		if rest, ok := strings.CutPrefix(f.Path, "ansible/playbooks/"); ok && !strings.Contains(rest, "/") && path.Ext(rest) == ".yml" {
			out = append(out, "playbooks/"+rest)
		}
	}
	sort.Strings(out)
	return out
}

// opsShebang is a first line that names a shell, as detect read it; a zsh
// shebang is excluded after, because shellcheck has no zsh.
var opsShebang = regexp.MustCompile(`^#!.*(bash|dash|ksh|zsh|/sh$|/sh |env +sh)`)

// OpsShellFiles splits the tracked files shellcheck reads into those whose
// first line is a shebang and the SOURCED FRAGMENTS that correctly have none
// (read as bash, or shellcheck answers SC2148 "shell is unknown" on a file
// that must not carry the line). firstLines maps a path to its first line,
// for the files whose first line starts "#!".
//
// TWO EXCLUSIONS, BOTH PRINCIPLED: *.tmpl is Go template text (the chezmoi
// phase checks those) and *.zsh is a dialect shellcheck does not have
// (SC2168 on a zsh `local`, measured). A file that is neither .sh nor .bash is
// read only when its shebang says a shell other than zsh.
func OpsShellFiles(files []OpsFile, firstLines map[string]string) (shebang, fragments []string) {
	for _, f := range files {
		line, hasShebang := firstLines[f.Path]
		switch path.Ext(f.Path) {
		case ".tmpl", ".zsh":
			continue
		case ".sh", ".bash":
			if hasShebang {
				shebang = append(shebang, f.Path)
			} else {
				fragments = append(fragments, f.Path)
			}
		default:
			if opsShebang.MatchString(line) && !strings.Contains(line, "zsh") {
				shebang = append(shebang, f.Path)
			}
		}
	}
	return shebang, fragments
}

// OpsFirstLines reads `git grep -I -n -z -E '^#!'` into the first lines that
// start "#!", by path: a match on line 1 only.
func OpsFirstLines(grep string) map[string]string {
	out := map[string]string{}
	for _, ln := range strings.Split(grep, "\n") {
		// "<path>\x00<lineno>\x00<text>"
		parts := strings.SplitN(ln, "\x00", 3)
		if len(parts) == 3 && parts[1] == "1" {
			out[parts[0]] = parts[2]
		}
	}
	return out
}

// OpsDebt counts shellcheck's gcc-format findings at the report severity: the
// debt, counted and never gating.
func OpsDebt(out string) int {
	n := 0
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, ":") {
			n++
		}
	}
	return n
}

var ansibleLintFinding = regexp.MustCompile(`(?m)^[a-z][a-z0-9-]*(\[[a-z-]+\])?: `)

// OpsAnsibleDebt counts ansible-lint's findings at the report profile.
func OpsAnsibleDebt(out string) int { return len(ansibleLintFinding.FindAllString(out, -1)) }

// OpsChezmoiTemplates is the tracked *.tmpl of a chezmoi SOURCE tree — one
// with chezmoi's own marker files or dot_* naming at the root — or nothing: a
// source tree with no template has nothing to render, and a tree that is not a
// source tree is not asked.
func OpsChezmoiTemplates(files []OpsFile) []string {
	source := opsHas(files, func(f OpsFile) bool {
		switch f.Path {
		case ".chezmoiignore", ".chezmoiroot", ".chezmoiversion":
			return true
		}
		first, _, _ := strings.Cut(f.Path, "/")
		return strings.HasPrefix(first, "dot_")
	})
	var out []string
	for _, f := range files {
		if source && path.Ext(f.Path) == ".tmpl" {
			out = append(out, f.Path)
		}
	}
	return out
}

// OpsYAMLFiles is every tracked .yml and .yaml.
func OpsYAMLFiles(files []OpsFile) []string {
	var out []string
	for _, f := range files {
		if opsIsYAML(f.Path) {
			out = append(out, f.Path)
		}
	}
	return out
}

// OpsStrictYAML reports the first reason a YAML stream does not load
// strictly: a parse error, or a duplicate key, which every loader the fleet
// runs silently resolves last-wins. nil when every document loads.
func OpsStrictYAML(content string) error {
	dec := yaml.NewDecoder(strings.NewReader(content))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// OpsYAMLReport is the yaml phase's report over the files and their load
// errors, in the order given: one line per bad file (its error, on one line)
// and a count, and whether any was bad.
func OpsYAMLReport(files []string, errs map[string]error) (string, bool) {
	var b strings.Builder
	bad := 0
	for _, f := range files {
		if err := errs[f]; err != nil {
			bad++
			fmt.Fprintf(&b, "%s: %s\n", f, strings.Join(strings.Fields(err.Error()), " "))
		}
	}
	fmt.Fprintf(&b, "%d yaml file(s), %d with a duplicate key or a parse error\n", len(files), bad)
	return b.String(), bad > 0
}

// OpsFluxPaths reads the Flux Kustomization CRs among the given manifests
// into the trees they apply, sorted and each named once, the way
// kustomize-controller reads spec.path; and names each file that did not
// parse. A document that is not a Kustomization of kustomize.toolkit.fluxcd.io
// names nothing.
func OpsFluxPaths(manifests map[string]string) ([]string, []string) {
	seen := map[string]bool{}
	var problems []string
	names := make([]string, 0, len(manifests))
	for n := range manifests {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		dec := yaml.NewDecoder(bytes.NewReader([]byte(manifests[name])))
		for {
			var doc struct {
				APIVersion string `yaml:"apiVersion"`
				Kind       string `yaml:"kind"`
				Spec       struct {
					Path string `yaml:"path"`
				} `yaml:"spec"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				problems = append(problems, name+": "+err.Error())
				break
			}
			if doc.Kind != "Kustomization" || !strings.HasPrefix(doc.APIVersion, "kustomize.toolkit.fluxcd.io") {
				continue
			}
			p := doc.Spec.Path
			for strings.HasPrefix(p, "./") {
				p = p[2:]
			}
			if p = strings.Trim(p, "/"); p != "" {
				seen[p] = true
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, problems
}

// OpsFluxFallback is every flux/<dir> that carries a kustomization.yaml, for
// a flux/ tree with no Kustomization CR.
func OpsFluxFallback(files []OpsFile) []string {
	var out []string
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		if len(parts) == 3 && parts[0] == "flux" && parts[2] == "kustomization.yaml" {
			out = append(out, parts[0]+"/"+parts[1])
		}
	}
	sort.Strings(out)
	return out
}

// OpsFluxClusterManifests is every tracked flux/clusters/**/*.yaml.
func OpsFluxClusterManifests(files []OpsFile) []string {
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f.Path, "flux/clusters/") && path.Ext(f.Path) == ".yaml" {
			out = append(out, f.Path)
		}
	}
	return out
}

// OpsKinds counts the objects in a built stream: its `kind:` lines at column 0.
func OpsKinds(stream string) int {
	n := 0
	for _, ln := range strings.Split(stream, "\n") {
		if strings.HasPrefix(ln, "kind:") {
			n++
		}
	}
	return n
}

// OpsBatches splits a file list into argument lists that each fit the argv
// budget, in order — xargs' job in the shell body.
func OpsBatches(files []string) [][]string {
	var out [][]string
	var batch []string
	size := 0
	for _, f := range files {
		if len(batch) > 0 && size+len(f)+1 > argvBudget {
			out = append(out, batch)
			batch, size = nil, 0
		}
		batch = append(batch, f)
		size += len(f) + 1
	}
	if len(batch) > 0 {
		out = append(out, batch)
	}
	return out
}
