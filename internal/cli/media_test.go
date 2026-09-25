package cli

import (
	"slices"
	"testing"
)

func TestSplitRef(t *testing.T) {
	for _, tc := range []struct {
		args []string
		n    int
		ref  string
		rest []string
	}{
		{[]string{"notes.txt"}, 1, "", []string{"notes.txt"}},
		{[]string{"pawly/agent-01", "notes.txt"}, 1, "pawly/agent-01", []string{"notes.txt"}},
		{[]string{"fix/typo"}, 1, "", []string{"fix/typo"}},
		{[]string{"pawly/agent-01"}, 0, "pawly/agent-01", []string{}},
		{[]string{"Fix the login/logout flow", "x"}, 1, "", []string{"Fix the login/logout flow", "x"}},
	} {
		ref, rest := splitRef(tc.args, tc.n)
		if ref != tc.ref || !slices.Equal(rest, tc.rest) {
			t.Errorf("splitRef(%q, %d) = %q, %q; want %q, %q", tc.args, tc.n, ref, rest, tc.ref, tc.rest)
		}
	}
}
