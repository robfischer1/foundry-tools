package checks

// MutatorRow is one class of mutant a mutation tool is told not to generate.
// Keyed by the OPERATOR, never by a config's path: the file has already moved
// once (.gremlins.yaml -> .gomutants.yaml) and a ratification keyed to a
// filename would have evaporated with the rename.
//
// Tool says which lane passes it, and Mutator is in that tool's own words: a
// gomutants operator, a cargo-mutants --exclude-re regex over the mutant's
// name, a cosmic-ray operator regex matched from the start, a Stryker mutator.
type MutatorRow struct {
	Tool, Mutator, Approved, Provenance, Reason string
}

// The tools a MutatorRow can name.
const (
	MutatorGo     = "go"
	MutatorRust   = "rust"
	MutatorPython = "python"
	MutatorTS     = "ts"
)

// SkippedMutators is the tool's rows, in table order: what its lane passes.
func SkippedMutators(tool string) []string {
	var names []string
	for _, r := range RatifiedMutators {
		if r.Tool == tool {
			names = append(names, r.Mutator)
		}
	}
	return names
}

// RatifiedMutators is the mutation gates' exclusion set, every language's, and
// the whole of it. A gomutants config that disables an operator no Go row here
// names is a finding.
//
// A DISABLED OPERATOR IS A SUPPRESSION, and it is the widest one in this file.
// A `noqa` silences one line; a ruff per-file-ignores entry silences one glob
// in one repository; this silences an operator in EVERY repository the fleet
// mutates, from a file most readers never open. The class is the same and the
// scale is not, so it earns the same rule: Rob's words, or it is a finding.
//
// THE DIRECTIVE TABLE ABOVE CANNOT SEE IT, which is why this exists as data
// rather than as another sjForm. Every form in sjPatterns is a SOURCE-level
// suppression — a comment a compiler or a linter reads, or a call like t.Skip.
// A YAML `disable:` key matches none of them (measured 2026-09-29 against the
// generated .gomutants.yaml: zero hits from the whole inventory), so before
// this table a fifth operator could be added to the fleet's mutation gate with
// nothing anywhere to notice.
//
// RATIFIED BY ROB, session 836886a4 (Hamilton20), 2026-09-29: "Find our
// canonical CI/lint configs, author a .gomutants.yaml with the 24/28, and wire
// that in to the stop-justifications workflow. Cite this turn as provenance for
// my ratifcation (You'll need it for stop-justifications)". The 24/28 is this
// set: gomutants ships 28 operators and these four leave, so the gate the fleet
// actually runs is 19 operators WIDER than gremlins' five defaults, not
// narrower. The point of naming them is that the width is stated.
//
// `only:` IS NOT RATIFIABLE AND HAS NO ROW. It is the inverse key — it disables
// every operator it does not name — so one line of it empties the population
// and takes the gate green over a repository that graded nothing (measured
// 2026-09-29: `only: [INVERT_BITWISE]` took 24 enabled types to 1 and 2 mutants
// to 0, exit 0). There is no set of four words that makes that a measurement,
// so every entry under it is a finding on sight.
//
// THE LOW-VALUE CLASSES LEFT EVERY LANE ON 2026-10-06. A ledger of every
// mutant the fleet's lanes had graded (erebus.ci_atom.findings, read
// 2026-10-05) put four classes' survivors at a small chance of being a real
// bug each — boundary flips, arithmetic swaps, loop control, error-wrap text —
// and every survivor still cost a reader's context, warning or not. RATIFIED
// BY ROB, session a0814b82 (Hopper39): "Rather than warning only, can each of
// our mutation suites be configured to skip the low-impact classes of
// mutants? Warnings still consume context, which makes them still carry a
// cost", then, choosing all four over three: "2, but in the canonical config
// files, not per-repo". So they are rows here, in each tool's own words, and
// no repository carries them. Where a tool cannot name a class without taking
// a valuable one with it, it keeps the class: Stryker's EqualityOperator flips
// `<` to `<=` AND `==` to `!=`, so TypeScript keeps its boundary mutants.
// cargo-mutants and cosmic-ray have no error-wrap mutant, and cargo-mutants no
// loop-control one, so those rows do not exist.
var RatifiedMutators = []MutatorRow{
	{
		Tool: MutatorGo, Mutator: "INTEGER_INCREMENT", Approved: "2026-09-29T00:00:00Z",
		Provenance: "session 836886a4 (Hamilton20), in-session",
		Reason:     "an integer literal +1: `0`->`1` on the DISCARDED value of `return 0, err`, which Go leaves unspecified, and `64`->`65` in `strconv.ParseFloat(q, 64)`, bit-identical for every input",
	},
	{
		Tool: MutatorGo, Mutator: "INTEGER_DECREMENT", Approved: "2026-09-29T00:00:00Z",
		Provenance: "session 836886a4 (Hamilton20), in-session",
		Reason:     "the same literal -1: `0`->`-1` on a discarded return, `64`->`63` in ParseFloat, which takes the 32-bit path only for bitSize 32",
	},
	{
		Tool: MutatorGo, Mutator: "FLOAT_INCREMENT", Approved: "2026-09-29T00:00:00Z",
		Provenance: "session 836886a4 (Hamilton20), in-session",
		Reason:     "a float literal +1: `1e6`->`1000001.0` as a nanocore divisor, identical under int64 truncation at every realistic reading",
	},
	{
		Tool: MutatorGo, Mutator: "FLOAT_DECREMENT", Approved: "2026-09-29T00:00:00Z",
		Provenance: "session 836886a4 (Hamilton20), in-session",
		Reason:     "the same literal -1: `1e6`->`999999.0`, identical under the same truncation",
	},
	{
		Tool: MutatorGo, Mutator: "ARITHMETIC_BASE", Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "`+`<->`-`, `*`<->`/`: 112 graded, 48 lived, ~1-3% a real bug — mostly timeouts and multipliers",
	},
	{
		Tool: MutatorGo, Mutator: "CONDITIONALS_BOUNDARY", Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "`<`<->`<=`, `>`<->`>=`: 175 graded, 142 lived, ~2-5% a real bug — mostly equivalent at exact-equality points",
	},
	{
		Tool: MutatorGo, Mutator: "ERRORF_WRAP", Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "an fmt.Errorf message's wrapping: 156 graded, 120 lived, <1% a real bug — message text only",
	},
	{
		Tool: MutatorGo, Mutator: "INVERT_LOOP_CTRL", Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "`break`<->`continue`: 92 graded, 78 lived, ~5-10% a real bug (daedalus, once) — usually map-order equivalent",
	},
	{
		Tool: MutatorRust, Mutator: `replace (< with <=|<= with <|> with >=|>= with >)( in |$)`, Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "the boundary flips, CONDITIONALS_BOUNDARY's twin; `<` to `>` or `==` stays",
	},
	{
		Tool: MutatorRust, Mutator: `replace [-+*/%] with [-+*/%]( in |$)`, Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "the arithmetic swaps, ARITHMETIC_BASE's twin; bitwise `|` and `&` stay",
	},
	{
		Tool: MutatorRust, Mutator: `replace [-+*/%]= with [-+*/%]=( in |$)`, Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "the same swaps on a compound assignment",
	},
	{
		Tool: MutatorPython, Mutator: `core/ReplaceComparisonOperator_(Lt_LtE|LtE_Lt|Gt_GtE|GtE_Gt)$`, Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "the boundary flips, CONDITIONALS_BOUNDARY's twin; Lt to Gt, Eq or Is stays",
	},
	{
		Tool: MutatorPython, Mutator: `core/ReplaceBinaryOperator_(Add|Sub|Mul|Div|FloorDiv|Mod|Pow)_(Add|Sub|Mul|Div|FloorDiv|Mod|Pow)$`, Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "the arithmetic swaps, ARITHMETIC_BASE's twin; a swap into or out of a bitwise or shift operator stays",
	},
	{
		Tool: MutatorPython, Mutator: `core/Replace(BreakWithContinue|ContinueWithBreak)$`, Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "loop control, INVERT_LOOP_CTRL's twin",
	},
	{
		Tool: MutatorTS, Mutator: "ArithmeticOperator", Approved: "2026-10-06T00:00:00Z",
		Provenance: "session a0814b82 (Hopper39), in-session",
		Reason:     "the arithmetic swaps, ARITHMETIC_BASE's twin",
	},
}
