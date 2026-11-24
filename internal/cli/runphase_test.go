package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `agentbox run`, left from run slots, waits for nothing: up with no command
// and down return at once saying so, and up with one runs it.
func TestRunWaitsForNothing(t *testing.T) {
	for _, args := range [][]string{{}, {"up"}, {"down"}, {"status"}} {
		cmd := newRunCmd(nil)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "Run slots are gone") {
			t.Errorf("run %v: %v, %q", args, err, out.String())
		}
	}
	was := execReal
	defer func() { execReal = was }()
	var ran []string
	execReal = func(_ string, argv, _ []string) error { ran = argv; return nil }
	cmd := newRunCmd(nil)
	cmd.SetArgs([]string{"up", "--", "sh", "-c", "true"})
	if err := cmd.Execute(); err != nil || strings.Join(ran, " ") != "sh -c true" {
		t.Errorf("run up -- sh -c true: %v, ran %q", err, ran)
	}
}
