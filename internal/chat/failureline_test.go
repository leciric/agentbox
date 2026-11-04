package chat

import (
	"strings"
	"testing"
)

// mise ends every error with a line that only says how to see more, so the
// last line of its stderr is the one that doesn't say what failed. A user's
// project chat reported "mise ERROR Run with --verbose or MISE_VERBOSE=1 for
// more information" and nothing else.
func TestFailureLineKeepsWhatMiseSaysFailed(t *testing.T) {
	t.Parallel()
	for stderr, want := range map[string]string{
		"mise ERROR No version is set for shim: node\nSet a global default version with one of the following:\nmise use -g node@24.0.0\nmise ERROR Run with --verbose or MISE_VERBOSE=1 for more information\n": "mise ERROR No version is set for shim: node",
		"mise ERROR Config files in /home/you/work/mise.toml are not trusted.\nTrust them with `mise trust`.\nmise ERROR Run with --verbose or MISE_VERBOSE=1 for more information":                             "mise ERROR Config files in /home/you/work/mise.toml are not trusted.",
		"mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information": "mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information",
		"warming up\nError: Cannot find module '/x/dist/index.js'\n":           "Error: Cannot find module '/x/dist/index.js'",
		"": "",
	} {
		if got := failureLine(stderr); got != want {
			t.Errorf("failureLine(%q) = %q, want %q", stderr, got, want)
		}
	}
}

// The same, end to end: a tool whose start fails that way says why in the chat.
func TestAStartThatMiseFailsSaysWhy(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, openStore(t), f)
	stderr := "mise ERROR No version is set for shim: node\nmise ERROR Run with --verbose or MISE_VERBOSE=1 for more information"
	launch, _ := failingFirst(f, 1, func() (*Process, error) { return exitedAtOnce(stderr), nil })
	m.Launch = launch
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if e := find(th, "error", 0); !strings.Contains(e.Text, "No version is set for shim: node") || strings.Contains(e.Text, "Run with --verbose") {
		t.Errorf("the chat says %q, want mise's own reason and not its --verbose line", e.Text)
	}
}
