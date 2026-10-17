package pinsync

import (
	"fmt"
	"regexp"
	"strings"
)

// tomlDoc is tools.toml as lines, edited in place: only a value between the
// quotes of a `key = "value"` line ever changes, so the comments, the order and
// every table nobody asked about stay byte for byte.
type tomlDoc struct {
	lines []string
}

var (
	tomlHeader = regexp.MustCompile(`^\[([A-Za-z0-9_.-]+)\]\s*(#.*)?$`)
	tomlString = regexp.MustCompile(`^(\s*)([A-Za-z0-9_-]+)(\s*=\s*)"([^"\\]*)"(\s*(#.*)?)$`)
)

func parseTOML(body []byte) *tomlDoc {
	return &tomlDoc{lines: strings.Split(string(body), "\n")}
}

func (d *tomlDoc) bytes() []byte { return []byte(strings.Join(d.lines, "\n")) }

// find answers the line index of key in table, or -1.
func (d *tomlDoc) find(table, key string) int {
	cur := ""
	for i, line := range d.lines {
		if m := tomlHeader.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if cur != table {
			continue
		}
		if m := tomlString.FindStringSubmatch(line); m != nil && m[2] == key {
			return i
		}
	}
	return -1
}

// get answers a string value, and whether the key is there.
func (d *tomlDoc) get(table, key string) (string, bool) {
	i := d.find(table, key)
	if i < 0 {
		return "", false
	}
	return tomlString.FindStringSubmatch(d.lines[i])[4], true
}

// set replaces a string value that is already there. It never adds a key: a
// table this command does not find is a tools.toml it was not written for.
func (d *tomlDoc) set(table, key, value string) error {
	if strings.ContainsAny(value, "\"\\\n") {
		return fmt.Errorf("[%s] %s: %q needs escaping, which a pin never does", table, key, value)
	}
	i := d.find(table, key)
	if i < 0 {
		return fmt.Errorf("tools.toml has no [%s] %s", table, key)
	}
	m := tomlString.FindStringSubmatch(d.lines[i])
	d.lines[i] = m[1] + m[2] + m[3] + `"` + value + `"` + m[5]
	return nil
}
