package checks

import "testing"

func TestIndent(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{{"", nil}, {"\n", nil}, {"a", []string{"> a"}}, {"a\nb\n", []string{"> a", "> b"}}} {
		got := Indent(tc.in, "> ")
		if len(got) != len(tc.want) {
			t.Fatalf("Indent(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Indent(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
