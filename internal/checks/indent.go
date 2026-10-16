package checks

import "strings"

// Indent is a tool's output set in from the line that introduced it: the lines
// of s, less its trailing newlines, each behind prefix. The compose atoms and the
// dies atoms word their findings with it.
func Indent(s, prefix string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return lines
}
