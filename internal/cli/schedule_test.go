package cli_test

import (
	"regexp"
	"testing"

	"agentbox/internal/testutil"
)

// TestScheduleCommands adds a scheduled task, lists it, pauses it and removes it.
func TestScheduleCommands(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "add", testutil.FixtureRepo(t, "hello-stack")); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "schedule", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "no scheduled tasks")

	out, err = run(t, "", "schedule", "add", "hello-stack", "Triage new issues", "--cron", "0 8 * * 1", "--tz", "Europe/Lisbon", "--title", "Triage", "--agent")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `"Triage"`, `cron "0 8 * * 1" (Europe/Lisbon)`, "creating an agent", "next run Mon")
	id := regexp.MustCompile(`id ([0-9a-f]+)`).FindStringSubmatch(out)
	if id == nil {
		t.Fatalf("no id in %q", out)
	}
	if _, err := run(t, "", "schedule", "add", "hello-stack", "x", "--at", "2020-01-01 09:00"); err == nil {
		t.Error("a time in the past was scheduled")
	}
	out, err = run(t, "", "schedule", "pause", "hello-stack", id[1])
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "paused")
	if _, err = run(t, "", "schedule", "rm", "hello-stack", id[1]); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, "", "schedule", "ls", "hello-stack"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "no scheduled tasks")
}
