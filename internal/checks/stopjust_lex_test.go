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
