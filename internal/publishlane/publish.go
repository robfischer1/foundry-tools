// Package publishlane holds the publish lane's decisions as pure functions:
// what uv build wrote, what the index calls it, what the index answered, and
// whether a file is already released. publish.go at the module root is left
// with the chain.
//
// Ported from stellar_core ci/publish.sh, rule for rule, except that the
// distribution and version are read off the built files instead of out of
// pyproject.toml (publish.go says why).
package publishlane

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Artifact is one file uv build wrote.
type Artifact struct {
	// File is the filename, exactly as the index lists it.
	File string
	// Dist is the distribution segment as the filename spells it.
	Dist string
	// Version is the version the build backend resolved.
	Version string
}

var (
	// wheel is {dist}-{version}(-{build})?-{python}-{abi}-{platform}.whl. No
	// segment carries a hyphen: the wheel spec escapes the distribution's to
	// underscores, and a build tag starts with a digit.
	wheel = regexp.MustCompile(`^([^-]+)-([^-]+)(?:-[0-9][^-]*)?-[^-]+-[^-]+-[^-]+\.whl$`)
	// sdist is {name}-{version}.tar.gz, the name normalised to underscores
	// (PEP 625), which is what uv build writes.
	sdist = regexp.MustCompile(`^([^-]+)-([^-]+)\.tar\.gz$`)
)

// ArtifactOf reads a filename uv build wrote into an Artifact. ok is false for
// anything that is neither a wheel nor an sdist — uv also writes a .gitignore
// into its output directory.
func ArtifactOf(file string) (a Artifact, ok bool) {
	for _, re := range []*regexp.Regexp{wheel, sdist} {
		if m := re.FindStringSubmatch(file); m != nil {
			return Artifact{File: file, Dist: m[1], Version: m[2]}, true
		}
	}
	return Artifact{}, false
}

var separators = regexp.MustCompile(`[-_.]+`)

// IndexName is the project's name on a simple index (PEP 503): lowercase,
// every run of -, _ and . one hyphen. The filenames keep the distribution's
// own spelling, which is why the two are never compared as strings
// (publish.sh index_name).
func IndexName(dist string) string {
	return strings.ToLower(separators.ReplaceAllString(dist, "-"))
}

// Released answers whether a project's simple page lists this exact file. The
// name is matched where a link names it — after the path's slash or the
// anchor's `>`, before its `#sha256` fragment, closing quote or `<` — so a file
// whose name ends with another's is not read as that one.
func Released(page, file string) bool {
	return regexp.MustCompile(`(?:^|[/>"])` + regexp.QuoteMeta(file) + `(?:$|[#"<])`).MatchString(page)
}

// Probe reads curl's answer to the index probe — the page, then a last line
// carrying the HTTP status (`-w '\n%{http_code}'`). A page that is empty
// leaves the status alone on the only line.
func Probe(out string) (status int, page string, err error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := len(lines) - 1
	status, err = strconv.Atoi(strings.TrimSpace(lines[last]))
	if err != nil {
		return 0, "", fmt.Errorf("the index probe answered no status: %.200q", out)
	}
	return status, strings.Join(lines[:last], "\n"), nil
}
