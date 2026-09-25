package agent

import "testing"

// TestGitQuoteEscapesBackslashesAndQuotes checks the escaping gitrepo relies
// on to build a "commit message" for a snapshot that names an agent whose ref
// may itself contain a backslash or a quote.
func TestGitQuoteEscapesBackslashesAndQuotes(t *testing.T) {
	cases := map[string]string{
		"plain":      `"plain"`,
		`back\slash`: `"back\\slash"`,
		`a "quote"`:  `"a \"quote\""`,
		`\"both\"`:   `"\\\"both\\\""`,
	}
	for in, want := range cases {
		if got := gitQuote(in); got != want {
			t.Errorf("gitQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
