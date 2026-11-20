package agent

import (
	"slices"
	"testing"
)

// Every shimmed command is linked, and none is the shim's own file.
func TestShimLinks(t *testing.T) {
	t.Parallel()
	script := shimLinks("/home/u/" + ShimDir)
	for _, name := range ShimmedCommands {
		if !slices.Contains(ShimmedCommands, name) || name == shimFile {
			t.Fatalf("%s", name)
		}
		if want := " && ln -sfn " + shimFile + " " + name; !contains(script, want) {
			t.Errorf("no link for %s", name)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
