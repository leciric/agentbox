package cli

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestDescribeArchived(t *testing.T) {
	if got := describeArchived(nil); got != "No past agents." {
		t.Errorf("none: %q", got)
	}
	got := describeArchived([]api.ArchivedAgent{{
		Ref: "pawly/agent-07", Name: "agent-07", Title: "Login fix", Branch: "agentbox/fix-login",
		Task:       "Fix the login\nredirect " + strings.Repeat("x", 300),
		ArchivedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
		PR:         &api.PullRequest{Number: 42, State: "merged", URL: "https://github.com/o/r/pull/42"},
	}})
	for _, want := range []string{"1 past agent(s)", `- agent-07 "Login fix", archived 2026-10-12, branch agentbox/fix-login, PR #42 (merged)`, "task: Fix the login redirect x", "…"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestPRCell(t *testing.T) {
	if prCell(nil) != "-" || prCell(&api.PullRequest{Number: 3, State: "open"}) != "#3 open" {
		t.Error("prCell")
	}
}
