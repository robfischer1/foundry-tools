package atomkit

import "testing"

func TestUT_448_EscapeID(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"fleet:check-merge-conflict", "fleet__check-merge-conflict"},
		{"a:b:c", "a__b__c"},
		{"", ""},
	} {
		if got := EscapeID(c.in); got != c.want {
			t.Errorf("EscapeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
