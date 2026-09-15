package checks

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// THE READER, carried from foundry-stocks ci/lib/stop_justifications.py.
//
// A match is judged by the REGION its first character falls in. A code form
// (a skip call, an allow attribute, an escape in a CI step) counts only in
// CODE; a comment-directive counts only in COMMENT; nothing counts in a
// STRING. That one rule closes both holes the script's header records: a
// directive quoted in a docstring read as filed (false positive), and a `//`
// inside a string earlier on the line hid a real skip after it (fail-open).
//
// Every mask is ONE BYTE PER SOURCE BYTE, so a regexp match's byte offset is
// its index into the mask. The script indexed by code point; the delimiters
// that change region are all ASCII, and UTF-8 never puts an ASCII byte inside
// a multi-byte character, so the two agree at every offset a match can start.

// Region codes, one per source byte.
const (
	RegionCode    = 'c'
	RegionComment = '#'
	RegionString  = 's'
)

// sjLex is how one language opens a line comment, a block comment and a
// string. wordHash is the YAML/shell rule that `#` opens a comment only at the
// start of a line or after whitespace: `foo#bar` is one word, and reading it as
// a comment is how a `|| true` after a URL fragment got excused.
type sjLex struct {
	line     string
	block    [2]string
	quotes   string
	raw      string
	wordHash bool
}

var sjLexes = map[string]sjLex{
	"python":     {line: "#", quotes: `"'`},
	"go":         {line: "//", block: [2]string{"/*", "*/"}, quotes: `"'`, raw: "`"},
	"rust":       {line: "//", block: [2]string{"/*", "*/"}, quotes: `"'`},
	"typescript": {line: "//", block: [2]string{"/*", "*/"}, quotes: `"'`, raw: "`"},
	"ci":         {line: "#", quotes: `"'`, wordHash: true},
	"dockerfile": {line: "#", quotes: `"'`, wordHash: true},
}

// RegionMask answers one CODE/COMMENT/STRING mask per line. Python is read by
// a lexer that answers what python 3.14's tokenize answers, and falls back to
// the quote-tracking scanner where tokenize would raise — the script's own
// fallback, for the only honest reason: a file python cannot read.
func RegionMask(lang string, lines []string) []string {
	if lang == "python" {
		if masks, ok := pythonMask(lines); ok {
			return masks
		}
	}
	return genericMask(lang, lines)
}

// RegionAt reports the region col falls in; past the end of the mask is CODE.
func RegionAt(mask string, col int) byte {
	if col >= 0 && col < len(mask) {
		return mask[col]
	}
	return RegionCode
}

// genericMask classifies lines by explicit quote tracking, carrying block
// comment and raw-string state ACROSS lines — both are multi-line constructs a
// per-line scanner cannot see. Single and double quotes are line-local: an
// unterminated one is a syntax error in every language here, and letting it
// span lines would let one stray quote blind the scan to the rest of the file.
//
// It is the script's _mask_generic statement for statement, quirks included:
// only a double-quoted string honours a backslash escape, and a block opener
// paints one byte before the closer is looked for, so `/*/` closes at once.
func genericMask(lang string, lines []string) []string {
	lex := sjLexes[lang]
	masks := make([]string, 0, len(lines))
	inBlock := ""
	inRaw := byte(0)
	for _, line := range lines {
		n := len(line)
		mask := make([]byte, 0, n+1)
		i := 0
		for i < n {
			ch := line[i]
			if inBlock != "" {
				mask = append(mask, RegionComment)
				if strings.HasPrefix(line[i:], inBlock) {
					i++
					mask = append(mask, RegionComment)
					inBlock = ""
				}
				i++
				continue
			}
			if inRaw != 0 {
				mask = append(mask, RegionString)
				if ch == inRaw {
					inRaw = 0
				}
				i++
				continue
			}
			if lex.block[0] != "" && strings.HasPrefix(line[i:], lex.block[0]) {
				inBlock = lex.block[1]
				mask = append(mask, RegionComment)
				i++
				continue
			}
			if lineCommentAt(lex, line, i) {
				for ; i < n; i++ {
					mask = append(mask, RegionComment)
				}
				continue
			}
			if strings.IndexByte(lex.raw, ch) >= 0 {
				inRaw = ch
				mask = append(mask, RegionString)
				i++
				continue
			}
			if strings.IndexByte(lex.quotes, ch) >= 0 {
				mask, i = quoted(line, i, mask)
				continue
			}
			mask = append(mask, RegionCode)
			i++
		}
		masks = append(masks, string(mask[:n]))
	}
	return masks
}

// quoted paints a line-local string opening at i and answers the mask and the
// index after it. Only a double quote honours a backslash, as in the script.
func quoted(line string, i int, mask []byte) ([]byte, int) {
	ch := line[i]
	mask = append(mask, RegionString)
	i++
	for i < len(line) {
		mask = append(mask, RegionString)
		if line[i] == '\\' && ch == '"' {
			i += 2
			mask = append(mask, RegionString)
			continue
		}
		i++
		if line[i-1] == ch {
			break
		}
	}
	return mask, i
}

func lineCommentAt(lex sjLex, line string, i int) bool {
	if !strings.HasPrefix(line[i:], lex.line) {
		return false
	}
	if !lex.wordHash || i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(line[:i])
	return pySpace(r)
}

// pySpace is python's str.isspace, which is wider than unicode.IsSpace by the
// four information separators.
func pySpace(r rune) bool {
	return (r >= 0x1c && r <= 0x1f) || unicode.IsSpace(r)
}

// pyStrip is python's str.strip().
func pyStrip(s string) string {
	return strings.TrimFunc(s, pySpace)
}

// runeCap is python's s[:n] — n code points, not n bytes.
func runeCap(s string, n int) string {
	k := 0
	for i := range s {
		if k == n {
			return s[:i]
		}
		k++
	}
	return s
}

// SplitLines is python's str.splitlines(): every line boundary python knows,
// and no empty line after a trailing one.
func SplitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		if !lineBreak(r) {
			continue
		}
		out = append(out, s[start:i-w])
		if r == '\r' && i < len(s) && s[i] == '\n' {
			i++
		}
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func lineBreak(r rune) bool {
	return (r >= '\n' && r <= '\r') || (r >= 0x1c && r <= 0x1e) || r == 0x85 || r == 0x2028 || r == 0x2029
}

// ---- python -----------------------------------------------------------------

// pyLexer answers the regions python 3.14's tokenize paints: COMMENT tokens,
// STRING tokens and the literal parts of an f-string, with everything else
// CODE. bad is set exactly where tokenize raises on source the fleet could
// plausibly hold — measured against python 3.14.7: a null byte, an
// unterminated string, a triple-quoted string or a bracket or a continuation
// still open at the end, a backslash that does not end its line, a dedent to a
// column no block opened, tabs and spaces mixed, a newline in a single-quoted
// f-string's literal or spec, and a lone `}` in an f-string.
//
// A t-string (3.14) is lexed like an f-string and painted like CODE, because
// the script's string-token set predates TSTRING_START and names only STRING
// and the three FSTRING tokens.
type pyLexer struct {
	lines []string
	masks [][]byte
	ln    int
	col   int
	bad   bool
}

func pythonMask(lines []string) ([]string, bool) {
	p := &pyLexer{lines: lines, masks: make([][]byte, len(lines))}
	for i, l := range lines {
		p.masks[i] = []byte(strings.Repeat("c", len(l)))
	}
	p.module()
	if p.bad {
		return nil, false
	}
	out := make([]string, len(lines))
	for i, m := range p.masks {
		out[i] = string(m)
	}
	return out, true
}

func (p *pyLexer) eof() bool { return p.ln >= len(p.lines) }

// at answers the byte under the cursor; a line's end reads as '\n'.
func (p *pyLexer) at() byte {
	line := p.lines[p.ln]
	if p.col < len(line) {
		return line[p.col]
	}
	return '\n'
}

// ahead answers the byte k past the cursor on the same line, or 0.
func (p *pyLexer) ahead(k int) byte {
	line := p.lines[p.ln]
	if p.col+k < len(line) {
		return line[p.col+k]
	}
	return 0
}

// step moves one byte, crossing a line end onto the next line.
func (p *pyLexer) step() {
	if p.col < len(p.lines[p.ln]) {
		p.col++
		return
	}
	p.ln++
	p.col = 0
}

// paintStep paints the byte under the cursor and moves past it. A line end
// has no byte to paint.
func (p *pyLexer) paintStep(kind byte) {
	if p.col < len(p.masks[p.ln]) {
		p.masks[p.ln][p.col] = kind
	}
	p.step()
}

// module lexes top-level code: logical lines, their indentation, and every
// token on them.
func (p *pyLexer) module() {
	for _, l := range p.lines {
		if strings.IndexByte(l, 0) >= 0 {
			p.bad = true
			return
		}
	}
	ind := &pyIndents{cols: []int{0}, alts: []int{0}}
	depth := 0
	continued := false
	for !p.eof() && !p.bad {
		if p.col == 0 && depth == 0 && !continued && !ind.admit(p.lines[p.ln]) {
			p.bad = true
			return
		}
		continued = false
		ch := p.at()
		if ch == '\\' {
			if p.col != len(p.lines[p.ln])-1 {
				p.bad = true
				return
			}
			continued = true
			p.step()
			p.step()
			continue
		}
		depth = p.token(depth)
	}
	if depth > 0 || continued {
		p.bad = true
	}
}

// pyIndents is tokenize's stack of open blocks, kept twice: once with a tab
// advancing to the next multiple of 8 and once with a tab counting 1. The
// first catches a dedent to a column nothing opened (IndentationError); the
// two disagreeing catches tabs and spaces mixed (TabError).
type pyIndents struct {
	cols []int
	alts []int
}

// admit checks a logical line's leading whitespace and answers false where
// tokenize raises. A blank or comment-only line is not a logical line at all.
func (s *pyIndents) admit(line string) bool {
	col, alt, i := 0, 0, 0
	for ; i < len(line) && (line[i] == ' ' || line[i] == '\t'); i++ {
		alt++
		col++
		if line[i] == '\t' {
			col = (col + 7) / 8 * 8
		}
	}
	if i == len(line) || line[i] == '#' {
		return true
	}
	top := len(s.cols) - 1
	if col > s.cols[top] {
		s.cols = append(s.cols, col)
		s.alts = append(s.alts, alt)
		return alt > s.alts[top]
	}
	for top > 0 && col < s.cols[top] {
		top--
	}
	s.cols = s.cols[:top+1]
	s.alts = s.alts[:top+1]
	return col == s.cols[top] && alt == s.alts[top]
}

// token lexes one token of code at the cursor and answers the bracket depth
// after it. A line end is a token of its own. In a replacement field the
// caller stops at `}`, `!` and `:` before calling here.
func (p *pyLexer) token(depth int) int {
	ch := p.at()
	if ch == '#' {
		for p.at() != '\n' {
			p.paintStep(RegionComment)
		}
		return depth
	}
	if ch == '"' || ch == '\'' {
		p.str(p.col, "")
		return depth
	}
	// A number ends at its first letter: tokenize reads 1f"a" as a NUMBER and
	// an f-string.
	if ch >= '0' && ch <= '9' {
		for p.at() == '_' || (p.at() >= '0' && p.at() <= '9') {
			p.step()
		}
		return depth
	}
	if wordByte(ch) {
		start := p.col
		for p.col < len(p.lines[p.ln]) && wordByte(p.at()) {
			p.col++
		}
		word := p.lines[p.ln][start:p.col]
		if (p.at() == '"' || p.at() == '\'') && pyPrefixes[strings.ToLower(word)] {
			p.str(start, strings.ToLower(word))
		}
		return depth
	}
	p.step()
	if strings.IndexByte("([{", ch) >= 0 {
		return depth + 1
	}
	if strings.IndexByte(")]}", ch) >= 0 && depth > 0 {
		return depth - 1
	}
	return depth
}

// wordByte is an identifier byte: ASCII letters, digits, underscore, and any
// byte of a non-ASCII character.
func wordByte(c byte) bool {
	return c == '_' || c >= 0x80 || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// pyPrefixes is every prefix python 3.14 accepts before a quote, lowercased.
var pyPrefixes = map[string]bool{
	"r": true, "u": true, "b": true, "br": true, "rb": true,
	"f": true, "fr": true, "rf": true, "t": true, "tr": true, "rt": true,
}

// str lexes a string literal whose prefix starts at column from on the
// cursor's line; the cursor sits on the opening quote.
func (p *pyLexer) str(from int, prefix string) {
	q := p.at()
	triple := p.ahead(1) == q && p.ahead(2) == q
	paint := byte(RegionString)
	if strings.Contains(prefix, "t") {
		paint = RegionCode
	}
	p.col = from
	for p.at() != q {
		p.paintStep(paint)
	}
	p.quote(q, triple, paint)
	if strings.ContainsAny(prefix, "ft") {
		p.fstring(q, triple, paint, false)
		return
	}
	p.plain(q, triple)
}

// closes reports whether the cursor sits on the literal's closing quote(s).
func (p *pyLexer) closes(q byte, triple bool) bool {
	return p.at() == q && (!triple || (p.ahead(1) == q && p.ahead(2) == q))
}

// quote paints the one or three quote bytes under the cursor.
func (p *pyLexer) quote(q byte, triple bool, paint byte) {
	p.paintStep(paint)
	if triple {
		p.paintStep(paint)
		p.paintStep(paint)
	}
}

// plain lexes the body of a non-formatted literal. A backslash protects the
// byte after it — a quote, or the line end — in raw and cooked strings alike.
func (p *pyLexer) plain(q byte, triple bool) {
	for !p.eof() {
		ch := p.at()
		if ch == '\n' && !triple {
			p.bad = true
			return
		}
		if p.closes(q, triple) {
			p.quote(q, triple, RegionString)
			return
		}
		p.paintStep(RegionString)
		if ch == '\\' && !p.eof() {
			p.paintStep(RegionString)
		}
	}
	p.bad = true
}

// fstring lexes a formatted literal's body — or, with spec true, a format spec
// inside one, which ends at the replacement field's `}`.
func (p *pyLexer) fstring(q byte, triple bool, paint byte, spec bool) {
	for !p.eof() && !p.bad {
		ch := p.at()
		if ch == '\n' && !triple {
			p.bad = true
			return
		}
		if spec && ch == '}' {
			return
		}
		if !spec && p.closes(q, triple) {
			p.quote(q, triple, paint)
			return
		}
		if ch == '{' && (spec || p.ahead(1) != '{') {
			p.step()
			p.field(q, triple, paint)
			continue
		}
		if ch == '}' && p.ahead(1) != '}' {
			p.bad = true
			return
		}
		p.paintStep(paint)
		if ch == '\\' && p.at() != '\n' && p.at() != '{' && p.at() != '}' {
			p.paintStep(paint)
		}
		// A doubled brace is one literal brace, and tokenize's FSTRING_MIDDLE
		// positions skip the second (measured on 10 of the fleet's 1,078 python
		// files): the first paints as string, the second stays code.
		if ch == '{' || ch == '}' {
			p.step()
		}
	}
	p.bad = true
}

// field lexes a replacement field after its `{`: an expression, an optional
// `!conversion`, an optional `:spec`, and the closing `}`. The expression may
// cross lines and carry a comment even in a single-quoted literal (PEP 701);
// the spec may not.
func (p *pyLexer) field(q byte, triple bool, paint byte) {
	depth := 0
	for !p.eof() && !p.bad {
		ch := p.at()
		if depth == 0 && ch == '}' {
			p.step()
			return
		}
		if depth == 0 && ch == ':' {
			p.step()
			p.fstring(q, triple, paint, true)
			continue
		}
		depth = p.token(depth)
	}
	p.bad = true
}
