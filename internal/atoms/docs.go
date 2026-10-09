package atoms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"dagger/foundry-tools/internal/checks"
)

// stopJustifications: no silent suppression of any gate.
//
// THE CHAIN'S OWN CALL. checks.StopJustifications(SJInput{...}) is the scan;
// what moves is who answers its two questions. The chain asked `git ls-files`
// and `src.File(p).Contents`; here Collect asked the same git and os.ReadFile
// answers the reads. The scan asks only for what checks.SJReads names (a test
// holds it to that), so reading lazily reads exactly what the chain fetched
// up front.
//
// A TREE THAT WILL NOT LIST IS A CANNOT RUN, not an empty scan: pre-commit
// hides a passing hook's output, so a silent skip is indistinguishable from a
// clean scan. A file that will not read reaches the scan as its read error,
// which the scan files itself.
//
// No origin is a repository that names no exemption, not a refusal: the
// checkout's directory is not a name (DirectoryExempt is keyed on the remote,
// the one coordinate a checkout cannot rename).
func stopJustifications(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.TrackedErr != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "stop-justifications: CANNOT RUN — "+in.TrackedErr.Error()+
			"\nstop-justifications: refusing to report success without scanning.")
	}
	repo := ""
	if in.Origin != "" {
		repo = checks.RepoFromOrigin(in.Origin)
	}
	state, out := checks.StopJustifications(checks.SJInput{
		Tracked: in.Tracked,
		Read: func(rel string) (string, error) {
			b, err := os.ReadFile(filepath.Join(in.Root, rel))
			return string(b), err
		},
		Repo: repo,
		// The UTC date: a justification expires on one, which is why this atom
		// is whole-tree scope even though it only reads tracked files.
		Today: in.Now.UTC().Format(time.DateOnly),
	})
	return checks.VerdictOf(a, state, out)
}

// checkYAML: every YAML file in the tree parses.
//
// SYNTAX IS THE QUESTION, AND MULTI-DOCUMENT YAML IS VALID HERE. The chain runs
// pre-commit's check-yaml with --allow-multiple-documents --unsafe, which
// parses every document and constructs nothing: a custom tag (Home Assistant's
// !include, an ansible vault) is not "could not determine a constructor", and
// a k3s manifest with a second document is not "expected a single document"
// (both measured on infra, 2026-09-10 and -11; the atom's chain says how).
// yaml.v3 agrees on those two: decoding into a yaml.Node builds a tree, never
// a Go value, so an unknown tag is a node with a tag.
//
// ALIASES ARE NOT RESOLVED, AS THE CHAIN DID NOT RESOLVE THEM. go.yaml.in/yaml/v3
// resolves an alias while it parses, so an undefined or forward alias ("a: *b"
// with no earlier &b) is an error even into a yaml.Node (v3.0.5 decode.go:
// "unknown anchor 'b' referenced"). pre-commit's --unsafe reads only the parse
// EVENTS, which carry the alias by name and resolve nothing, so the chain passes
// that file. The binary now passes it too (syntaxError undefines the alias and
// parses again), because a verdict that turned a pass red on the day the binary
// began to vote would be a change in what the fleet accepts, not a finding.
// Anything else the parser rejects is still a finding.
//
// EVERY DOCUMENT of a file is decoded, to io.EOF. The first error in a file is
// reported with the file and, where the parser has one, the line — "yaml: line
// 3: …". One line per bad file: a second error behind a first is rarely a
// second fact.
func checkYAML(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	var yamls, found, unread []string
	for _, f := range in.Files {
		if strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml") {
			yamls = append(yamls, f)
		}
	}
	for _, f := range yamls {
		if err := ctx.Err(); err != nil {
			return interrupted(a, err)
		}
		body, err := os.ReadFile(filepath.Join(in.Root, f))
		if err != nil {
			unread = append(unread, err.Error())
			continue
		}
		if err := syntaxError(body); err != nil {
			found = append(found, f+": "+err.Error())
		}
	}
	switch {
	case len(yamls) == 0:
		return checks.VerdictOf(a, int(checks.StatePass), "fleet:check-yaml: no YAML in this repository")
	case len(found) > 0:
		return checks.VerdictOf(a, int(checks.StateFindings), strings.Join(found, "\n"))
	case len(unread) > 0:
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %d YAML file(s) could not be read, and a file never parsed is not a file that parses.\n%s",
			a.ID, len(unread), unmeasured(unread)))
	}
	// Silent, as the hook is when it passes: the chain's pass carries no output,
	// so a pass here that printed a count would differ from it in the logs.
	return checks.VerdictOf(a, int(checks.StatePass), "")
}

// syntaxError is the first parse error in any document of body, or nil. An
// empty file has no documents and parses.
//
// A DOCUMENT THAT NAMES AN ANCHOR NOBODY DEFINED parses as if the alias were a
// null: each undefined anchor the parser names is rewritten out of the text
// (undefineAlias) and the file is parsed again, so a real syntax error beside
// the alias is still the error reported. EVERY PASS REMOVES AT LEAST ONE '*', so
// one pass per '*' in the file, and one more to read the result, is the most it
// can take; the bound is what keeps a rewrite that stopped working from hanging
// a gate.
func syntaxError(body []byte) error {
	var err error
	for range bytes.Count(body, []byte("*")) + 1 {
		if err = parseAll(body); err == nil {
			return nil
		}
		m := unknownAnchor.FindStringSubmatch(err.Error())
		if m == nil {
			return err
		}
		body = undefineAlias(body, m[1])
	}
	return err
}

// unknownAnchor is the parser's complaint about an alias with no anchor.
var unknownAnchor = regexp.MustCompile(`unknown anchor '([^']*)' referenced`)

// undefineAlias replaces the alias tokens "*name" with a null scalar. The
// scanner ends an alias's name at the first character that is not a letter,
// digit, '_' or '-' (yaml_parser_scan_anchor), so that is where the token ends;
// a "*name" inside a quoted string is rewritten too, which changes a string's
// text and nothing about whether the file parses.
func undefineAlias(body []byte, name string) []byte {
	re := regexp.MustCompile(`(?m)\*` + regexp.QuoteMeta(name) + `($|[^0-9A-Za-z_-])`)
	return re.ReplaceAll(body, []byte("null${1}"))
}

// parseAll decodes every document of body into a yaml.Node, to io.EOF.
func parseAll(body []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
