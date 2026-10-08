package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// WHERE A GOMUTANTS REPORT'S FILE NAMES ARE RELATIVE TO, and why the classifier
// cannot be handed "." and trusted.
//
// gomutants writes `file_name` as the file's path relative to the LONGEST COMMON
// IMPORT-PATH PREFIX of the packages it ran (internal/discover computeRelFile),
// not to the module root and not to the directory it was invoked in. A module
// whose packages span the whole tree has the module path as that prefix, so the
// names happen to be module-relative. A module whose only package is
// cmd/fleet-door has that package as the prefix, and its files are "answer.go".
// mutation-gate then looks for ./answer.go, finds nothing, and refuses the whole
// report as a base-directory mismatch — MEASURED on paneless@1630cf5 (2026-10-08),
// where 37 survivors in cmd/fleet-door read "the unkillability classifier did not
// answer" instead of as survivors.
//
// THE FIX IS TO NAME THE FILES FROM THE ROOT BEFORE ANYONE READS THEM: the base
// is recomputed here from the same `go list` the run resolved its packages with,
// and the report is rewritten to module-relative names. Everything downstream —
// the classifier, the coverage-profile join, the noise keys — then agrees on one
// spelling, with no knowledge of the quirk.

// GoPackagesFormat is the `go list -f` template that names each package's import
// path and directory, tab-separated, one per line.
const GoPackagesFormat = `{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\n"}}`

// GoReportBase answers the module-relative directory gomutants' file names are
// relative to, from GoPackagesFormat's output for the packages the run was given
// and the module's absolute directory root. "" means module-relative already (or
// that nothing could be said), and the report is left as it is.
func GoReportBase(listOut, root string) string {
	type pkg struct{ imp, dir string }
	var pkgs []pkg
	for _, ln := range strings.Split(listOut, "\n") {
		imp, dir, _ := strings.Cut(ln, "\t")
		imp, dir = strings.TrimSpace(imp), strings.TrimSpace(dir)
		if imp == "" || dir == "" {
			continue
		}
		pkgs = append(pkgs, pkg{imp, dir})
	}
	if len(pkgs) == 0 {
		return ""
	}
	// gomutants' longestCommonPrefix, exactly: a string prefix trimmed back to
	// the last "/", not a whole-segment match.
	prefix := pkgs[0].imp
	for _, p := range pkgs[1:] {
		if prefix = commonImportPrefix(prefix, p.imp); prefix == "" {
			return ""
		}
	}
	// The directory the prefix names: any package's dir, less its import path's
	// tail past the prefix.
	first := pkgs[0]
	sub := strings.TrimPrefix(strings.TrimPrefix(first.imp, prefix), "/")
	base := path.Clean(first.dir)
	if sub != "" {
		if !strings.HasSuffix(base, "/"+sub) {
			return ""
		}
		base = strings.TrimSuffix(base, "/"+sub)
	}
	root = path.Clean(root)
	if !strings.HasPrefix(base, root+"/") {
		return ""
	}
	return strings.TrimPrefix(base, root+"/")
}

// RebaseGoReport rewrites every file_name in a gomutants report to be relative
// to the module root, given the directory GoReportBase answered. Every other
// byte of meaning is kept; an empty base returns the report unchanged.
func RebaseGoReport(report []byte, base string) ([]byte, error) {
	if base == "" {
		return report, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(report, &doc); err != nil {
		return nil, fmt.Errorf("the mutation report is not a gomutants report: %v", err)
	}
	var files []map[string]json.RawMessage
	if err := json.Unmarshal(doc["files"], &files); err != nil {
		return nil, fmt.Errorf("the mutation report has no readable files: %v", err)
	}
	for _, f := range files {
		var name string
		if err := json.Unmarshal(f["file_name"], &name); err != nil {
			return nil, fmt.Errorf("a file in the mutation report has no readable file_name: %v", err)
		}
		renamed, _ := json.Marshal(path.Join(base, name))
		f["file_name"] = renamed
	}
	doc["files"], _ = json.Marshal(files)
	return json.Marshal(doc)
}

// commonImportPrefix trims prefix back to the last "/" until imp starts with
// it, trying each "/" from the right. "" is no common prefix.
func commonImportPrefix(prefix, imp string) string {
	if strings.HasPrefix(imp, prefix) {
		return prefix
	}
	for back := range len(prefix) {
		cut := len(prefix) - 1 - back
		if prefix[cut] == '/' && strings.HasPrefix(imp, prefix[:cut]) {
			return prefix[:cut]
		}
	}
	return ""
}
