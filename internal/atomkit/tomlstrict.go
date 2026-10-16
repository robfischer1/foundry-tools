package atomkit

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// decodeStrict is the E2 decode: TOML into v, an unknown key refused (the first
// one, in file order), and the schema key checked. schema is read from the
// decoded value by the caller, so this only reports what the decode saw.
func decodeStrict(src []byte, v any) error {
	md, err := toml.Decode(string(src), v)
	if err != nil {
		return fmt.Errorf("not TOML: %w", err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return fmt.Errorf("unknown key %s", un[0])
	}
	return nil
}

// checkSchema is E2's schema rule: the first key names the schema and version.
func checkSchema(got, want string) error {
	if got == want {
		return nil
	}
	if got == "" {
		got = "missing"
	}
	return fmt.Errorf("schema is %s, want %s", got, want)
}
