package agent

import "testing"

// The ceiling compares models by what they cost. A name it can't rank, like
// "default" (whatever Claude Code picks for the account), is never refused
// as above one.
func TestModelTier(t *testing.T) {
	for model, want := range map[string]int{
		"haiku": 1, "claude-haiku-4-5-20251001": 1, "sonnet": 2, "sonnet[1m]": 2,
		"opus": 3, "opus[1m]": 3, "claude-opus-5-5": 3, "claude-fable-5-1": 4,
		"default": 0, "something-new": 0,
	} {
		if got := modelTier(model); got != want {
			t.Errorf("modelTier(%q) = %d, want %d", model, got, want)
		}
	}
}
