package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestTerminalTailsAPI checks that GET /v1/projects/{project}/terminals reads
// each named agent's terminal and leaves out the ones it can't find, and that
// a second read within tailFresh is answered from the cache, the preview's longer tail included.
func TestTerminalTailsAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startTestDaemon(t, dir, `case "$1" in
  exec) echo x >> `+dir+`/execs; printf 'make\nl1\nl2\nl3\nl4\nl5\nl6\n' ;;
esac`)
	ctx := context.Background()
	a := addTestAgent(t, d)

	// The thumbnail's four lines, then its preview's six from the same read.
	for _, lines := range []int{0, 6} {
		tails, err := d.client.TerminalTails(ctx, a.Project, []string{a.Name, "nobody"}, lines)
		if err != nil {
			t.Fatal(err)
		}
		if len(tails.Agents) != 1 || tails.Agents[0].Ref != a.Ref() || !tails.Agents[0].Busy || len(tails.Agents[0].Lines) != max(lines, 4) || tails.Agents[0].Lines[len(tails.Agents[0].Lines)-1] != "l6" {
			t.Fatalf("TerminalTails(%d lines) = %+v, want one busy tail for %s", lines, tails, a.Ref())
		}
	}
	if _, err := d.client.TerminalTails(ctx, a.Project, []string{a.Name}, 41); err == nil {
		t.Error("TerminalTails() with 41 lines should be refused")
	}
	if n := countLines(t, dir+"/execs"); n != 1 {
		t.Errorf("the terminal was read %d times, want once (cached)", n)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}
