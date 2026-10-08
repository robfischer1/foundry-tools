package atoms

import "testing"

func TestComposeNoTrackedSecrets(t *testing.T) {
	const id = "compose:no-tracked-secrets"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no compose spec is absent", map[string]string{".env": "S=1"}, 0, absent, []string{"tracks no compose.yaml/compose.yml"}},
		{"a spec and no credential passes", map[string]string{"compose.yaml": "x"}, 0, pass, []string{"no credential-shaped file is tracked"}},
		{"a tracked credential file beside a spec is a finding", map[string]string{"compose.yaml": "x", "stack/.env": "S=1", "keys/id.pem": "k"}, 1, findings,
			[]string{"tracked files that must never be committed", "  stack/.env", "  keys/id.pem"}},
		// The compose atoms read the RAW tree: the chains never applied the
		// fleet exclude to this lane, so a vendored spec is still a spec.
		{"a spec under an excluded directory still counts", map[string]string{"vendor/compose.yaml": "x"}, 0, pass, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a scan that failed is not an absence the repository declared", func(t *testing.T) {
		in := treeIn(t, map[string]string{"compose.yaml": "x"})
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the compose-surface scan itself failed (boom)")
	})
}

func TestComposeThirdPartyPins(t *testing.T) {
	const id = "compose:third-party-pins"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no compose spec is absent", map[string]string{"a.yml": "image: x"}, 0, absent, []string{"tracks no compose.yaml/compose.yml"}},
		{"no interpolation keeps the ratchet closed", map[string]string{"compose.yaml": "image: x:1\n"}, 0, pass, []string{"the pin era stays closed"}},
		{"an image interpolation is a red merge with its line", map[string]string{"compose.yaml": "services:\n  a:\n    image: r/x:${PIN_X}\n"}, 1, findings,
			[]string{"1 ${PIN_} interpolation(s) found", "compose.yaml:3:    image: r/x:${PIN_X}"}},
		{"a pin under .forgejo is not scanned", map[string]string{"compose.yaml": "x", ".forgejo/w.yml": "image: ${PIN_X}"}, 0, pass, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a body that will not read is a ratchet not counted", func(t *testing.T) {
		in := treeIn(t, map[string]string{"compose.yaml": "x"})
		in.Committable = append(in.Committable, "gone.yml") // listed, and not on disk
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "could not read gone.yml", "closed ratchet on a scan that did not run")
	})
	t.Run("a scan that failed", func(t *testing.T) {
		in := treeIn(t, map[string]string{"compose.yaml": "x"})
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the compose-surface scan itself failed")
	})
}

func TestIndent(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{{"", nil}, {"\n", nil}, {"a", []string{"> a"}}, {"a\nb\n", []string{"> a", "> b"}}} {
		got := indent(tc.in, "> ")
		if len(got) != len(tc.want) {
			t.Fatalf("indent(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("indent(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
