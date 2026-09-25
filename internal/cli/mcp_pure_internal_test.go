package cli

// Tests for the MCP tools' pure text-rendering helpers: what a model reads
// back from search_memory, project_state, fleet and the rest, which is worth
// getting right for exactly the reason the tools' own descriptions give — a
// model reads this and nothing else.

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestDescribeFleet(t *testing.T) {
	if got := describeFleet(api.Fleet{}); !strings.Contains(got, "no agents yet. Create one with create_agent.") {
		t.Errorf("describeFleet(empty) = %q", got)
	}
	got := describeFleet(api.Fleet{
		Agents: []api.FleetAgent{{
			Agent:   api.Agent{Name: "agent-01", Title: "Fix login", Branch: "agentbox/agent-01", State: "running", AI: "claude", ClaudeAccount: "work"},
			Changes: api.AgentChanges{Files: 2},
			Media:   3,
			PR:      &api.PullRequest{Number: 5, State: "open"},
		}},
		Idle: 1,
	})
	for _, want := range []string{
		"agent-01", "Fix login", "branch: agentbox/agent-01, machine: running",
		"account: work", "changes:", "3 thing(s) shown", "pull request: #5 open",
		"1 agent(s) finished and are holding a machine. retire_agent frees one",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("describeFleet missing %q:\n%s", want, got)
		}
	}
}

func TestDescribeSecrets(t *testing.T) {
	if got := describeSecrets(nil); !strings.Contains(got, "have no secrets") {
		t.Errorf("describeSecrets(none) = %q", got)
	}
	got := describeSecrets([]api.Secret{
		{Name: "STRIPE_KEY", Scope: "project"},
		{Name: "AGENT_TOKEN", Scope: "agent", Agent: "agent-02"},
	})
	if !strings.Contains(got, "$STRIPE_KEY — every agent of this project") || !strings.Contains(got, "$AGENT_TOKEN — agent-02 only") {
		t.Errorf("describeSecrets = %q", got)
	}
}

func TestDescribeQuestions(t *testing.T) {
	if got := describeQuestions(nil); got != "No agent is waiting on you." {
		t.Errorf("describeQuestions(none) = %q", got)
	}
	got := describeQuestions([]api.Question{
		{ID: "q1", Agent: "agent-01", Status: "escalated", Question: "Paginate?", Context: "building the list page"},
		{ID: "q2", Agent: "agent-02", Kind: api.CredentialSecret, SecretName: "API_KEY", Question: "needs a key"},
	})
	for _, want := range []string{
		"id q1, from agent-01 (escalated)", "Paginate?", "what it was doing: building the list page",
		"id q2, from agent-02: asking the user for the secret $API_KEY",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("describeQuestions missing %q:\n%s", want, got)
		}
	}
}

func TestCredentialWanted(t *testing.T) {
	if got := credentialWanted(api.Question{Kind: api.CredentialSecret, SecretName: "STRIPE_KEY"}); got != "the secret $STRIPE_KEY" {
		t.Errorf("credentialWanted(secret) = %q", got)
	}
	if got := credentialWanted(api.Question{Kind: api.CredentialGitHub}); got != "a GitHub account" {
		t.Errorf("credentialWanted(github) = %q", got)
	}
}

func TestDescribeTasksAndHelpers(t *testing.T) {
	tasks := []api.Task{
		{ID: "t1", Goal: "Fix login", Status: "active", Agent: "agent-01", DependsOn: []string{"t2"}},
		{ID: "t2", Goal: "Add tests", Status: "open", ParentID: "t1", Blocks: []string{"t1"}, Detail: "cover the happy path"},
	}
	got := describeTasks(tasks)
	for _, want := range []string{
		"[t1] Fix login (active, agent-01)", "waiting on: Add tests (t2)",
		"[t2] Add tests (open)", "part of: Fix login (t1)", "holding up: Fix login (t1)", "cover the happy path",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("describeTasks missing %q:\n%s", want, got)
		}
	}

	if got := taskName("t9", map[string]string{"t9": "Do it"}); got != "Do it (t9)" {
		t.Errorf("taskName(known) = %q", got)
	}
	if got := taskName("t9", nil); got != "t9" {
		t.Errorf("taskName(unknown) = %q", got)
	}

	if got := taskOwner(api.Task{}); got != "whichever agent picks it up" {
		t.Errorf("taskOwner(unassigned) = %q", got)
	}
	if got := taskOwner(api.Task{Agent: "agent-03"}); got != "agent-03" {
		t.Errorf("taskOwner(assigned) = %q", got)
	}

	if !openTask("active") || !openTask("blocked") || !openTask("open") {
		t.Error("openTask says a working status isn't open")
	}
	if openTask(api.TaskDone) || openTask(api.TaskAbandoned) {
		t.Error("openTask says a finished status is still open")
	}
}

func TestDescribeSearchAndWorkingMemory(t *testing.T) {
	if got := describeSearch("ports", api.MemorySearchResults{}); !strings.Contains(got, `Nothing remembered about "ports" yet`) {
		t.Errorf("describeSearch(nothing) = %q", got)
	}
	results := api.MemorySearchResults{
		Memories: []api.Memory{{ID: "m1", Title: "Postgres is on 5433", Kind: "fact", Importance: 3, Content: "not the default 5432"}},
		Reports:  []api.AgentReport{{Agent: "agent-01", Status: "done", Summary: "Shipped the login page"}},
	}
	got := describeSearch("postgres", results)
	for _, want := range []string{"[m1] Postgres is on 5433 (fact, importance 3)", "not the default 5432", "agent-01 (done): Shipped the login page"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeSearch missing %q:\n%s", want, got)
		}
	}

	if got := describeWorking(api.WorkingMemory{}); !strings.Contains(got, "Nothing recorded") {
		t.Errorf("describeWorking(empty) = %q", got)
	}
	got = describeWorking(api.WorkingMemory{Goal: "Ship reminders", CurrentTask: "the list page", ActiveAgents: []string{"agent-01"}, Blockers: []string{"waiting on design"}, Notes: "check with the lead"})
	for _, want := range []string{"Goal: Ship reminders", "Now: the list page", "On it: agent-01", "Blocked: waiting on design", "Notes: check with the lead"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeWorking missing %q:\n%s", want, got)
		}
	}
}

func TestDescribeRunAndSizeOf(t *testing.T) {
	if got := describeRun(api.LeadRunResult{TimedOut: true}); !strings.Contains(got, "ran out of time") {
		t.Errorf("describeRun(timed out) = %q", got)
	}
	if got := describeRun(api.LeadRunResult{ExitCode: 0, Output: ""}); !strings.Contains(got, "Exit code 0.") || !strings.Contains(got, "printed nothing") {
		t.Errorf("describeRun(clean, nothing printed) = %q", got)
	}
	if got := describeRun(api.LeadRunResult{ExitCode: 1, Output: "boom\n", Bytes: 4}); !strings.Contains(got, "Exit code 1.") || !strings.Contains(got, "boom") {
		t.Errorf("describeRun(failed) = %q", got)
	}
	if got := describeRun(api.LeadRunResult{ExitCode: 0, Output: "only the tail", Bytes: 1000}); !strings.Contains(got, "It printed") || !strings.Contains(got, "this is the end of it") {
		t.Errorf("describeRun(truncated) = %q", got)
	}

	for _, c := range []struct {
		n    int64
		want string
	}{
		{500, "500 bytes"},
		{2048, "2 KiB"},
		{5 << 20, "5.0 MiB"},
	} {
		if got := sizeOf(c.n); got != c.want {
			t.Errorf("sizeOf(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestDescribeNoteChange(t *testing.T) {
	got := describeNoteChange(api.NoteChange{Section: "Stack", Was: "pnpm monorepo", Now: "pnpm monorepo, Postgres on 5433", FromLead: true}, "Edited")
	for _, want := range []string{`Edited, under "Stack".`, "was: pnpm monorepo", "now: pnpm monorepo, Postgres on 5433"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeNoteChange missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "the user's own text") {
		t.Errorf("describeNoteChange wrongly warns about the lead's own edit:\n%s", got)
	}

	got = describeNoteChange(api.NoteChange{Was: "old note"}, "Removed")
	if !strings.Contains(got, "the user's own text, not an entry of yours") {
		t.Errorf("describeNoteChange doesn't warn about a user's note:\n%s", got)
	}
}

func TestClip(t *testing.T) {
	if got := clip("short", 10); got != "short" {
		t.Errorf("clip(short) = %q", got)
	}
	if got := clip("this is a longer sentence", 7); got != "this is […]" {
		t.Errorf("clip(long) = %q", got)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("line one\nline   two"); got != "line one line two" {
		t.Errorf("oneLine(multiline) = %q", got)
	}
	if got := oneLine(strings.Repeat("a", 250)); got != strings.Repeat("a", 200)+"…" {
		t.Errorf("oneLine(long) doesn't cut at 200 runes")
	}
}
