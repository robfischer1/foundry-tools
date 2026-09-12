package checks

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The fleet lane's JUDGEMENTS, as pure functions over strings and ints.
//
// Every one of these was a line of shell inside a Go string — an awk program,
// a `case` on an exit code, a `grep -q` on a tool's output, a sed pipeline —
// and none of it was reachable by a test: package main panics without a Dagger
// engine, and a script in a string is not compiled by anything. The behaviour
// is unchanged; what changed is that a mutant in any of it is now a mutant the
// suite sees.

// FilesOver reads `stat -c '%s %n'` output — one `<size> <path>` line per file
// — and answers the paths larger than the limit.
//
// THE THRESHOLD IS THE FLEET'S, 2048 KB, and it is measured against the one
// file that ever crossed it. It was 500 KiB (pre-commit's own --maxkb=500)
// until 2026-09-11, when the atoms stopped reading a repo's hook args and infra
// went red on flux/infrastructure/cert-manager.yaml — 1010 KB, cert-manager's
// own vendored CRD bundle, which infra's config had always passed with
// --maxkb=2048. The rule exists to catch an accidental binary or dataset, not a
// text manifest a vendor ships at a megabyte.
//
// A LINE THAT IS NOT `<int> <path>` IS SKIPPED, not guessed at. stat writes its
// failures to stderr, and the caller's output() folds stderr into this string
// whenever the exec exited non-zero — so "stat: cannot statx 'x': No such file"
// arrives here and must not be read as a file of unknown size. The caller reads
// the exit code separately and refuses to call a partial measurement clean.
//
// Only the path is reported, as the awk it replaces did (`$1=""; sub(/^ /,"");
// print`) — and unlike that awk, a path containing runs of spaces survives
// intact, because the split is on the FIRST space rather than a field rebuild
// with OFS.
//
// THE SPLIT IS strings.Cut, NOT AN INDEX AND A BOUNDARY. It was
// `cut := strings.IndexByte(line, ' '); if cut <= 0`, and that comparison
// carried an EQUIVALENT MUTANT the suite could not kill: a line whose first
// byte is a space has cut == 0, and `line[:0]` is "", which ParseInt rejects —
// so `<= 0` and `< 0` skip the same lines by two different routes. Cut says
// "there was a space" once, with no boundary to get wrong (gremlins 45:10,
// LIVED on PR #31).
func FilesOver(statOut string, limitBytes int64) []string {
	var over []string
	for _, line := range strings.Split(statOut, "\n") {
		line = strings.TrimRight(line, "\r")
		sizeText, name, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		size, err := strconv.ParseInt(sizeText, 10, 64)
		if err != nil {
			continue
		}
		if size > limitBytes {
			over = append(over, name)
		}
	}
	return over
}

// GrepThroughXargsState maps one `xargs -0 -a <list> grep …` run to an atom
// state, because grep IS three-valued and xargs collapses two of the three.
//
// grep's own contract: 0 a line was selected, 1 no line was selected, >1 an
// error. xargs' own contract (`man xargs`, EXIT STATUS): 0 if every invocation
// succeeded, 123 if ANY invocation exited 1-125, 124 for a 255, 125 for a
// signal, 126 cannot-run, 127 not-found, 1 for an error of its own.
//
// THE COLLAPSE IS THE PROBLEM. xargs splits a large population into several
// grep invocations, so the ordinary clean run — one chunk matched nothing —
// already exits 123, and so does a grep that died on an unreadable path. The
// exit code alone therefore cannot separate "clean" from "broken", and reading
// 123 as CANNOT RUN would make every large repository unpushable while reading
// it as PASS would hide a scan that never finished.
//
// SO THE OUTPUT DECIDES, AND THE CODE ONLY CONFIRMS:
//
//   - Non-empty stdout is a HIT, whatever the code. grep prints a selected line
//     only when it selected one; a chunk that errored elsewhere does not erase
//     the marker another chunk found.
//   - Empty stdout with 0, 1 or 123 is NO HIT. 123 is the mixed-chunk case
//     above; 0 is every chunk matching (impossible with no output, admitted for
//     completeness); 1 is the same all-chunks-non-zero answer under a BSD-style
//     xargs, which returns 1 rather than 123 — the lane image is Debian, but a
//     mapping that is wrong on another findutils is a mapping that will be
//     wrong one image roll from now.
//   - Anything else with empty stdout is a BROKEN SCAN — 124 (a 255), 125 (a
//     signal), 126/127 (the binary is not there), 137 (an OOM kill) — and a
//     scan that did not run is never a pass. That is this module's founding
//     argument, applied to the one atom whose tool answers in three codes.
//
// The states are the State constants' own values: 0 pass, 1 findings, 2 could
// not run.
func GrepThroughXargsState(code int, stdout string) int {
	if strings.TrimSpace(stdout) != "" {
		return int(StateFindings)
	}
	switch code {
	case 0, 1, 123:
		return int(StatePass)
	default:
		return int(StateCannotRun)
	}
}

// ToolThroughXargsState maps one `xargs -0 -a <list> <tool> …` run to an atom
// state for a TWO-VALUED tool — check-yaml and detect-secrets-hook, which exit
// 0 clean and 1 with something to say.
//
// WITHOUT THIS, EVERY FINDING READS AS A CANNOT-RUN. StateFor maps anything
// that is not 0 or 1 to could-not-run, and xargs never passes 1 through: a
// hook that exited 1 on a bad file makes xargs exit 123 (any invocation in
// 1-125). So 123 is the findings code here, and it is safe to say so because
// the tool was PROBED under the default Expect one exec earlier — `--help`
// already proved the binary resolves and runs, so 123 is the tool judging the
// files rather than uvx failing to find it.
//
// 124/125/126/127 and xargs' own 1 stay could-not-run: a 255, a signal, a
// missing binary and an xargs that could not read its list are all scans that
// did not happen.
func ToolThroughXargsState(code int) int {
	switch code {
	case 0:
		return int(StatePass)
	case 123:
		return int(StateFindings)
	default:
		return int(StateCannotRun)
	}
}

// secretsExclude is the population detect-secrets does NOT scan: test fixtures.
// A fixture's whole job is to look like the thing it is a fixture for, and a
// baseline that had to excuse every one of them would excuse the real ones too.
var secretsExclude = regexp.MustCompile(`(^|/)testdata/|^tests/fixtures/`)

// SecretsPopulation drops the fixture directories from the gate population.
//
// THE PATHS ARE PASSED AS THE BASELINE KEYS THEM. .secrets.baseline is written
// by pre-commit, which hands the scanner git-relative paths, so "bases/x.yaml"
// is the key. The walk this replaces handed detect-secrets "./bases/x.yaml",
// which matches nothing in the baseline, so EVERY already-excused finding came
// back as a new secret — 200+ of them on foundry-stocks, all of them already in
// its baseline (measured 2026-09-09). population() yields the bare relative
// path, and nothing here prefixes it.
func SecretsPopulation(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if secretsExclude.MatchString(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// languagesDecl is the `languages: [ … ]` array as it appears in an opengrep
// rule — the same expression the shell handed `grep -oE`.
var languagesDecl = regexp.MustCompile(`languages:[[:space:]]*\[[^]]*\]`)

// notLower strips everything that is not a lowercase letter, which is how the
// shell turned ` "go" ` into `go` without a YAML parser: quotes, spaces and
// commas all fall out together.
var notLower = regexp.MustCompile(`[^a-z]`)

// sastLaneManifests is the ordered manifest→language mapping the check walks.
// ORDERED, not a map, because the finding lists the missing lanes and a set
// iterated at random would make the message differ run to run for one tree.
var sastLaneManifests = []struct{ Manifest, Lang string }{
	{"go.mod", "go"},
	{"Cargo.toml", "rust"},
	{"pyproject.toml", "python"},
}

// SastLanesMissing answers what rules/sast DECLARES and which lanes this
// repository builds that the ruleset never names.
//
// THIS IS THE CASE THE ZERO-FILE REFUSAL STRUCTURALLY CANNOT SEE. A ruleset
// that matches SOMETHING and misses the rest still exits 0 with findings from
// the languages it did cover: foundry-stocks#4949 — chaos scanned 28 of 2636
// files, themis 26 of 2221, both green. opengrep cannot report a language it
// was never asked about, so the only place to catch it is here, against the
// manifests that say what the repository actually builds.
//
// The declared set is read the way the shell read it and for the same reason:
// a full YAML parse would need a dependency this module does not carry, and the
// question is coarse — does the word appear inside a `languages:` array. Lines
// whose first non-space character is `#` are dropped first, so a commented-out
// rule does not count as coverage. The result is deduplicated and sorted, as
// `sort -u` left it.
//
// TYPESCRIPT AND JAVASCRIPT ARE ONE LANE HERE. package.json declares the node
// lane and either name covers it, which is why that pair is not in the table
// above.
func SastLanesMissing(rulesBodies []string, rootEntries []string) (declared []string, missing []string) {
	seen := map[string]bool{}
	for _, body := range rulesBodies {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
				continue
			}
			for _, m := range languagesDecl.FindAllString(line, -1) {
				// The array's contents, by the brackets the regexp already
				// matched rather than by index arithmetic — `[`+1 and len-1
				// were a pair of EQUIVALENT MUTANTS (gremlins 201:41, LIVED):
				// everything the off-by-one dragged in (the `:` or the space
				// before the bracket) is stripped by notLower anyway, so no
				// input could tell the mutant from the original.
				_, inner, _ := strings.Cut(m, "[")
				inner = strings.TrimSuffix(inner, "]")
				for _, lang := range strings.Split(inner, ",") {
					lang = notLower.ReplaceAllString(lang, "")
					if lang != "" {
						seen[lang] = true
					}
				}
			}
		}
	}
	declared = make([]string, 0, len(seen))
	for lang := range seen {
		declared = append(declared, lang)
	}
	sort.Strings(declared)

	for _, m := range sastLaneManifests {
		if HasEntry(rootEntries, m.Manifest) && !seen[m.Lang] {
			missing = append(missing, m.Lang+"("+m.Manifest+")")
		}
	}
	if HasEntry(rootEntries, "package.json") && !seen["typescript"] && !seen["javascript"] {
		missing = append(missing, "typescript-or-javascript(package.json)")
	}
	return declared, missing
}

// zeroFileScan is opengrep's own summary line for a scan that examined nothing.
var zeroFileScan = regexp.MustCompile(`Ran [0-9]+ rules on 0 files`)

// OpengrepZeroFiles reports whether a scan examined NO FILE AT ALL.
//
// THIS IS THE DEFECT THE MODULE WAS BUILT AROUND. "Ran N rules on 0 files: 0
// findings" exits 0 and renders as Passed, indistinguishable from a clean scan
// — and go-repo-template and rust-repo-template shipped the PYTHON ruleset, so
// every Go and Rust star in the fleet ran a security gate that had never
// examined a single file and reported success every time (foundry-stocks#4415).
// The atom reads this and answers CANNOT RUN.
func OpengrepZeroFiles(out string) bool {
	return zeroFileScan.MatchString(out)
}
