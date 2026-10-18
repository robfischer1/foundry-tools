package atomkit

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ToolsSchema is the schema key of tools.toml.
const ToolsSchema = "tools@1"

var (
	digestPinned = regexp.MustCompile(`@sha256:[0-9a-f]{64}`)
	hex64        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ToolPin is one [tool.<name>] of tools@1: how a tool the atoms need arrives.
type ToolPin struct {
	Name    string `json:"name,omitempty" toml:"-"` // the table's key
	Kind    string `json:"kind" toml:"kind"`        // go | uv | url | image
	Spec    string `json:"spec,omitempty" toml:"spec"`
	Version string `json:"version,omitempty" toml:"version"`
	SHA256  string `json:"sha256,omitempty" toml:"sha256"`
	Image   string `json:"image,omitempty" toml:"image"`
	// Bins are the programs of an image tool that go on an atom's PATH, as
	// paths inside its image. Empty puts the image's bin directories on it.
	Bins []string `json:"bins,omitempty" toml:"bins"`
}

// ToolsFile is tools@1 after the check: images by digest, tools by kind.
type ToolsFile struct {
	Schema     string
	Base       struct{ Go string }
	Toolchains map[string]string
	Preflights map[string][]string
	Services   map[string]string
	Tools      map[string]ToolPin
}

// rawTools is the TOML as written. A toolchain's preflight is decoded loosely
// so a wrong type can be reported in the file's own words.
type rawTools struct {
	Schema string `toml:"schema"`
	Base   struct {
		Go string `toml:"go"`
	} `toml:"base"`
	Toolchain map[string]struct {
		Image     string `toml:"image"`
		Preflight any    `toml:"preflight"`
	} `toml:"toolchain"`
	Service map[string]struct {
		Image string `toml:"image"`
	} `toml:"service"`
	Tool map[string]ToolPin `toml:"tool"`
}

// ParseTools decodes tools@1 strictly and checks it (lld.md FN-108).
func ParseTools(b []byte) (ToolsFile, error) {
	var raw rawTools
	if err := decodeStrict(b, &raw); err != nil {
		return ToolsFile{}, err
	}
	if err := checkSchema(raw.Schema, ToolsSchema); err != nil {
		return ToolsFile{}, err
	}
	f := ToolsFile{Schema: raw.Schema, Toolchains: map[string]string{}, Services: map[string]string{}, Tools: map[string]ToolPin{}}
	f.Base.Go = raw.Base.Go
	if f.Base.Go != "" {
		if err := pinned("base", "go", f.Base.Go); err != nil {
			return ToolsFile{}, err
		}
	}
	for _, name := range sortedNames(raw.Toolchain) {
		tc := raw.Toolchain[name]
		if err := pinned("toolchain", name, tc.Image); err != nil {
			return ToolsFile{}, err
		}
		f.Toolchains[name] = tc.Image
		if tc.Preflight == nil {
			continue
		}
		argv, ok := stringList(tc.Preflight)
		if !ok {
			return ToolsFile{}, fmt.Errorf("toolchain.%s: preflight must be a non-empty list of strings", name)
		}
		if f.Preflights == nil {
			f.Preflights = map[string][]string{}
		}
		f.Preflights[name] = argv
	}
	for _, name := range sortedNames(raw.Service) {
		if err := pinned("service", name, raw.Service[name].Image); err != nil {
			return ToolsFile{}, err
		}
		f.Services[name] = raw.Service[name].Image
	}
	for _, name := range sortedNames(raw.Tool) {
		pin := raw.Tool[name]
		pin.Name = name
		if err := checkTool(pin); err != nil {
			return ToolsFile{}, err
		}
		f.Tools[name] = pin
	}
	return f, nil
}

// pinned refuses an image that a tag alone names: a tag moves, a digest does not.
func pinned(table, name, image string) error {
	if image != "" && !digestPinned.MatchString(image) {
		return fmt.Errorf("%s.%s: image %s is not pinned by digest", table, name, image)
	}
	return nil
}

func checkTool(p ToolPin) error {
	if err := pinned("tool", p.Name, p.Image); err != nil {
		return err
	}
	if err := checkBins(p); err != nil {
		return err
	}
	switch p.Kind {
	case "url":
		if p.Spec == "" || !hex64.MatchString(p.SHA256) {
			return fmt.Errorf("tool.%s: url tool needs sha256", p.Name)
		}
	case "image":
		if p.Image == "" {
			return fmt.Errorf("tool.%s: image tool needs image", p.Name)
		}
	case "go", "uv":
		if p.Spec == "" || p.Version == "" {
			return fmt.Errorf("tool.%s: %s tool needs spec and version", p.Name, p.Kind)
		}
	default:
		return fmt.Errorf("tool.%s: kind %s is not go, uv, url or image", p.Name, p.Kind)
	}
	return nil
}

// checkBins refuses bins on a tool that is not an image, and a bin that is not
// a clean path to a file inside the image.
func checkBins(p ToolPin) error {
	if len(p.Bins) == 0 {
		return nil
	}
	if p.Kind != "image" {
		return fmt.Errorf("tool.%s: bins belong to an image tool", p.Name)
	}
	for _, b := range p.Bins {
		if b == "" || path.IsAbs(b) || path.Clean(b) != b || b == ".." || strings.HasPrefix(b, "../") || strings.HasSuffix(b, "/") {
			return fmt.Errorf("tool.%s: bins entry %q is not a clean path inside the image", p.Name, b)
		}
	}
	return nil
}

// stringList reads a TOML array of strings; empty or mistyped is not ok.
func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]string, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
