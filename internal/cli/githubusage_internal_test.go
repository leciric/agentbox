package cli

import (
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// agentbox github usage names the account, says how much of what GitHub
// counted was AgentBox's and how much its agents', and lists the busiest
// callers first.
func TestRenderGitHubUsage(t *testing.T) {
	now := time.Now()
	usage := api.GitHubUsage{Accounts: []api.GitHubAccountUsage{{
		Token: "abc123", Account: "work", Login: "octocat", Since: now.Add(-time.Hour),
		Rates: []api.GitHubRate{{Resource: "core", Limit: 5000, Remaining: 120, Used: 4880, Ours: 410, Others: 4470, Reset: now.Add(20 * time.Minute), At: now}},
		Calls: []api.GitHubCallCount{
			{Caller: "pulls", Method: "GET", Endpoint: "/repos/{owner}/{repo}/pulls", Sent: 240, Counted: 12, NotModified: 228, Last: now},
			{Caller: "merge", Method: "PUT", Endpoint: "/repos/{owner}/{repo}/pulls/{n}/merge", Sent: 1, Counted: 1, Last: now},
		},
	}}}
	var out strings.Builder
	if err := renderGitHubUsage(&out, usage, false, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"octocat (work)",
		"120 of 5000 left",
		"AgentBox 410, others (agents' gh, you) 4470",
		"PUT /repos/{owner}/{repo}/pulls/{n}/merge",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "pulls  ") > strings.Index(got, "merge  ") {
		t.Errorf("the busiest caller isn't first:\n%s", got)
	}

	out.Reset()
	if err := renderGitHubUsage(&out, api.GitHubUsage{}, false, now); err != nil || !strings.Contains(out.String(), "hasn't asked GitHub") {
		t.Errorf("no usage = %q, %v", out.String(), err)
	}
}
