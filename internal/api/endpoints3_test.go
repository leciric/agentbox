package api

import (
	"context"
	"net/http"
	"testing"
)

// TestMemoryClientMethodsEncodeRequestsAndDecodeResponses covers all three
// surfaces MemoryClient is built from (see memory_client.go's doc comment):
// ProjectMemory names a project in its path, LeadMemory and SelfMemory don't.
func TestMemoryClientMethodsEncodeRequestsAndDecodeResponses(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "ProjectMemory Events with a query", method: http.MethodGet,
			path: "/v1/projects/pawly/memory/events?agent=agent-01&limit=10&type=finish&type=ask",
			resp: `[{"id":"e1"}]`,
			run: func(t *testing.T, c *Client) {
				events, err := c.ProjectMemory("pawly").Events(context.Background(), EventQuery{
					Agent: "agent-01", Types: []string{"finish", "ask"}, Limit: 10,
				})
				if err != nil || len(events) != 1 || events[0].ID != "e1" {
					t.Errorf("Events() = %+v, %v; want [{e1}], nil", events, err)
				}
			},
		},
		{
			name: "ProjectMemory Events with no query narrows nothing", method: http.MethodGet,
			path: "/v1/projects/pawly/memory/events",
			resp: `[]`,
			run: func(t *testing.T, c *Client) {
				events, err := c.ProjectMemory("pawly").Events(context.Background(), EventQuery{})
				if err != nil || len(events) != 0 {
					t.Errorf("Events() = %+v, %v; want none, nil", events, err)
				}
			},
		},
		{
			name: "LeadMemory AppendEvent", method: http.MethodPost, path: "/v1/project/memory/events",
			body: `{"type":"finish","agent":"agent-01"}`,
			resp: `{"id":"e2","type":"finish"}`,
			run: func(t *testing.T, c *Client) {
				ev, err := c.LeadMemory().AppendEvent(context.Background(), AddMemoryEventRequest{Type: "finish", Agent: "agent-01"})
				if err != nil || ev.ID != "e2" {
					t.Errorf("AppendEvent() = %+v, %v; want ID e2, nil", ev, err)
				}
			},
		},
		{
			name: "SelfMemory Memories with kinds", method: http.MethodGet,
			path: "/v1/self/memory/memories?kind=decision%2Cissue",
			resp: `[{"id":"m1","kind":"decision"}]`,
			run: func(t *testing.T, c *Client) {
				mems, err := c.SelfMemory().Memories(context.Background(), "decision", "issue")
				if err != nil || len(mems) != 1 {
					t.Errorf("Memories() = %+v, %v; want 1 memory, nil", mems, err)
				}
			},
		},
		{
			name: "ProjectMemory Memories with no kinds", method: http.MethodGet,
			path: "/v1/projects/pawly/memory/memories",
			resp: `[]`,
			run: func(t *testing.T, c *Client) {
				mems, err := c.ProjectMemory("pawly").Memories(context.Background())
				if err != nil || len(mems) != 0 {
					t.Errorf("Memories() = %+v, %v; want none, nil", mems, err)
				}
			},
		},
		{
			name: "AddMemory", method: http.MethodPost, path: "/v1/projects/pawly/memory/memories",
			body: `{"title":"switched to SQLite"}`,
			resp: `{"id":"m2","title":"switched to SQLite"}`,
			run: func(t *testing.T, c *Client) {
				mem, err := c.ProjectMemory("pawly").AddMemory(context.Background(), AddMemoryRequest{Title: "switched to SQLite"})
				if err != nil || mem.ID != "m2" {
					t.Errorf("AddMemory() = %+v, %v; want ID m2, nil", mem, err)
				}
			},
		},
		{
			name: "Search", method: http.MethodPost, path: "/v1/projects/pawly/memory/search",
			body: `{"query":"sqlite","limit":5}`,
			resp: `{"memories":[{"id":"m2"}]}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.ProjectMemory("pawly").Search(context.Background(), "sqlite", 5)
				if err != nil || len(res.Memories) != 1 {
					t.Errorf("Search() = %+v, %v; want 1 memory, nil", res, err)
				}
			},
		},
		{
			name: "WorkingMemory", method: http.MethodGet, path: "/v1/projects/pawly/memory/working",
			resp: `{"goal":"ship v0.4"}`,
			run: func(t *testing.T, c *Client) {
				wm, err := c.ProjectMemory("pawly").WorkingMemory(context.Background())
				if err != nil || wm.Goal != "ship v0.4" {
					t.Errorf("WorkingMemory() = %+v, %v; want goal ship v0.4, nil", wm, err)
				}
			},
		},
		{
			name: "SetWorkingMemory", method: http.MethodPatch, path: "/v1/projects/pawly/memory/working",
			body: `{"goal":"ship v0.4"}`,
			resp: `{"goal":"ship v0.4"}`,
			run: func(t *testing.T, c *Client) {
				goal := "ship v0.4"
				wm, err := c.ProjectMemory("pawly").SetWorkingMemory(context.Background(), WorkingMemoryPatch{Goal: &goal})
				if err != nil || wm.Goal != "ship v0.4" {
					t.Errorf("SetWorkingMemory() = %+v, %v; want goal ship v0.4, nil", wm, err)
				}
			},
		},
		{
			name: "Artifacts", method: http.MethodGet, path: "/v1/projects/pawly/memory/artifacts",
			resp: `[{"id":"a1"}]`,
			run: func(t *testing.T, c *Client) {
				arts, err := c.ProjectMemory("pawly").Artifacts(context.Background())
				if err != nil || len(arts) != 1 {
					t.Errorf("Artifacts() = %+v, %v; want 1 artifact, nil", arts, err)
				}
			},
		},
		{
			name: "AddArtifact", method: http.MethodPost, path: "/v1/projects/pawly/memory/artifacts",
			body: `{"type":"branch","path":"agentbox/agent-08"}`,
			resp: `{"id":"a2","type":"branch"}`,
			run: func(t *testing.T, c *Client) {
				art, err := c.ProjectMemory("pawly").AddArtifact(context.Background(), AddArtifactRequest{Type: "branch", Path: "agentbox/agent-08"})
				if err != nil || art.ID != "a2" {
					t.Errorf("AddArtifact() = %+v, %v; want ID a2, nil", art, err)
				}
			},
		},
		{
			name: "Reports for one agent", method: http.MethodGet, path: "/v1/projects/pawly/memory/reports?agent=agent-01",
			resp: `[{"id":"r1"}]`,
			run: func(t *testing.T, c *Client) {
				reports, err := c.ProjectMemory("pawly").Reports(context.Background(), "agent-01")
				if err != nil || len(reports) != 1 {
					t.Errorf("Reports() = %+v, %v; want 1 report, nil", reports, err)
				}
			},
		},
		{
			name: "AddReport", method: http.MethodPost, path: "/v1/projects/pawly/memory/reports",
			body: `{"summary":"raised coverage to 60%"}`,
			resp: `{"id":"r2","summary":"raised coverage to 60%"}`,
			run: func(t *testing.T, c *Client) {
				report, err := c.ProjectMemory("pawly").AddReport(context.Background(), AddReportRequest{Summary: "raised coverage to 60%"})
				if err != nil || report.ID != "r2" {
					t.Errorf("AddReport() = %+v, %v; want ID r2, nil", report, err)
				}
			},
		},
		{
			name: "Context", method: http.MethodPost, path: "/v1/projects/pawly/memory/context",
			body: `{"query":"what changed in internal/api"}`,
			resp: `{"text":"internal/api is a thin client..."}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.ProjectMemory("pawly").Context(context.Background(), ContextRequest{Query: "what changed in internal/api"})
				if err != nil || res.Text == "" {
					t.Errorf("Context() = %+v, %v; want built text, nil", res, err)
				}
			},
		},
		{
			name: "ContextStats", method: http.MethodGet, path: "/v1/projects/pawly/memory/context/stats",
			resp: `{"builds":4,"tokens":8000}`,
			run: func(t *testing.T, c *Client) {
				stats, err := c.ProjectMemory("pawly").ContextStats(context.Background())
				if err != nil || stats.Builds != 4 {
					t.Errorf("ContextStats() = %+v, %v; want Builds 4, nil", stats, err)
				}
			},
		},
		{
			name: "Tasks with a query", method: http.MethodGet,
			path: "/v1/projects/pawly/memory/tasks?agent=agent-01&open=true&parent=t1&status=open",
			resp: `[{"id":"t2"}]`,
			run: func(t *testing.T, c *Client) {
				tasks, err := c.ProjectMemory("pawly").Tasks(context.Background(), TaskQuery{
					Agent: "agent-01", Statuses: []string{"open"}, Parent: "t1", OpenOnly: true,
				})
				if err != nil || len(tasks) != 1 {
					t.Errorf("Tasks() = %+v, %v; want 1 task, nil", tasks, err)
				}
			},
		},
		{
			name: "Task by id", method: http.MethodGet, path: "/v1/projects/pawly/memory/tasks/t2",
			resp: `{"id":"t2","goal":"write tests"}`,
			run: func(t *testing.T, c *Client) {
				task, err := c.ProjectMemory("pawly").Task(context.Background(), "t2")
				if err != nil || task.Goal != "write tests" {
					t.Errorf("Task() = %+v, %v; want goal write tests, nil", task, err)
				}
			},
		},
		{
			name: "AddTask", method: http.MethodPost, path: "/v1/projects/pawly/memory/tasks",
			body: `{"goal":"write tests"}`,
			resp: `{"id":"t3","goal":"write tests"}`,
			run: func(t *testing.T, c *Client) {
				task, err := c.ProjectMemory("pawly").AddTask(context.Background(), AddTaskRequest{Goal: "write tests"})
				if err != nil || task.ID != "t3" {
					t.Errorf("AddTask() = %+v, %v; want ID t3, nil", task, err)
				}
			},
		},
		{
			name: "UpdateTask", method: http.MethodPatch, path: "/v1/projects/pawly/memory/tasks/t3",
			body: `{"status":"done"}`,
			resp: `{"id":"t3","status":"done"}`,
			run: func(t *testing.T, c *Client) {
				status := "done"
				task, err := c.ProjectMemory("pawly").UpdateTask(context.Background(), "t3", UpdateTaskRequest{Status: &status})
				if err != nil || task.Status != "done" {
					t.Errorf("UpdateTask() = %+v, %v; want status done, nil", task, err)
				}
			},
		},
		{
			name: "LinkTasks", method: http.MethodPost, path: "/v1/projects/pawly/memory/tasks/link",
			body: `{"taskId":"t3","dependsOnId":"t2"}`,
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.ProjectMemory("pawly").LinkTasks(context.Background(), "t3", "t2"); err != nil {
					t.Errorf("LinkTasks() = %v", err)
				}
			},
		},
		{
			name: "UnlinkTasks", method: http.MethodPost, path: "/v1/projects/pawly/memory/tasks/unlink",
			body: `{"taskId":"t3","dependsOnId":"t2"}`,
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.ProjectMemory("pawly").UnlinkTasks(context.Background(), "t3", "t2"); err != nil {
					t.Errorf("UnlinkTasks() = %v", err)
				}
			},
		},
		{
			name: "ResolveMemory", method: http.MethodPost, path: "/v1/projects/pawly/memory/resolve",
			body: `{"id":"m2","why":"no longer true"}`,
			resp: `{"id":"m2","resolvedWhy":"no longer true"}`,
			run: func(t *testing.T, c *Client) {
				mem, err := c.ProjectMemory("pawly").ResolveMemory(context.Background(), "m2", "no longer true")
				if err != nil || mem.ID != "m2" {
					t.Errorf("ResolveMemory() = %+v, %v; want ID m2, nil", mem, err)
				}
			},
		},
		{
			name: "Consolidation", method: http.MethodGet, path: "/v1/projects/pawly/memory/consolidation",
			resp: `{"setting":20,"events":100}`,
			run: func(t *testing.T, c *Client) {
				cons, err := c.ProjectMemory("pawly").Consolidation(context.Background())
				if err != nil || cons.Setting != 20 {
					t.Errorf("Consolidation() = %+v, %v; want Setting 20, nil", cons, err)
				}
			},
		},
		{
			name: "Duplicates", method: http.MethodGet, path: "/v1/projects/pawly/memory/duplicates",
			resp: `[{"similarity":0.9}]`,
			run: func(t *testing.T, c *Client) {
				dups, err := c.ProjectMemory("pawly").Duplicates(context.Background())
				if err != nil || len(dups) != 1 || dups[0].Similarity != 0.9 {
					t.Errorf("Duplicates() = %+v, %v; want one pair at 0.9, nil", dups, err)
				}
			},
		},
		{
			name: "Consolidate distilling", method: http.MethodPost, path: "/v1/projects/pawly/memory/consolidate",
			body: `{"distil":true}`,
			resp: `[{"id":"c1","kind":"distill"}]`,
			run: func(t *testing.T, c *Client) {
				passes, err := c.ProjectMemory("pawly").Consolidate(context.Background(), true)
				if err != nil || len(passes) != 1 || passes[0].Kind != "distill" {
					t.Errorf("Consolidate() = %+v, %v; want one distill pass, nil", passes, err)
				}
			},
		},
	})
}

// TestTokensMethodsEncodeQueriesAndDecodeReports checks TokenQuery.values()
// end to end: every field it can set, and that a zero query sends no query
// string at all rather than an empty "?".
func TestTokensMethodsEncodeQueriesAndDecodeReports(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "Tokens with a full query", method: http.MethodGet,
			path: "/v1/tokens?agent=agent-01&project=pawly&since=2026-01-02T03%3A04%3A05Z",
			resp: `{"total":100}`,
			run: func(t *testing.T, c *Client) {
				report, err := c.Tokens(context.Background(), TokenQuery{Project: "pawly", Agent: "agent-01", Since: testTime})
				if err != nil || report.Total != 100 {
					t.Errorf("Tokens() = %+v, %v; want Total 100, nil", report, err)
				}
			},
		},
		{
			name: "Tokens with a zero query", method: http.MethodGet, path: "/v1/tokens",
			resp: `{"total":0}`,
			run: func(t *testing.T, c *Client) {
				report, err := c.Tokens(context.Background(), TokenQuery{})
				if err != nil || report.Total != 0 {
					t.Errorf("Tokens() = %+v, %v; want Total 0, nil", report, err)
				}
			},
		},
		{
			name: "TokenTurns with a limit", method: http.MethodGet, path: "/v1/tokens/turns?limit=5&project=pawly",
			resp: `[{"project":"pawly"}]`,
			run: func(t *testing.T, c *Client) {
				turns, err := c.TokenTurns(context.Background(), TokenQuery{Project: "pawly"}, 5)
				if err != nil || len(turns) != 1 {
					t.Errorf("TokenTurns() = %+v, %v; want 1 turn, nil", turns, err)
				}
			},
		},
	})
}

// TestSecretsPathValidatesTheTarget checks SecretsPath's own rules — a
// project alone, or "<project>/<agent>" — separately from the calls that use
// it, since a bad target must fail before any request is sent.
func TestSecretsPathValidatesTheTarget(t *testing.T) {
	tests := []struct {
		target  string
		want    string
		wantErr bool
	}{
		{"pawly", "/v1/projects/pawly/secrets", false},
		{"pawly/agent-01", "/v1/agents/pawly/agent-01/secrets", false},
		{"", "", true},
		{"pawly/", "", true},
		{"pawly/a/b", "", true},
	}
	for _, tt := range tests {
		got, err := SecretsPath(tt.target)
		if (err != nil) != tt.wantErr {
			t.Errorf("SecretsPath(%q) err = %v, wantErr %v", tt.target, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("SecretsPath(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}
}

// TestSecretsClientMethodsEncodeRequestsAndDecodeResponses checks the
// requests these calls build never carry a value on the way out — Secret has
// no Value field to begin with, so this leans on SetSecret's request body
// instead, the one place a value is ever sent (D52).
func TestSecretsClientMethodsEncodeRequestsAndDecodeResponses(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "Secrets for a project", method: http.MethodGet, path: "/v1/projects/pawly/secrets",
			resp: `[{"name":"API_KEY","scope":"project"}]`,
			run: func(t *testing.T, c *Client) {
				secrets, err := c.Secrets(context.Background(), "pawly")
				if err != nil || len(secrets) != 1 || secrets[0].Name != "API_KEY" {
					t.Errorf("Secrets() = %+v, %v; want [API_KEY], nil", secrets, err)
				}
			},
		},
		{
			name: "Secrets for an agent", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/secrets",
			resp: `[]`,
			run: func(t *testing.T, c *Client) {
				secrets, err := c.Secrets(context.Background(), "pawly/agent-01")
				if err != nil || len(secrets) != 0 {
					t.Errorf("Secrets() = %+v, %v; want none, nil", secrets, err)
				}
			},
		},
		{
			name: "SetSecret", method: http.MethodPut, path: "/v1/projects/pawly/secrets/API_KEY",
			body: `{"value":"sk-secret"}`,
			resp: `{"name":"API_KEY","scope":"project"}`,
			run: func(t *testing.T, c *Client) {
				secret, err := c.SetSecret(context.Background(), "pawly", "API_KEY", "sk-secret")
				if err != nil || secret.Name != "API_KEY" {
					t.Errorf("SetSecret() = %+v, %v; want API_KEY, nil", secret, err)
				}
			},
		},
		{
			name: "RemoveSecret", method: http.MethodDelete, path: "/v1/projects/pawly/secrets/API_KEY",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemoveSecret(context.Background(), "pawly", "API_KEY"); err != nil {
					t.Errorf("RemoveSecret() = %v", err)
				}
			},
		},
		{
			name: "ProjectSecretNames", method: http.MethodGet, path: "/v1/project/secrets",
			resp: `[{"name":"API_KEY","scope":"project"},{"name":"TOKEN","scope":"agent","agent":"agent-01"}]`,
			run: func(t *testing.T, c *Client) {
				secrets, err := c.ProjectSecretNames(context.Background())
				if err != nil || len(secrets) != 2 {
					t.Errorf("ProjectSecretNames() = %+v, %v; want 2 secrets, nil", secrets, err)
				}
			},
		},
	})
}

// TestClaudeLimitsDecodesTheAccountsWindows checks ClaudeLimits reads the
// usage windows an account carries, not just that the call succeeds.
func TestClaudeLimitsDecodesTheAccountsWindows(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "ClaudeLimits", method: http.MethodGet, path: "/v1/limits",
			resp: `[{"account":"work","windows":[{"name":"five_hour","utilization":0.4}]}]`,
			run: func(t *testing.T, c *Client) {
				limits, err := c.ClaudeLimits(context.Background())
				if err != nil || len(limits) != 1 || len(limits[0].Windows) != 1 || limits[0].Windows[0].Utilization != 0.4 {
					t.Errorf("ClaudeLimits() = %+v, %v; want one account with a five_hour window at 0.4, nil", limits, err)
				}
			},
		},
	})
}

// TestJobDoneIsFalseOnlyWhileRunning checks Done covers every terminal
// status, not just the one the daemon happens to reach first.
func TestJobDoneIsFalseOnlyWhileRunning(t *testing.T) {
	for status, wantDone := range map[string]bool{
		JobRunning:   false,
		JobSucceeded: true,
		JobFailed:    true,
		JobCancelled: true,
	} {
		if got := (Job{Status: status}).Done(); got != wantDone {
			t.Errorf("Job{Status: %q}.Done() = %v, want %v", status, got, wantDone)
		}
	}
}

// TestThemeAppliedAndDescribe checks Applied is true only when the setting
// follows a theme that was actually found, and Describe reads right in each
// of the three cases that matters to a log line: pinned, followed but
// unnamed, and followed and named.
func TestThemeAppliedAndDescribe(t *testing.T) {
	tests := []struct {
		name         string
		theme        Theme
		wantApplied  bool
		wantDescribe string
	}{
		{"pinned light ignores an available theme", Theme{Appearance: AppearanceLight, Available: true, Name: "tokyo-night"}, false, "AgentBox's own colours"},
		{"follow with nothing found", Theme{Appearance: AppearanceFollow, Available: false}, false, "AgentBox's own colours"},
		{"follow with a theme but no name", Theme{Appearance: AppearanceFollow, Available: true}, true, "this machine's theme"},
		{"follow with a named theme", Theme{Appearance: AppearanceFollow, Available: true, Name: "tokyo-night"}, true, "tokyo-night"},
	}
	for _, tt := range tests {
		if got := tt.theme.Applied(); got != tt.wantApplied {
			t.Errorf("%s: Applied() = %v, want %v", tt.name, got, tt.wantApplied)
		}
		if got := tt.theme.Describe(); got != tt.wantDescribe {
			t.Errorf("%s: Describe() = %q, want %q", tt.name, got, tt.wantDescribe)
		}
	}
}
