package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newHubTestServer stands up a server for HubClient's own tests and returns
// its URL, cleaning itself up when the test ends.
func newHubTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestChatClientMethodsEncodeRequestsAndDecodeResponses covers the chat
// surface: every call goes through chatDo, which routes "pawly" and
// "pawly/agent-01" refs to different paths (see chatPath), so these also
// exercise that routing on live calls rather than only on chatPath itself.
func TestChatClientMethodsEncodeRequestsAndDecodeResponses(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "ProjectChat", method: http.MethodGet, path: "/v1/projects/pawly/lead",
			resp: `{"project":"pawly","started":true}`,
			run: func(t *testing.T, c *Client) {
				chat, err := c.ProjectChat(context.Background(), "pawly")
				if err != nil || !chat.Started {
					t.Errorf("ProjectChat() = %+v, %v; want Started true, nil", chat, err)
				}
			},
		},
		{
			name: "ResetProjectChat", method: http.MethodDelete, path: "/v1/projects/pawly/lead",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.ResetProjectChat(context.Background(), "pawly"); err != nil {
					t.Errorf("ResetProjectChat() = %v", err)
				}
			},
		},
		{
			name: "Chat for an agent", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/chat",
			resp: `{"agent":"agent-01","seq":3}`,
			run: func(t *testing.T, c *Client) {
				thread, err := c.Chat(context.Background(), "pawly/agent-01")
				if err != nil || thread.Seq != 3 {
					t.Errorf("Chat() = %+v, %v; want Seq 3, nil", thread, err)
				}
			},
		},
		{
			name: "Chat for the project's own lead", method: http.MethodGet, path: "/v1/projects/pawly/chat",
			resp: `{"agent":"lead","seq":1}`,
			run: func(t *testing.T, c *Client) {
				thread, err := c.Chat(context.Background(), "pawly")
				if err != nil || thread.Agent != "lead" {
					t.Errorf("Chat() = %+v, %v; want the lead's thread, nil", thread, err)
				}
			},
		},
		{
			name: "StartChat", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/chat/start",
			resp: `{"state":"starting"}`,
			run: func(t *testing.T, c *Client) {
				sess, err := c.StartChat(context.Background(), "pawly/agent-01")
				if err != nil || sess.State != ChatStarting {
					t.Errorf("StartChat() = %+v, %v; want state starting, nil", sess, err)
				}
			},
		},
		{
			name: "SendChat", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/chat/messages",
			body: `{"text":"go on","images":[{"mimeType":"image/png","data":"YWJj","name":"shot.png"}]}`,
			resp: `{"id":"item-1","kind":"user"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.SendChat(context.Background(), "pawly/agent-01", "go on", ChatImageUpload{MimeType: "image/png", Data: "YWJj", Name: "shot.png"})
				if err != nil || item.ID != "item-1" {
					t.Errorf("SendChat() = %+v, %v; want ID item-1, nil", item, err)
				}
			},
		},
		{
			name: "CancelChat", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/chat/cancel",
			resp: `{"state":"ready"}`,
			run: func(t *testing.T, c *Client) {
				sess, err := c.CancelChat(context.Background(), "pawly/agent-01")
				if err != nil || sess.State != ChatReady {
					t.Errorf("CancelChat() = %+v, %v; want state ready, nil", sess, err)
				}
			},
		},
		{
			name: "AnswerChat", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/chat/permissions/item-1",
			body: `{"optionId":"allow_once"}`,
			resp: `{"id":"item-1"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.AnswerChat(context.Background(), "pawly/agent-01", "item-1", "allow_once")
				if err != nil || item.ID != "item-1" {
					t.Errorf("AnswerChat() = %+v, %v; want ID item-1, nil", item, err)
				}
			},
		},
		{
			name: "SetChatOption", method: http.MethodPut, path: "/v1/agents/pawly/agent-01/chat/options/model",
			body: `{"value":"opus"}`,
			resp: `{"state":"ready"}`,
			run: func(t *testing.T, c *Client) {
				sess, err := c.SetChatOption(context.Background(), "pawly/agent-01", "model", "opus")
				if err != nil || sess.State != ChatReady {
					t.Errorf("SetChatOption() = %+v, %v; want state ready, nil", sess, err)
				}
			},
		},
		{
			name: "ClearChat", method: http.MethodDelete, path: "/v1/agents/pawly/agent-01/chat",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.ClearChat(context.Background(), "pawly/agent-01"); err != nil {
					t.Errorf("ClearChat() = %v", err)
				}
			},
		},
		{
			name: "Files", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/files",
			resp: `{"files":["main.go","README.md"]}`,
			run: func(t *testing.T, c *Client) {
				files, err := c.Files(context.Background(), "pawly/agent-01")
				if err != nil || len(files.Files) != 2 {
					t.Errorf("Files() = %+v, %v; want 2 files, nil", files, err)
				}
			},
		},
	})
}

// TestLeadClientMethodsEncodeRequestsAndDecodeResponses covers the lead
// socket's own routes, none of which names a project: leadPath always
// prefixes "/v1/project", never "/v1/projects/<name>".
func TestLeadClientMethodsEncodeRequestsAndDecodeResponses(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "ProjectFleet", method: http.MethodGet, path: "/v1/project/agents",
			resp: `{"project":"pawly","idle":1}`,
			run: func(t *testing.T, c *Client) {
				fleet, err := c.ProjectFleet(context.Background())
				if err != nil || fleet.Idle != 1 {
					t.Errorf("ProjectFleet() = %+v, %v; want Idle 1, nil", fleet, err)
				}
			},
		},
		{
			name: "ProjectAccounts", method: http.MethodGet, path: "/v1/project/accounts",
			resp: `[{"name":"work","default":true}]`,
			run: func(t *testing.T, c *Client) {
				accounts, err := c.ProjectAccounts(context.Background())
				if err != nil || len(accounts) != 1 || !accounts[0].Default {
					t.Errorf("ProjectAccounts() = %+v, %v; want one default account, nil", accounts, err)
				}
			},
		},
		{
			name: "ProjectSelf", method: http.MethodGet, path: "/v1/project",
			resp: `{"name":"pawly"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.ProjectSelf(context.Background())
				if err != nil || p.Name != "pawly" {
					t.Errorf("ProjectSelf() = %+v, %v; want pawly, nil", p, err)
				}
			},
		},
		{
			name: "ProjectSettings", method: http.MethodGet, path: "/v1/project/settings",
			resp: `{"defaultLeadModel":"opus"}`,
			run: func(t *testing.T, c *Client) {
				s, err := c.ProjectSettings(context.Background())
				if err != nil || s.DefaultLeadModel != "opus" {
					t.Errorf("ProjectSettings() = %+v, %v; want opus, nil", s, err)
				}
			},
		},
		{
			name: "CreateProjectAgent", method: http.MethodPost, path: "/v1/project/agents",
			body: `{"project":"pawly","ai":""}`,
			resp: `{"id":"job-1"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.CreateProjectAgent(context.Background(), CreateAgentRequest{Project: "pawly"})
				if err != nil || job.ID != "job-1" {
					t.Errorf("CreateProjectAgent() = %+v, %v; want ID job-1, nil", job, err)
				}
			},
		},
		{
			name: "TellAgent", method: http.MethodPost, path: "/v1/project/agents/agent-01/chat/messages",
			body: `{"text":"keep going"}`,
			resp: `{"id":"item-2"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.TellAgent(context.Background(), "agent-01", "keep going")
				if err != nil || item.ID != "item-2" {
					t.Errorf("TellAgent() = %+v, %v; want ID item-2, nil", item, err)
				}
			},
		},
		{
			name: "AgentChat", method: http.MethodGet, path: "/v1/project/agents/agent-01/chat",
			resp: `{"agent":"agent-01","seq":5}`,
			run: func(t *testing.T, c *Client) {
				thread, err := c.AgentChat(context.Background(), "agent-01")
				if err != nil || thread.Seq != 5 {
					t.Errorf("AgentChat() = %+v, %v; want Seq 5, nil", thread, err)
				}
			},
		},
		{
			name: "AgentDiff", method: http.MethodGet, path: "/v1/project/agents/agent-01/diff?path=a.go&path=b.go&stat=true",
			resp: `{"diff":"--- a/a.go"}`,
			run: func(t *testing.T, c *Client) {
				diff, err := c.AgentDiff(context.Background(), "agent-01", true, "a.go", "b.go")
				if err != nil || diff != "--- a/a.go" {
					t.Errorf("AgentDiff() = %q, %v; want the diff text, nil", diff, err)
				}
			},
		},
		{
			name: "AgentDiff with no filters", method: http.MethodGet, path: "/v1/project/agents/agent-01/diff",
			resp: `{"diff":""}`,
			run: func(t *testing.T, c *Client) {
				diff, err := c.AgentDiff(context.Background(), "agent-01", false)
				if err != nil || diff != "" {
					t.Errorf("AgentDiff() = %q, %v; want empty, nil", diff, err)
				}
			},
		},
		{
			name: "RetireProject", method: http.MethodPost, path: "/v1/project/retire",
			body: `{"how":"pause"}`,
			resp: `{"how":"pause","retired":[],"skipped":[]}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.RetireProject(context.Background(), RetireRequest{How: "pause"})
				if err != nil || res.How != "pause" {
					t.Errorf("RetireProject() = %+v, %v; want how pause, nil", res, err)
				}
			},
		},
		{
			name: "ProjectNotes", method: http.MethodGet, path: "/v1/project/notes",
			resp: `{"text":"watch out for X"}`,
			run: func(t *testing.T, c *Client) {
				notes, err := c.ProjectNotes(context.Background())
				if err != nil || notes.Text != "watch out for X" {
					t.Errorf("ProjectNotes() = %+v, %v; want the text, nil", notes, err)
				}
			},
		},
		{
			name: "AppendProjectNote", method: http.MethodPost, path: "/v1/project/notes",
			body: `{"text":"found a flaky test"}`,
			resp: `{"text":"found a flaky test"}`,
			run: func(t *testing.T, c *Client) {
				notes, err := c.AppendProjectNote(context.Background(), "found a flaky test")
				if err != nil || notes.Text != "found a flaky test" {
					t.Errorf("AppendProjectNote() = %+v, %v; want the note, nil", notes, err)
				}
			},
		},
		{
			name: "EditProjectNote", method: http.MethodPost, path: "/v1/project/notes/edit",
			body: `{"match":"flaky test","text":"fixed the flaky test"}`,
			resp: `{"was":"flaky test","now":"fixed the flaky test"}`,
			run: func(t *testing.T, c *Client) {
				change, err := c.EditProjectNote(context.Background(), "flaky test", "fixed the flaky test")
				if err != nil || change.Now != "fixed the flaky test" {
					t.Errorf("EditProjectNote() = %+v, %v; want the new text, nil", change, err)
				}
			},
		},
		{
			name: "RemoveProjectNote", method: http.MethodPost, path: "/v1/project/notes/remove",
			body: `{"match":"fixed the flaky test"}`,
			resp: `{"was":"fixed the flaky test"}`,
			run: func(t *testing.T, c *Client) {
				change, err := c.RemoveProjectNote(context.Background(), "fixed the flaky test")
				if err != nil || change.Was != "fixed the flaky test" {
					t.Errorf("RemoveProjectNote() = %+v, %v; want the removed text, nil", change, err)
				}
			},
		},
		{
			name: "ProjectQuestions", method: http.MethodGet, path: "/v1/project/questions",
			resp: `[{"id":"q1"}]`,
			run: func(t *testing.T, c *Client) {
				qs, err := c.ProjectQuestions(context.Background())
				if err != nil || len(qs) != 1 {
					t.Errorf("ProjectQuestions() = %+v, %v; want 1 question, nil", qs, err)
				}
			},
		},
		{
			name: "LeadAnswerQuestion", method: http.MethodPost, path: "/v1/project/questions/q1/answer",
			body: `{"answer":"go ahead"}`,
			resp: `{"id":"q1","answer":"go ahead"}`,
			run: func(t *testing.T, c *Client) {
				q, err := c.LeadAnswerQuestion(context.Background(), "q1", "go ahead")
				if err != nil || q.Answer != "go ahead" {
					t.Errorf("LeadAnswerQuestion() = %+v, %v; want go ahead, nil", q, err)
				}
			},
		},
		{
			name: "LeadEscalateQuestion", method: http.MethodPost, path: "/v1/project/questions/q1/escalate",
			body: `{"why":"too risky to decide alone"}`,
			resp: `{"id":"q1","status":"escalated"}`,
			run: func(t *testing.T, c *Client) {
				q, err := c.LeadEscalateQuestion(context.Background(), "q1", "too risky to decide alone")
				if err != nil || q.Status != "escalated" {
					t.Errorf("LeadEscalateQuestion() = %+v, %v; want status escalated, nil", q, err)
				}
			},
		},
		{
			name: "RunInAgent", method: http.MethodPost, path: "/v1/project/agents/agent-01/run",
			body: `{"command":"go test ./..."}`,
			resp: `{"exitCode":0,"output":"ok","bytes":2}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.RunInAgent(context.Background(), "agent-01", LeadRunRequest{Command: "go test ./..."})
				if err != nil || res.ExitCode != 0 || res.Output != "ok" {
					t.Errorf("RunInAgent() = %+v, %v; want exit 0, output ok, nil", res, err)
				}
			},
		},
		{
			name: "CopyBetweenAgents", method: http.MethodPost, path: "/v1/project/copy",
			body: `{"from":"agent-01","path":"out.txt","to":"agent-02"}`,
			resp: `{"bytes":42}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.CopyBetweenAgents(context.Background(), LeadCopyRequest{From: "agent-01", Path: "out.txt", To: "agent-02"})
				if err != nil || res.Bytes != 42 {
					t.Errorf("CopyBetweenAgents() = %+v, %v; want Bytes 42, nil", res, err)
				}
			},
		},
	})
}

// TestHubClientMethodsEncodeRequestsAndDecodeResponses covers HubClient,
// including that a trailing slash on its URL doesn't leak into the request
// path (client() trims it) and that Environment() builds the right base for
// an environment's own daemon API.
func TestHubClientMethodsEncodeRequestsAndDecodeResponses(t *testing.T) {
	t.Run("Login", func(t *testing.T) {
		var gotPath, gotBody string
		srv := newHubTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			gotBody = string(b)
			_, _ = w.Write([]byte(`{"token":"session-1"}`))
		})
		h := HubClient{URL: srv + "/", Token: ""}
		sess, err := h.Login(context.Background(), HubLoginRequest{Email: "a@example.com", Password: "hunter2"})
		if err != nil {
			t.Fatalf("Login() = %v", err)
		}
		if sess.Token != "session-1" {
			t.Fatalf("Login() = %+v, %v; want a session token, nil", sess, err)
		}
		if gotPath != "/v1/auth/login" {
			t.Errorf("path = %q, want /v1/auth/login with no doubled slash", gotPath)
		}
		if gotBody != `{"email":"a@example.com","password":"hunter2"}` {
			t.Errorf("body = %s", gotBody)
		}
	})

	t.Run("Me and Environments use the session token", func(t *testing.T) {
		var gotAuth string
		srv := newHubTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			switch r.URL.Path {
			case "/v1/me":
				_, _ = w.Write([]byte(`{"email":"a@example.com"}`))
			case "/v1/environments":
				_, _ = w.Write([]byte(`[{"id":"env-1"}]`))
			}
		})
		h := HubClient{URL: srv, Token: "session-1"}
		user, err := h.Me(context.Background())
		if err != nil || user.Email != "a@example.com" {
			t.Fatalf("Me() = %+v, %v; want a@example.com, nil", user, err)
		}
		if gotAuth != "Bearer session-1" {
			t.Errorf("Authorization = %q, want the session as a bearer token", gotAuth)
		}
		envs, err := h.Environments(context.Background())
		if err != nil || len(envs) != 1 || envs[0].ID != "env-1" {
			t.Errorf("Environments() = %+v, %v; want [{env-1}], nil", envs, err)
		}
	})

	t.Run("CreateEnvironment and DeleteEnvironment", func(t *testing.T) {
		var gotMethod, gotPath, gotBody string
		srv := newHubTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			gotBody = string(b)
			_, _ = w.Write([]byte(`{"environment":{"id":"env-2"},"token":"env-token"}`))
		})
		h := HubClient{URL: srv}
		out, err := h.CreateEnvironment(context.Background(), "pawly")
		if err != nil || out.Environment.ID != "env-2" || out.Token != "env-token" {
			t.Fatalf("CreateEnvironment() = %+v, %v; want id env-2 and its token, nil", out, err)
		}
		if gotMethod != http.MethodPost || gotPath != "/v1/environments" || gotBody != `{"name":"pawly"}` {
			t.Errorf("request = %s %s %s, want POST /v1/environments {\"name\":\"pawly\"}", gotMethod, gotPath, gotBody)
		}

		if err := h.DeleteEnvironment(context.Background(), "env-2"); err != nil {
			t.Fatalf("DeleteEnvironment() = %v", err)
		}
		if gotMethod != http.MethodDelete || gotPath != "/v1/environments/env-2" {
			t.Errorf("request = %s %s, want DELETE /v1/environments/env-2", gotMethod, gotPath)
		}
	})

	t.Run("Environment builds a client scoped to that environment's API", func(t *testing.T) {
		h := HubClient{URL: "https://hub.example.com/", Token: "session-1"}
		env := h.Environment("env-1")
		if env.base != "https://hub.example.com/v1/environments/env-1/api" {
			t.Errorf("Environment(env-1).base = %q, want the environment's API root", env.base)
		}
		if env.token != "session-1" {
			t.Errorf("Environment(env-1).token = %q, want the hub session carried over", env.token)
		}
	})
}
