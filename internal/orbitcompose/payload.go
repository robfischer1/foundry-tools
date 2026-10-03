package orbitcompose

import (
	"errors"
	"slices"
	"strings"
)

// Payloads answers both orbit dies' files from the contracts directory's
// files, keyed by name: data/contracts is every contract file BYTE FOR BYTE
// (the README is not a contract and is not in it), and data/orbits is Die
// over the same contracts. A contract that does not parse stops both — a die
// is never published over a directory that does not compose.
func Payloads(files map[string][]byte) (contracts, orbits map[string][]byte, err error) {
	contracts = map[string][]byte{}
	for name, raw := range files {
		if strings.HasSuffix(name, ".toml") {
			contracts[name] = raw
		}
	}
	if len(contracts) == 0 {
		return nil, nil, errors.New("the directory holds no contract (*.toml)")
	}
	names := make([]string, 0, len(contracts))
	for name := range contracts {
		names = append(names, name)
	}
	slices.Sort(names)
	cs := make([]Contract, 0, len(names))
	for _, name := range names {
		c, err := ParseContract(name, contracts[name])
		if err != nil {
			return nil, nil, err
		}
		cs = append(cs, c)
	}
	return contracts, Die(cs), nil
}
