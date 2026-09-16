package checks

import (
	"strings"
	"testing"
)

// maskOf is RegionMask over text split the way the scan splits a file.
func maskOf(lang, text string) []string {
	return RegionMask(lang, SplitLines(text))
}

type maskCase struct {
	label, lang, text string
	want              []string
}

func wantMasks(t *testing.T, label, lang, text string, want ...string) {
	t.Helper()
	got := maskOf(lang, text)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", label, got, want)
	}
}

var genericMaskCases = []maskCase{
	{"a go line comment", "go", `x := 1 // y`, []string{"ccccccc####"}},
	{"a string hides a line comment", "go", `s := "a//b" // c`, []string{"cccccssssssc####"}},
	{"a block comment spans lines", "go", "a /* b\nc */ d", []string{"cc####", "####cc"}},
	{"a raw string spans lines", "go", "x := `a\n//b` // c", []string{"cccccss", "ssssc####"}},
	{"a slash-star-slash closes at once", "rust", "/*/ x", []string{"###cc"}},
	{"only a double quote honours a backslash", "typescript", `"a\"b" 'c\'d'`, []string{"sssssscsssscs"}},
	{"a backslash ending a double-quoted line", "go", `x = "a\`, []string{"ccccsss"}},
	{"an unterminated quote stays on its line", "rust", "x = 'a\ny", []string{"ccccss", "c"}},
	{"a typescript template literal is raw", "typescript", "`a\n${b}` // c", []string{"ss", "sssssc####"}},
	{"yaml: a hash after a word is not a comment", "ci", "run: curl http://x#frag # c", []string{"cccccccccccccccccccccccc###"}},
	{"yaml: a hash at column zero is", "ci", "# c", []string{"###"}},
	{"dockerfile: a hash after a tab is", "dockerfile", "RUN x\t# c", []string{"cccccc###"}},
	{"a hash after a non-breaking space is a comment", "ci", "x\u00a0# c", []string{"ccc###"}},
	{"a hash after an information separator is a comment", "ci", "x\x1f# c", []string{"cc###"}},
	{"python's scanner reads a hash anywhere", "python", "x#y", []string{"c##"}},
	{"a quote inside a comment opens nothing", "ci", "# it's\nx 'y'", []string{"######", "ccsss"}},
}

func TestGenericMaskTracksCommentsStringsAndBlocksAcrossLines(t *testing.T) {
	for _, c := range genericMaskCases {
		wantMasks(t, c.label, c.lang, c.text, c.want...)
	}
}

func TestRegionAtIsCodePastEitherEnd(t *testing.T) {
	if RegionAt("s#", 0) != RegionString || RegionAt("s#", 1) != RegionComment {
		t.Error("an index inside the mask answers its region")
	}
	if RegionAt("s#", 2) != RegionCode || RegionAt("s#", -1) != RegionCode {
		t.Error("an index outside the mask is CODE")
	}
}

// Every expectation below was measured against python 3.14.7's tokenize.
var pythonMaskCases = []maskCase{
	{"a comment", "python", "x = 1  # c", []string{"ccccccc###"}},
	{"a string then a comment", "python", `x = "#" # c`, []string{"ccccsssc###"}},
	{"a prefixed string", "python", `x = rb"\" #"  # c`, []string{"ccccsssssssscc###"}},
	{"every prefix spelling", "python", `a=R"x" b=Br'x' c=u"x" d=rF"x"`, []string{"ccsssscccssssscccsssscccsssss"}},
	{"a word that is no prefix", "python", `xr"a"`, []string{"ccsss"}},
	{"a digit run is no prefix", "python", `1f"a"`, []string{"cssss"}},
	{"a triple string spans lines", "python", "x = \"\"\"a\n# b\nc\"\"\" # d", []string{"ccccssss", "sss", "ssssc###"}},
	{"a single-quoted string continues over a backslash", "python", "x = 'a\\\nb'  # c", []string{"ccccsss", "sscc###"}},
	{"an f-string's literal is string and its field is code", "python", `f"a {b} # c"  # d`, []string{"sssscccssssscc###"}},
	{"a format spec is string", "python", `f"{a:>{w}}"`, []string{"sscccsccccs"}},
	{"doubled braces are literal, and tokenize skips the second", "python", `f"{{#}}"`, []string{"ssscsscs"}},
	{"a conversion and a not-equals", "python", `f"{a!r}{a!=b}"`, []string{"sscccccccccccs"}},
	{"a dict in a field is not a spec", "python", `f"{ {1:2}[1] }"`, []string{"ssccccccccccccs"}},
	{"a nested string in a field", "python", `f"{x["#"]}"`, []string{"sscccsssccs"}},
	{"a comment in a single-quoted field that crosses lines", "python", "x = f\"{a # c\n}\"", []string{"ccccssccc###", "cs"}},
	{"a triple f-string with a comment in its field", "python", "f\"\"\"{a # c\n}\"\"\"", []string{"ssssccc###", "csss"}},
	{"a backslash before a brace starts a field", "python", `rf"\{a}"`, []string{"sssscccs"}},
	{"a backslash escape in an f-string", "python", `f"\"#"`, []string{"ssssss"}},
	{"a t-string is code to the script", "python", `x = t"a {b} # c"  # d`, []string{"cccccccccccccccccc###"}},
	{"a string nested in a t-string field is string", "python", `t"{"#"}"`, []string{"cccssscc"}},
	{"an indented single line", "python", "    return 1  # c", []string{"cccccccccccccc###"}},
	{"an unmatched closer", "python", ")  # c", []string{"ccc###"}},
	{"a stray symbol", "python", "x = 1 $ 2  # c", []string{"ccccccccccc###"}},
	{"a non-ascii identifier", "python", "é = 'x'", []string{"cccccsss"}},
	{"an empty file", "python", "", []string{}},
}

func TestPythonMaskPaintsWhatTokenizePaints(t *testing.T) {
	for _, c := range pythonMaskCases {
		wantMasks(t, c.label, c.lang, c.text, c.want...)
	}
}

func TestPythonMaskAcceptsTheIndentationTokenizeAccepts(t *testing.T) {
	for _, src := range []string{
		"if x:\n    y\nz",
		"if x:\n    if y:\n        z\n    w\nv",
		"if x:\n    y\n  # a comment at any column\n    z",
		"if x:\n    y\n\n   \n    z",
		"x = (1,\n  2)\ny",
		"x = 1 + \\\n  2\ny",
		"if x:\n\ty\n\tz",
		"if x:\n\ty\n\t\tz\n\tw",
		"s = '''\n  a\n'''\ny",
		"if x:\n    y\n",
	} {
		if _, ok := pythonMask(SplitLines(src)); !ok {
			t.Errorf("tokenize reads %q, the lexer refused it", src)
		}
	}
}

// Where tokenize raises, the scan falls back to the quote-tracking scanner.
func TestPythonMaskRefusesWhereTokenizeRaises(t *testing.T) {
	for label, src := range map[string]string{
		"an open bracket at the end":             "foo(a,  # c",
		"an open triple string":                  `x = """abc`,
		"an unterminated string":                 `x = "abc`,
		"an unterminated string over a line end": "x = 'a\nb'",
		"a continuation at the end":              "x = 1 \\",
		"a backslash that does not end its line": "x = 1 \\ y",
		"a null byte":                            "x = 1\x002",
		"a dedent to no open block":              "if x:\n    y\n  z",
		"a dedent after a comment line":          "def f():\n  pass\n\n# c\n x",
		"a tab where the block used spaces":      "if x:\n        y\n\tz",
		"spaces where the block used a tab":      "if x:\n\ty\n        z",
		"an indent that only tabs measure":       "if x:\n       y\n\t z",
		"a newline in a single-quoted spec":      "x = f\"{a:\n}\"",
		"a newline in a single-quoted f-string":  "x = f\"a\nb\"",
		"a lone closing brace":                   `x = f"a}"`,
		"an f-string open at the end":            `x = f"{a`,
		"a spec open at the end":                 `x = f"{a:x`,
		"a triple f-string open at the end":      `x = f"""a`,
	} {
		if _, ok := pythonMask(SplitLines(src)); ok {
			t.Errorf("%s: tokenize raises on %q, the lexer read it", label, src)
		}
	}
	wantMasks(t, "the fallback scanner answers instead", "python", "foo('#',  # c", "ccccsssccc###")
}

func TestSplitLinesIsPythonsSplitlines(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\n\nb", []string{"a", "", "b"}},
		{"a\r\nb\rc", []string{"a", "b", "c"}},
		{"a\r\r\nb", []string{"a", "", "b"}},
		{"a\vb\fc\x1cd\x1de\x1ef", []string{"a", "b", "c", "d", "e", "f"}},
		{"a\u0085b\u2028c\u2029d", []string{"a", "b", "c", "d"}},
		{"a\x1fb", []string{"a\x1fb"}},
		{"\n", []string{""}},
	} {
		if got := SplitLines(tc.in); strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("SplitLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPyStripAndRuneCapCountCodePoints(t *testing.T) {
	if got := pyStrip("\x1c\u00a0 x y\t\x1f"); got != "x y" {
		t.Errorf("pyStrip = %q", got)
	}
	if got := runeCap("éab", 2); got != "éa" {
		t.Errorf("runeCap by code point = %q", got)
	}
	if got := runeCap("ab", 5); got != "ab" {
		t.Errorf("runeCap past the end = %q", got)
	}
	if got := runeCap("ab", 0); got != "" {
		t.Errorf("runeCap zero = %q", got)
	}
}

// A line classified alone gets the same reader a file does.
func TestScanLineClassifiesALineAloneWhenGivenNoMask(t *testing.T) {
	if hits := ScanLine("python", "x = \"# "+sjNoqa+"\"", ""); len(hits) != 0 {
		t.Errorf("a directive in a string, classified alone, is data: %v", hits)
	}
	if hits := ScanLine("python", "x = 1  # "+sjNoqa, ""); len(hits) != 1 || hits[0].Form != "noqa" {
		t.Errorf("a directive in a comment, classified alone: %v", hits)
	}
	// The FIRST match of a form decides: a quoted directive before a real one
	// hides it, as the script's single search did.
	if hits := ScanLine("python", "x = \"# "+sjNoqa+"\"  # "+sjNoqa, ""); len(hits) != 0 {
		t.Errorf("the first match is in a string: %v", hits)
	}
	if hits := ScanLine("python", "x = 1", "c"); len(hits) != 0 {
		t.Errorf("no directive, no hit: %v", hits)
	}
}

// ---- the lexer boundaries the whole-file masks agreed on either way ---------
//
// Each case below names an expression the mutation lane flipped without any
// test moving. A mask asserted over a whole file is a weak claim about the
// byte that decides; these assert on the byte.

func TestWordByteIsAnIdentifierByte(t *testing.T) {
	for c := 0; c < 256; c++ {
		b := byte(c)
		want := b == '_' || b >= 0x80 ||
			(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
		if got := wordByte(b); got != want {
			t.Errorf("wordByte(%#x) = %v, want %v", b, got, want)
		}
	}
	// The edges each range is decided by, named so a boundary slip reads as
	// the character it broke rather than as a hex code.
	for _, edge := range []struct {
		b    byte
		want bool
	}{
		{'a', true}, {'z', true}, {'`', false}, {'{', false},
		{'A', true}, {'Z', true}, {'@', false}, {'[', false},
		{'0', true}, {'9', true}, {'/', false}, {':', false},
		{'_', true}, {'^', false}, {0x7f, false}, {0x80, true}, {0xff, true},
	} {
		if got := wordByte(edge.b); got != edge.want {
			t.Errorf("wordByte(%q) = %v, want %v", edge.b, got, edge.want)
		}
	}
}

func TestAheadStopsAtTheEndOfTheLine(t *testing.T) {
	p := &pyLexer{lines: []string{"abc"}}
	for _, tc := range []struct {
		col, k int
		want   byte
	}{
		{0, 0, 'a'}, {0, 1, 'b'}, {0, 2, 'c'},
		{0, 3, 0},   // exactly one past the end reads as nothing
		{0, 9, 0},   // and so does far past it
		{2, 0, 'c'}, // the last byte is still readable from the last column
		{2, 1, 0},
	} {
		p.col = tc.col
		if got := p.ahead(tc.k); got != tc.want {
			t.Errorf("col %d ahead(%d) = %q, want %q", tc.col, tc.k, got, tc.want)
		}
	}
}

func TestPythonMaskRefusesANullByteAtAnyColumn(t *testing.T) {
	for _, line := range []string{"\x00", "\x00x = 1", "x = 1\x00", "x = \x00 1"} {
		if _, ok := pythonMask([]string{line}); ok {
			t.Errorf("tokenize raises on a null byte; %q was accepted", line)
		}
	}
	if _, ok := pythonMask([]string{"x = 1", "\x00"}); ok {
		t.Error("a null byte on any line refuses the file")
	}
	if _, ok := pythonMask([]string{"x = 1"}); !ok {
		t.Error("a file with no null byte is accepted")
	}
}

// admit is python's indentation rule, tabs and all: col is tab-expanded to the
// next multiple of 8, alt counts the raw characters, and a level that agrees on
// one but not the other is the ambiguity TabError names.
func TestPyIndentsAdmitIsPythonsIndentationRule(t *testing.T) {
	for _, tc := range []struct {
		label string
		lines []string
		want  bool
	}{
		{"a plain indent and dedent", []string{"if x:", "    y", "z"}, true},
		{"nested, then out two levels at once", []string{"a", "  b", "    c", "a"}, true},
		{"a dedent to a column no level holds", []string{"a", "    b", "  c"}, false},
		{"a blank line is not indentation", []string{"a", "", "a"}, true},
		{"a comment sits at any column", []string{"a", "        # comment", "a"}, true},

		// One tab expands to column 8. Eight spaces also reach column 8, so
		// the columns agree while the raw counts (1 vs 8) do not.
		{"a tab and eight spaces are the same column", []string{"if x:", "\ty", "        z"}, false},
		{"a tab alone is consistent with itself", []string{"if x:", "\ty", "\tz"}, true},
		{"four spaces then a tab is a wider column", []string{"if x:", "    y", "\tz"}, false},
		{"a tab from column 4 still lands on 8", []string{"if x:", "    y", "    \tz", "    w"}, true},

		// A tab reaches column 8 EXACTLY, so ten spaces is deeper than one tab
		// and four spaces is shallower. Both bracket the tab stop: a formula
		// that lands anywhere else reorders one of them.
		{"ten spaces is deeper than a tab", []string{"if x:", "\ta", "          b"}, true},
		{"four spaces is shallower than a tab", []string{"if x:", "\ta", "    b"}, false},

		// Deeper by column while the raw count only ties is the TabError an
		// indent has to refuse: two spaces reach column 2 in two characters,
		// and a space then a tab reaches column 8 in the same two.
		{"a wider column on a tied raw count", []string{"if x:", "  a", " \tb"}, false},

		{"a dedent onto a level that exists", []string{"a", "  b", "  c"}, true},
	} {
		s := &pyIndents{cols: []int{0}, alts: []int{0}}
		got := true
		for _, l := range tc.lines {
			if !s.admit(l) {
				got = false
				break
			}
		}
		if got != tc.want {
			t.Errorf("%s: admitted=%v, want %v", tc.label, got, tc.want)
		}
	}
}

func TestSplitLinesHandlesACarriageReturnAtEveryPosition(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"a\r\nb", []string{"a", "b"}}, // the pair is ONE break, not two
		{"a\r", []string{"a"}},         // a CR at the very end reads nothing past it
		{"a\rb", []string{"a", "b"}},   // a lone CR still breaks
		{"a\r\r\nb", []string{"a", "", "b"}},
		{"a\n\rb", []string{"a", "", "b"}},
		{"\r\n", []string{""}},
		{"\r", []string{""}},
	} {
		got := SplitLines(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("SplitLines(%q) = %q, want %q", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("SplitLines(%q) = %q, want %q", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestLineBreakIsPythonsLineBoundarySet(t *testing.T) {
	breaks := map[rune]bool{
		'\n': true, '\v': true, '\f': true, '\r': true,
		0x1c: true, 0x1d: true, 0x1e: true, 0x85: true, 0x2028: true, 0x2029: true,
	}
	for _, r := range []rune{'\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029,
		'a', ' ', '\t', 0x1b, 0x1f, 0x84, 0x86, 0x2027, 0x202a, 0} {
		if got := lineBreak(r); got != breaks[r] {
			t.Errorf("lineBreak(%#x) = %v, want %v", r, got, breaks[r])
		}
	}
}

// Indentation is read at the START of a logical line and nowhere else: not
// inside brackets, and not on the far side of a backslash continuation. Each
// case is one tokenize accepts and a scan that read the indent anyway rejects.
func TestPythonMaskReadsIndentationOnlyAtALogicalLineStart(t *testing.T) {
	for _, tc := range []struct {
		label string
		src   string
	}{
		{"inside brackets any column is free", "x = [\n  1,\n 2,\n]"},
		{"a continuation carries its own column", "if x:\n    y = 1 + \\\n  2\n    z"},
		{"a nested bracket stays free", "x = f(\n        a,\n  b,\n)"},
	} {
		if _, ok := pythonMask(SplitLines(tc.src)); !ok {
			t.Errorf("%s: tokenize accepts this, the scan refused it", tc.label)
		}
	}
	// The control: the same shapes OUTSIDE a bracket or continuation are the
	// IndentationError tokenize raises, so the cases above are not passing
	// because the check never runs.
	for _, src := range []string{"x = 1\n  1,\n 2,\n", "if x:\n    y = 1\n  2\n"} {
		if _, ok := pythonMask(SplitLines(src)); ok {
			t.Errorf("a bad dedent outside brackets is refused: %q was accepted", src)
		}
	}
}

// A closer with nothing open does not take the depth negative — if it did, the
// indentation check above would silently stop running for the rest of the file.
func TestPythonMaskKeepsDepthAtZeroOnAnUnbalancedCloser(t *testing.T) {
	if _, ok := pythonMask(SplitLines(")\n  x\n y\n")); ok {
		t.Error("a stray closer must not switch off the indentation check")
	}
	if _, ok := pythonMask(SplitLines("x = (1)\n  y\n z\n")); ok {
		t.Error("a balanced pair leaves depth at zero")
	}
}

// A number ends at its first letter: tokenize reads 1f"a" as a NUMBER and an
// f-string, whose replacement fields are CODE, not string text.
func TestPythonMaskEndsANumberAtItsFirstLetter(t *testing.T) {
	for _, tc := range []struct {
		label, src, want string
	}{
		{"a digit then an f-string prefix", `x = 0f"{y}"`, `cccccsscccs`},
		{"underscores are part of the number", `x = 1_9f"{y}"`, `cccccccsscccs`},
		{"the top of the digit range", `x = 9f"{y}"`, `cccccsscccs`},
	} {
		got := RegionMask("python", []string{tc.src})
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: %q\n got %q\nwant %q", tc.label, tc.src, got, tc.want)
		}
	}
}
