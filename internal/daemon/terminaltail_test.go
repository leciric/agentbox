package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestTerminalTailsAPI checks that GET /v1/projects/{project}/terminals reads
// each named agent's terminal and leaves out the ones it can't find, and that
// a second read within tailFresh is answered from the cache.
func TestTerminalTailsAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startTestDaemon(t, dir, `case "$1" in
  exec) echo x >> `+dir+`/execs; printf 'make\nbuilding…\n' ;;
esac`)
	ctx := context.Background()
	a := addTestAgent(t, d)

	for range 2 {
		tails, err := d.client.TerminalTails(ctx, a.Project, []string{a.Name, "nobody"})
		if err != nil {
			t.Fatal(err)
		}
		if len(tails.Agents) != 1 || tails.Agents[0].Ref != a.Ref() || !tails.Agents[0].Busy || len(tails.Agents[0].Lines) != 1 {
			t.Fatalf("TerminalTails() = %+v, want one busy tail for %s", tails, a.Ref())
		}
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
