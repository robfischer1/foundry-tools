package orbitcompose

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Usage is the command line.
const Usage = "usage: orbitcompose -contracts <foundry-dies>/orbits -out <flux>/prime/orbits [-namespace prime] [-check]"

// Change is what bringing the directory to the composed set takes.
type Change struct {
	Write  map[string][]byte
	Remove []string
}

// Empty reports a directory already at the composed set.
func (c Change) Empty() bool { return len(c.Write) == 0 && len(c.Remove) == 0 }

// Plan compares the composed set want with the directory's files have. A file
// the composer does not own (Owned) stops the plan: the directory is the
// composer's whole, and a person's file in it is a fault to report, never a
// file to overwrite or delete.
func Plan(want, have map[string][]byte) (Change, error) {
	c := Change{Write: map[string][]byte{}}
	for name, raw := range have {
		if !Owned(name, raw) {
			return Change{}, fmt.Errorf("%s is not the composer's (no %q first line, or not a sidecar); the directory must hold only what orbitcompose writes", name, OwnerMark)
		}
		if _, ok := want[name]; !ok {
			c.Remove = append(c.Remove, name)
		}
	}
	for name, raw := range want {
		if !bytes.Equal(have[name], raw) {
			c.Write[name] = raw
		}
	}
	slices.Sort(c.Remove)
	return c, nil
}

// ReadContracts parses every <producer>-<consumer>.toml in dir, in file name
// order. Anything that is not a .toml file (the README) is not a contract.
func ReadContracts(dir string) ([]Contract, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Contract
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		c, err := ParseContract(e.Name(), raw)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no contract (*.toml)", dir)
	}
	return out, nil
}

// ReadDir answers the regular files in dir by name; an absent dir is empty.
func ReadDir(dir string) (map[string][]byte, error) {
	have := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return have, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			return nil, fmt.Errorf("%s/%s is a directory; the composer's directory holds only files", dir, e.Name())
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		have[e.Name()] = raw
	}
	return have, nil
}

// Apply writes and removes what c says, in dir.
func Apply(dir string, c Change) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range c.Remove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	for name, raw := range c.Write {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// apply is Apply, and a variable so a test can reach Main's write failure:
// every way to make a real write fail after a successful read also fails the
// read first.
var apply = Apply

// Main runs the composer: exit 0 when the directory is (now) the composed
// set, 1 when -check finds it is not, 2 when it could not compose at all.
func Main(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("orbitcompose", flag.ContinueOnError)
	fs.SetOutput(errOut)
	contracts := fs.String("contracts", "", "the contracts directory (foundry-dies/orbits)")
	dir := fs.String("out", "", "the directory the composer owns (flux prime/orbits)")
	namespace := fs.String("namespace", "prime", "the ConfigMaps' namespace")
	check := fs.Bool("check", false, "write nothing; exit 1 if the directory is not the composed set")
	if err := fs.Parse(args); err != nil || *contracts == "" || *dir == "" || fs.NArg() != 0 {
		fmt.Fprintln(errOut, Usage)
		return 2
	}
	c, err := plan(*contracts, *dir, *namespace)
	if err != nil {
		fmt.Fprintln(errOut, "orbitcompose:", err)
		return 2
	}
	report(out, c)
	if *check {
		if c.Empty() {
			return 0
		}
		return 1
	}
	if err := apply(*dir, c); err != nil {
		fmt.Fprintln(errOut, "orbitcompose:", err)
		return 2
	}
	return 0
}

// plan composes contracts and compares the result with dir.
func plan(contracts, dir, namespace string) (Change, error) {
	cs, err := ReadContracts(contracts)
	if err != nil {
		return Change{}, err
	}
	sidecars := Compose(cs)
	have, err := ReadDir(dir)
	if err != nil {
		return Change{}, err
	}
	return Plan(Files(namespace, sidecars), have)
}

// report says what the change is, one file per line, sorted.
func report(out io.Writer, c Change) {
	if c.Empty() {
		fmt.Fprintln(out, "orbitcompose: up to date")
		return
	}
	var lines []string
	for name := range c.Write {
		lines = append(lines, "write  "+name)
	}
	for _, name := range c.Remove {
		lines = append(lines, "remove "+name)
	}
	slices.Sort(lines)
	fmt.Fprintln(out, strings.Join(lines, "\n"))
}
