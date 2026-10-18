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
		block := text[start:end]
		if !shaField.MatchString(block) {
			return "", fmt.Errorf("provenance.json %s has no sha256 to rewrite", r.Key)
		}
		if !bytesField.MatchString(block) {
			return "", fmt.Errorf("provenance.json %s has no bytes to rewrite", r.Key)
		}
		block = shaField.ReplaceAllString(block, "${1}"+r.SHA256+"${2}")
		block = bytesField.ReplaceAllString(block, fmt.Sprintf("${1}%d", r.Bytes))
		text = text[:start] + block + text[end:]
	}
	return text, nil
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
