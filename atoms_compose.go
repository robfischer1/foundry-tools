package main

import "strings"

// indent is the `sed 's/^/  /'` every one of these bodies ended with: a tool's
// output, set in from the line that introduced it.
func indent(s, prefix string) []string {
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
