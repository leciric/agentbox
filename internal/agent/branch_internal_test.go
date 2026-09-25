package agent

import (
	"strings"
	"testing"
)

func TestSlugForBranch(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Fix: login redirect!":             "fix-login-redirect",
		"  feat/CSV export  ":              "feat-csv-export",
		"Add export\nwith all the details": "add-export",
		"🙂":                                "",
		"":                                 "",
		"Measure whether the token fixes from D83–D87 held, and what now fills agents' contexts": "measure-whether-the-token-fixes-from-d83-d87",
		strings.Repeat("x", 60): strings.Repeat("x", MaxBranchSlugLen),
	} {
		got := SlugForBranch(in)
		if got != want {
			t.Errorf("SlugForBranch(%q) = %q, want %q", in, got, want)
		}
		if err := CheckBranchSlug(got); err != nil {
			t.Errorf("SlugForBranch(%q) = %q, which CheckBranchSlug refuses: %v", in, got, err)
		}
	}
}

func TestUniqueBranch(t *testing.T) {
	t.Parallel()
	taken := map[string]bool{"agentbox/fix": true, "agentbox/fix-2": true, "agentbox/fix-4": true}
	if got := uniqueBranch("agentbox/", "fix", func(b string) bool { return taken[b] }); got != "agentbox/fix-3" {
		t.Errorf("uniqueBranch = %q, want agentbox/fix-3", got)
	}
	if got := uniqueBranch("", "free", func(b string) bool { return taken[b] }); got != "free" {
		t.Errorf("uniqueBranch = %q, want free", got)
	}
	// The suffix fits inside the cap, and never leaves a double hyphen.
	long := strings.Repeat("a", MaxBranchSlugLen-2) + "-b"
	got := uniqueBranch("p/", long, func(b string) bool { return b == "p/"+long })
	slug := strings.TrimPrefix(got, "p/")
	if err := CheckBranchSlug(slug); err != nil || !strings.HasSuffix(slug, "-2") {
		t.Errorf("uniqueBranch(%q) = %q: %v", long, got, err)
	}
}
