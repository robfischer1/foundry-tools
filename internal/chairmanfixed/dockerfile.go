package chairmanfixed

import (
	"strings"
)

// copyLine is one COPY instruction of the base's Dockerfile.
type copyLine struct {
	chown, chmod string
	from         string
	srcs         []string
	dst          string
}

// dockerfile is what the checks read of bases/chairman/Dockerfile: its COPYs and
// its last ENV PATH.
type dockerfile struct {
	copies []copyLine
	path   string
}

// parseDockerfile joins continuation lines and keeps COPY and ENV PATH.
func parseDockerfile(body string) dockerfile {
	var d dockerfile
	var logical []string
	cur := ""
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if cur == "" && (t == "" || strings.HasPrefix(t, "#")) {
			continue
		}
		if strings.HasSuffix(t, `\`) {
			cur += strings.TrimSuffix(t, `\`) + " "
			continue
		}
		logical = append(logical, cur+t)
		cur = ""
	}
	for _, l := range logical {
		f := strings.Fields(l) // a logical line is never blank
		switch strings.ToUpper(f[0]) {
		case "COPY":
			var c copyLine
			args := f[1:]
			for len(args) > 0 && strings.HasPrefix(args[0], "--") {
				k, v, _ := strings.Cut(args[0], "=")
				switch strings.ToLower(k) {
				case "--chown":
					c.chown = v
				case "--chmod":
					c.chmod = v
				case "--from":
					c.from = v
				}
				args = args[1:]
			}
			if len(args) >= 2 {
				c.srcs, c.dst = args[:len(args)-1], args[len(args)-1]
				d.copies = append(d.copies, c)
			}
		case "ENV":
			for _, kv := range f[1:] {
				if v, ok := strings.CutPrefix(kv, "PATH="); ok {
					d.path = v
				}
			}
		}
	}
	return d
}

// copying finds the COPY that puts the context file src at dst: the destination
// is dst itself, or a directory (trailing slash) the file lands in by name.
func (d dockerfile) copying(src, dst string) (copyLine, bool) {
	for _, c := range d.copies {
		if c.from != "" {
			continue
		}
		for _, s := range c.srcs {
			if s != src {
				continue
			}
			if c.dst == dst || (strings.HasSuffix(c.dst, "/") && c.dst+baseName(s) == dst) {
				return c, true
			}
		}
	}
	return copyLine{}, false
}

// copyingTree finds the COPY of the directory src (trailing slash) to dst.
func (d dockerfile) copyingTree(src, dst string) (copyLine, bool) {
	for _, c := range d.copies {
		if c.from == "" && len(c.srcs) == 1 && c.srcs[0] == src && c.dst == dst {
			return c, true
		}
	}
	return copyLine{}, false
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
