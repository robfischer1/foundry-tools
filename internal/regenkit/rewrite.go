package regenkit

import (
	"fmt"
	"regexp"
	"strings"
)

// Rec is a hash and a size to put into the block of one top-level key of a
// provenance.json: guest_wasm, embedded_wasm, bindings, component_wasm.
type Rec struct {
	Key, SHA256 string
	Bytes       int
}

var (
	shaField   = regexp.MustCompile(`("sha256"\s*:\s*")[0-9a-f]{64}(")`)
	bytesField = regexp.MustCompile(`("bytes"\s*:\s*)[0-9]+`)
)

// Rewrite puts each record's sha256 and size into its block of the file's text
// and touches nothing else, so the formatting (indent widths differ from file
// to file), the notes and the pins stay as the person wrote them, and a file
// whose inputs did not move comes back byte for byte. A block with no sha256 or
// no bytes to rewrite is an error: the record would be left stale.
func Rewrite(text string, recs []Rec) (string, error) {
	for _, r := range recs {
		start, end, ok := ObjectSpan(text, r.Key)
		if !ok {
			return "", fmt.Errorf("provenance.json has no %s record to rewrite", r.Key)
		}
		block, err := rewriteBlock(text[start:end], r)
		if err != nil {
			return "", err
		}
		text = text[:start] + block + text[end:]
	}
	return text, nil
}

// rewriteBlock puts a record's sha256 and size into one object's text.
func rewriteBlock(block string, r Rec) (string, error) {
	if !shaField.MatchString(block) {
		return "", fmt.Errorf("provenance.json %s has no sha256 to rewrite", r.Key)
	}
	if !bytesField.MatchString(block) {
		return "", fmt.Errorf("provenance.json %s has no bytes to rewrite", r.Key)
	}
	block = shaField.ReplaceAllString(block, "${1}"+r.SHA256+"${2}")
	return bytesField.ReplaceAllString(block, fmt.Sprintf("${1}%d", r.Bytes)), nil
}

// RewriteFiles puts each record's sha256 and size into its entry of the
// top-level `files` object, whose keys are the generated files' paths (the
// transpile's output: provenance.json records what each shipped file hashes
// to). It touches nothing else, as Rewrite does. A record whose path has no
// entry is answered in absent and written nowhere: adding an entry is the
// author's, and the caller says so. A file with no `files` object keeps no such
// record, and nothing is absent. An entry with no sha256 or no bytes is an
// error: the record would be left stale.
func RewriteFiles(text string, recs []Rec) (out string, absent []string, err error) {
	start, end, ok := ObjectSpan(text, "files")
	if !ok {
		return text, nil, nil
	}
	files := text[start:end]
	for _, r := range recs {
		s, e, found := ObjectSpan(files, r.Key)
		if !found {
			absent = append(absent, r.Key)
			continue
		}
		block, err := rewriteBlock(files[s:e], r)
		if err != nil {
			return "", nil, fmt.Errorf("provenance.json files entry %s: %w", r.Key, err)
		}
		files = files[:s] + block + files[e:]
	}
	return text[:start] + files + text[end:], absent, nil
}

// ObjectSpan finds the object that is the value of a top-level key: the span
// from its `{` to just past its `}`. It reads strings (a note may hold braces and
// escaped quotes) and nesting, and nothing else of JSON.
func ObjectSpan(text, key string) (start, end int, ok bool) {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case '"':
			close := stringEnd(text, i)
			if close < 0 {
				return 0, 0, false
			}
			if depth == 1 && text[i+1:close] == key {
				j := skipSpace(text, close+1)
				if j < len(text) && text[j] == ':' {
					j = skipSpace(text, j+1)
					if j < len(text) && text[j] == '{' {
						return j, matching(text, j), matching(text, j) > 0
					}
				}
			}
			i = close
		}
	}
	return 0, 0, false
}

// stringEnd is the index of the quote that closes the string opened at i, or -1.
func stringEnd(text string, i int) int {
	for j := i + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return -1
}

// skipSpace is the index of the first byte at or after i that is not whitespace.
func skipSpace(text string, i int) int {
	for i < len(text) && strings.IndexByte(" \t\r\n", text[i]) >= 0 {
		i++
	}
	return i
}

// matching is the index just past the `}` that closes the `{` at i, or -1.
func matching(text string, i int) int {
	depth := 0
	for j := i; j < len(text); j++ {
		switch text[j] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		case '"':
			close := stringEnd(text, j)
			if close < 0 {
				return -1
			}
			j = close
		}
	}
	return -1
}
