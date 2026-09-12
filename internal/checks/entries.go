package checks

import "strings"

// HasEntry reports whether a Directory.Entries listing carries this name.
//
// The engine names a DIRECTORY with a trailing slash and a file without one,
// so a caller asking for "rules" and a caller asking for "orbit.toml" both get
// a straight answer here rather than each remembering which shape it wanted.
// Three lane ports wrote this function on three branches; this is the one.
func HasEntry(entries []string, name string) bool {
	for _, e := range entries {
		if strings.TrimSuffix(e, "/") == name {
			return true
		}
	}
	return false
}
