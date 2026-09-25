package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// endpointCase drives one Client (or MemoryClient/HubClient) call against a
// server that checks the request it sends and answers with a canned
// response; run asserts what the call decoded that response into. Together
// the two halves cover both directions of the wire: a wrong path, method or
// request body fails the same way a wrong decode of the response would.
type endpointCase struct {
	name   string
	method string
	path   string // the exact request-URI the call must send
	body   string // the exact JSON body the call must send; "" for none
	resp   string // the server's response body
	run    func(t *testing.T, c *Client)
}

func runEndpointCases(t *testing.T, cases []endpointCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.RequestURI()
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.Write([]byte(tc.resp))
			}))
			defer srv.Close()
			c := NewRemoteClient(srv.URL, "")
			tc.run(t, c)
			if gotMethod != tc.method {
				t.Errorf("method = %s, want %s", gotMethod, tc.method)
			}
			if gotPath != tc.path {
				t.Errorf("path = %s, want %s", gotPath, tc.path)
			}
			if tc.body != "" && gotBody != tc.body {
				t.Errorf("body = %s, want %s", gotBody, tc.body)
			}
			if tc.body == "" && gotBody != "" {
				t.Errorf("body = %s, want none sent", gotBody)
			}
		})
	}
}

var testTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

const testTimeJSON = `2026-01-02T03:04:05Z`

// TestClientMethodsEncodeRequestsAndDecodeResponses walks most of Client's
// one-line wrappers: each names the path (including any query it builds) and
// request body a call must produce, and checks what it decodes the response
// into — the two places a hand-written wrapper actually goes wrong.
func TestClientMethodsEncodeRequestsAndDecodeResponses(t *testing.T) {
	runEndpointCases(t, []endpointCase{
		{
			name: "Fleet", method: http.MethodGet, path: "/v1/projects/pawly/fleet",
			resp: `{"project":"pawly","agents":[],"idle":2}`,
			run: func(t *testing.T, c *Client) {
				fleet, err := c.Fleet(context.Background(), "pawly")
				if err != nil || fleet.Idle != 2 {
					t.Errorf("Fleet() = %+v, %v; want Idle 2, nil", fleet, err)
				}
			},
		},
		{
			name: "Ask", method: http.MethodPost, path: "/v1/self/ask",
			body: `{"question":"deploy now?","context":"release"}`,
			resp: `{"id":"q1","status":"pending"}`,
			run: func(t *testing.T, c *Client) {
				q, err := c.Ask(context.Background(), "deploy now?", "release")
				if err != nil || q.ID != "q1" {
					t.Errorf("Ask() = %+v, %v; want ID q1, nil", q, err)
				}
			},
		},
		{
			name: "AgentEvents", method: http.MethodGet, path: "/v1/projects/pawly/agent-events",
			resp: `[{"id":"e1"},{"id":"e2"}]`,
			run: func(t *testing.T, c *Client) {
				events, err := c.AgentEvents(context.Background(), "pawly")
				if err != nil || len(events) != 2 || events[0].ID != "e1" {
					t.Errorf("AgentEvents() = %+v, %v; want 2 events starting e1", events, err)
				}
			},
		},
		{
			name: "RequestCredential", method: http.MethodPost, path: "/v1/self/credential",
			body: `{"kind":"secret","name":"API_KEY","reason":"needs it"}`,
			resp: `{"id":"q2","status":"pending"}`,
			run: func(t *testing.T, c *Client) {
				q, err := c.RequestCredential(context.Background(), CredentialRequest{Kind: CredentialSecret, Name: "API_KEY", Reason: "needs it"})
				if err != nil || q.ID != "q2" {
					t.Errorf("RequestCredential() = %+v, %v; want ID q2, nil", q, err)
				}
			},
		},
		{
			name: "AnswerCredential", method: http.MethodPost, path: "/v1/projects/pawly/questions/q2/credential",
			body: `{"value":"secret-value"}`,
			resp: `{"id":"q2","status":"answered"}`,
			run: func(t *testing.T, c *Client) {
				q, err := c.AnswerCredential(context.Background(), "pawly", "q2", AnswerCredentialRequest{Value: "secret-value"})
				if err != nil || q.Status != "answered" {
					t.Errorf("AnswerCredential() = %+v, %v; want status answered, nil", q, err)
				}
			},
		},
		{
			name: "Questions all", method: http.MethodGet, path: "/v1/projects/pawly/questions?all=1",
			resp: `[{"id":"q1"}]`,
			run: func(t *testing.T, c *Client) {
				qs, err := c.Questions(context.Background(), "pawly", true)
				if err != nil || len(qs) != 1 {
					t.Errorf("Questions(all) = %+v, %v; want 1 question, nil", qs, err)
				}
			},
		},
		{
			name: "Questions waiting only", method: http.MethodGet, path: "/v1/projects/pawly/questions",
			resp: `[]`,
			run: func(t *testing.T, c *Client) {
				qs, err := c.Questions(context.Background(), "pawly", false)
				if err != nil || len(qs) != 0 {
					t.Errorf("Questions() = %+v, %v; want none, nil", qs, err)
				}
			},
		},
		{
			name: "AnswerQuestion", method: http.MethodPost, path: "/v1/projects/pawly/questions/q1/answer",
			body: `{"answer":"yes"}`,
			resp: `{"id":"q1","answer":"yes"}`,
			run: func(t *testing.T, c *Client) {
				q, err := c.AnswerQuestion(context.Background(), "pawly", "q1", "yes")
				if err != nil || q.Answer != "yes" {
					t.Errorf("AnswerQuestion() = %+v, %v; want Answer yes, nil", q, err)
				}
			},
		},
		{
			name: "Sections", method: http.MethodGet, path: "/v1/sections",
			resp: `[{"id":"s1","name":"Active"}]`,
			run: func(t *testing.T, c *Client) {
				secs, err := c.Sections(context.Background())
				if err != nil || len(secs) != 1 || secs[0].Name != "Active" {
					t.Errorf("Sections() = %+v, %v; want [{s1 Active}], nil", secs, err)
				}
			},
		},
		{
			name: "AddSection", method: http.MethodPost, path: "/v1/sections",
			body: `{"name":"Backlog"}`,
			resp: `{"id":"s2","name":"Backlog"}`,
			run: func(t *testing.T, c *Client) {
				sec, err := c.AddSection(context.Background(), "Backlog")
				if err != nil || sec.ID != "s2" {
					t.Errorf("AddSection() = %+v, %v; want ID s2, nil", sec, err)
				}
			},
		},
		{
			name: "UpdateSection", method: http.MethodPatch, path: "/v1/sections/s2",
			body: `{"collapsed":true}`,
			resp: `{"id":"s2","collapsed":true}`,
			run: func(t *testing.T, c *Client) {
				collapsed := true
				sec, err := c.UpdateSection(context.Background(), "s2", UpdateSectionRequest{Collapsed: &collapsed})
				if err != nil || !sec.Collapsed {
					t.Errorf("UpdateSection() = %+v, %v; want Collapsed true, nil", sec, err)
				}
			},
		},
		{
			name: "RemoveSection", method: http.MethodDelete, path: "/v1/sections/s2",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemoveSection(context.Background(), "s2"); err != nil {
					t.Errorf("RemoveSection() = %v", err)
				}
			},
		},
		{
			name: "SetProjectLayout", method: http.MethodPut, path: "/v1/projects/layout",
			body: `{"sections":[{"id":"s1","projects":["pawly"]}],"loose":["other"]}`,
			resp: `[{"name":"pawly"},{"name":"other"}]`,
			run: func(t *testing.T, c *Client) {
				projects, err := c.SetProjectLayout(context.Background(), ProjectLayout{
					Sections: []SectionProjects{{ID: "s1", Projects: []string{"pawly"}}},
					Loose:    []string{"other"},
				})
				if err != nil || len(projects) != 2 {
					t.Errorf("SetProjectLayout() = %+v, %v; want 2 projects, nil", projects, err)
				}
			},
		},
		{
			name: "SetAutonomy", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"autonomy":"on"}`,
			resp: `{"name":"pawly","autonomy":"on"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetAutonomy(context.Background(), "pawly", "on")
				if err != nil || p.Autonomy != "on" {
					t.Errorf("SetAutonomy() = %+v, %v; want Autonomy on, nil", p, err)
				}
			},
		},
		{
			name: "SetFinishNotices", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"finishNotices":"off"}`,
			resp: `{"name":"pawly","finishNotices":"off"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetFinishNotices(context.Background(), "pawly", "off")
				if err != nil || p.FinishNotices != "off" {
					t.Errorf("SetFinishNotices() = %+v, %v; want off, nil", p, err)
				}
			},
		},
		{
			name: "SetRolloverThreshold", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"rolloverThreshold":50}`,
			resp: `{"name":"pawly","rolloverThreshold":50}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetRolloverThreshold(context.Background(), "pawly", 50)
				if err != nil || p.RolloverThreshold != 50 {
					t.Errorf("SetRolloverThreshold() = %+v, %v; want 50, nil", p, err)
				}
			},
		},
		{
			name: "SetContextBudget", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"contextBudget":8000}`,
			resp: `{"name":"pawly","contextBudget":8000}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetContextBudget(context.Background(), "pawly", 8000)
				if err != nil || p.ContextBudget != 8000 {
					t.Errorf("SetContextBudget() = %+v, %v; want 8000, nil", p, err)
				}
			},
		},
		{
			name: "SetConsolidation", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"consolidation":25}`,
			resp: `{"name":"pawly","consolidation":25}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetConsolidation(context.Background(), "pawly", 25)
				if err != nil || p.Consolidation != 25 {
					t.Errorf("SetConsolidation() = %+v, %v; want 25, nil", p, err)
				}
			},
		},
		{
			name: "SetConsolidationModel", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"consolidationModel":"cheap"}`,
			resp: `{"name":"pawly","consolidationModel":"cheap"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetConsolidationModel(context.Background(), "pawly", "cheap")
				if err != nil || p.ConsolidationModel != "cheap" {
					t.Errorf("SetConsolidationModel() = %+v, %v; want cheap, nil", p, err)
				}
			},
		},
		{
			name: "RolloverChat", method: http.MethodPost, path: "/v1/projects/pawly/chat/rollover",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RolloverChat(context.Background(), "pawly"); err != nil {
					t.Errorf("RolloverChat() = %v", err)
				}
			},
		},
		{
			name: "SetAgentModel", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"agentModel":"auto"}`,
			resp: `{"name":"pawly","agentModel":"auto"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetAgentModel(context.Background(), "pawly", AgentModelAuto)
				if err != nil || p.AgentModel != "auto" {
					t.Errorf("SetAgentModel() = %+v, %v; want auto, nil", p, err)
				}
			},
		},
		{
			name: "SetBranchPrefix", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{"branchPrefix":"thiago/"}`,
			resp: `{"name":"pawly","branchPrefix":"thiago/"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.SetBranchPrefix(context.Background(), "pawly", "thiago/")
				if err != nil || p.BranchPrefix != "thiago/" {
					t.Errorf("SetBranchPrefix() = %+v, %v; want thiago/, nil", p, err)
				}
			},
		},
		{
			name: "SetMediaRetention", method: http.MethodPatch, path: "/v1/settings",
			body: `{"mediaRetention":"7d"}`,
			resp: `{"mediaRetention":"7d"}`,
			run: func(t *testing.T, c *Client) {
				s, err := c.SetMediaRetention(context.Background(), "7d")
				if err != nil || s.MediaRetention != "7d" {
					t.Errorf("SetMediaRetention() = %+v, %v; want 7d, nil", s, err)
				}
			},
		},
		{
			name: "Retire", method: http.MethodPost, path: "/v1/projects/pawly/retire",
			body: `{"how":"stop","dryRun":true}`,
			resp: `{"how":"stop","dryRun":true,"retired":[],"skipped":[{"name":"agent-01","reason":"busy"}]}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.Retire(context.Background(), "pawly", RetireRequest{How: "stop", DryRun: true})
				if err != nil || len(res.Skipped) != 1 || res.Skipped[0].Reason != "busy" {
					t.Errorf("Retire() = %+v, %v; want one skipped, reason busy, nil", res, err)
				}
			},
		},
		{
			name: "ProjectMedia with filters", method: http.MethodGet, path: "/v1/projects/pawly/media?agent=agent-01&kind=screenshot",
			resp: `[{"id":"m1"}]`,
			run: func(t *testing.T, c *Client) {
				items, err := c.ProjectMedia(context.Background(), "pawly", "agent-01", "screenshot")
				if err != nil || len(items) != 1 || items[0].ID != "m1" {
					t.Errorf("ProjectMedia() = %+v, %v; want [{m1}], nil", items, err)
				}
			},
		},
		{
			name: "ProjectMedia unfiltered", method: http.MethodGet, path: "/v1/projects/pawly/media",
			resp: `[]`,
			run: func(t *testing.T, c *Client) {
				items, err := c.ProjectMedia(context.Background(), "pawly", "", "")
				if err != nil || len(items) != 0 {
					t.Errorf("ProjectMedia() = %+v, %v; want none, nil", items, err)
				}
			},
		},
		{
			name: "ProjectPullRequests", method: http.MethodGet, path: "/v1/projects/pawly/pulls",
			resp: `{"github":"leciric/agentbox"}`,
			run: func(t *testing.T, c *Client) {
				pulls, err := c.ProjectPullRequests(context.Background(), "pawly")
				if err != nil || pulls.GitHub != "leciric/agentbox" {
					t.Errorf("ProjectPullRequests() = %+v, %v; want github leciric/agentbox, nil", pulls, err)
				}
			},
		},
		{
			name: "MergePullRequest", method: http.MethodPost, path: "/v1/projects/pawly/pulls/42/merge",
			body: `{"method":"squash"}`,
			resp: `{"number":42,"state":"merged"}`,
			run: func(t *testing.T, c *Client) {
				pr, err := c.MergePullRequest(context.Background(), "pawly", 42, "squash")
				if err != nil || pr.Number != 42 {
					t.Errorf("MergePullRequest() = %+v, %v; want number 42, nil", pr, err)
				}
			},
		},
		{
			name: "RemoveGitHubAccount", method: http.MethodDelete, path: "/v1/auth/github/work",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemoveGitHubAccount(context.Background(), "work"); err != nil {
					t.Errorf("RemoveGitHubAccount() = %v", err)
				}
			},
		},
		{
			name: "SetDefaultGitHubAccount", method: http.MethodPost, path: "/v1/auth/github/work/default",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.SetDefaultGitHubAccount(context.Background(), "work"); err != nil {
					t.Errorf("SetDefaultGitHubAccount() = %v", err)
				}
			},
		},
		{
			name: "RenameGitHubAccount", method: http.MethodPost, path: "/v1/auth/github/work/rename",
			body: `{"name":"personal"}`,
			resp: `{"name":"personal"}`,
			run: func(t *testing.T, c *Client) {
				renamed, err := c.RenameGitHubAccount(context.Background(), "work", "personal")
				if err != nil || renamed.Name != "personal" {
					t.Errorf("RenameGitHubAccount() = %+v, %v; want personal, nil", renamed, err)
				}
			},
		},
		{
			name: "Settings", method: http.MethodGet, path: "/v1/settings",
			resp: `{"defaultLeadModel":"opus"}`,
			run: func(t *testing.T, c *Client) {
				s, err := c.Settings(context.Background())
				if err != nil || s.DefaultLeadModel != "opus" {
					t.Errorf("Settings() = %+v, %v; want opus, nil", s, err)
				}
			},
		},
		{
			name: "Setup", method: http.MethodGet, path: "/v1/setup",
			resp: `{"ready":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.Setup(context.Background())
				if err != nil || !status.Ready {
					t.Errorf("Setup() = %+v, %v; want Ready true, nil", status, err)
				}
			},
		},
		{
			name: "SaveClaudeToken", method: http.MethodPost, path: "/v1/auth/claude",
			body: `{"token":"sk-1","account":"work"}`,
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.SaveClaudeToken(context.Background(), ClaudeTokenRequest{Token: "sk-1", Account: "work"}); err != nil {
					t.Errorf("SaveClaudeToken() = %v", err)
				}
			},
		},
		{
			name: "StartClaudeLogin", method: http.MethodPost, path: "/v1/auth/claude/login",
			body: `{"account":"work"}`,
			resp: `{"id":"job-1","status":"running"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.StartClaudeLogin(context.Background(), ClaudeLoginRequest{Account: "work"})
				if err != nil || job.ID != "job-1" {
					t.Errorf("StartClaudeLogin() = %+v, %v; want ID job-1, nil", job, err)
				}
			},
		},
		{
			name: "ClaudeLogin", method: http.MethodGet, path: "/v1/auth/claude/login/job-1",
			resp: `{"url":"https://claude.ai/login"}`,
			run: func(t *testing.T, c *Client) {
				login, err := c.ClaudeLogin(context.Background(), "job-1")
				if err != nil || login.URL != "https://claude.ai/login" {
					t.Errorf("ClaudeLogin() = %+v, %v; want the login URL, nil", login, err)
				}
			},
		},
		{
			name: "ClaudeLoginCode", method: http.MethodPost, path: "/v1/auth/claude/login/job-1/code",
			body: `{"code":"123456"}`,
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.ClaudeLoginCode(context.Background(), "job-1", "123456"); err != nil {
					t.Errorf("ClaudeLoginCode() = %v", err)
				}
			},
		},
		{
			name: "RemoveClaudeAccount", method: http.MethodDelete, path: "/v1/auth/claude/work",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemoveClaudeAccount(context.Background(), "work"); err != nil {
					t.Errorf("RemoveClaudeAccount() = %v", err)
				}
			},
		},
		{
			name: "RenameClaudeAccount", method: http.MethodPost, path: "/v1/auth/claude/work/rename",
			body: `{"name":"personal"}`,
			resp: `{"name":"personal"}`,
			run: func(t *testing.T, c *Client) {
				renamed, err := c.RenameClaudeAccount(context.Background(), "work", "personal")
				if err != nil || renamed.Name != "personal" {
					t.Errorf("RenameClaudeAccount() = %+v, %v; want personal, nil", renamed, err)
				}
			},
		},
		{
			name: "SetDefaultClaudeAccount", method: http.MethodPost, path: "/v1/auth/claude/work/default",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.SetDefaultClaudeAccount(context.Background(), "work"); err != nil {
					t.Errorf("SetDefaultClaudeAccount() = %v", err)
				}
			},
		},
		{
			name: "Media", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/media",
			resp: `[{"id":"m1"}]`,
			run: func(t *testing.T, c *Client) {
				items, err := c.Media(context.Background(), "pawly/agent-01")
				if err != nil || len(items) != 1 {
					t.Errorf("Media() = %+v, %v; want 1 item, nil", items, err)
				}
			},
		},
		{
			name: "MediaItem", method: http.MethodGet, path: "/v1/media/m1",
			resp: `{"id":"m1","kind":"screenshot"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.MediaItem(context.Background(), "m1")
				if err != nil || item.Kind != "screenshot" {
					t.Errorf("MediaItem() = %+v, %v; want kind screenshot, nil", item, err)
				}
			},
		},
		{
			name: "DeleteMedia", method: http.MethodDelete, path: "/v1/media/m1",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.DeleteMedia(context.Background(), "m1"); err != nil {
					t.Errorf("DeleteMedia() = %v", err)
				}
			},
		},
		{
			name: "DeleteProjectMedia", method: http.MethodPost, path: "/v1/projects/pawly/media/delete",
			body: `{"ids":["m1","m2"]}`,
			resp: `{"deleted":2}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.DeleteProjectMedia(context.Background(), "pawly", DeleteMediaRequest{IDs: []string{"m1", "m2"}})
				if err != nil || res.Deleted != 2 {
					t.Errorf("DeleteProjectMedia() = %+v, %v; want Deleted 2, nil", res, err)
				}
			},
		},
		{
			name: "DeleteAgentMedia", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/delete",
			body: `{"ids":["m1"]}`,
			resp: `{"deleted":1}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.DeleteAgentMedia(context.Background(), "pawly/agent-01", DeleteMediaRequest{IDs: []string{"m1"}})
				if err != nil || res.Deleted != 1 {
					t.Errorf("DeleteAgentMedia() = %+v, %v; want Deleted 1, nil", res, err)
				}
			},
		},
		{
			name: "Screenshot", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/screenshot",
			body: `{"name":"before"}`,
			resp: `{"id":"m3","kind":"screenshot"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.Screenshot(context.Background(), "pawly/agent-01", ScreenshotRequest{Name: "before"})
				if err != nil || item.ID != "m3" {
					t.Errorf("Screenshot() = %+v, %v; want ID m3, nil", item, err)
				}
			},
		},
		{
			name: "Recording", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/media/record",
			resp: `{"recording":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.Recording(context.Background(), "pawly/agent-01")
				if err != nil || !status.Recording {
					t.Errorf("Recording() = %+v, %v; want Recording true, nil", status, err)
				}
			},
		},
		{
			name: "StartRecording", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/record/start",
			body: `{"name":"demo"}`,
			resp: `{"recording":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.StartRecording(context.Background(), "pawly/agent-01", RecordRequest{Name: "demo"})
				if err != nil || !status.Recording {
					t.Errorf("StartRecording() = %+v, %v; want Recording true, nil", status, err)
				}
			},
		},
		{
			name: "StopRecording", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/record/stop",
			resp: `{"id":"m4","kind":"recording"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.StopRecording(context.Background(), "pawly/agent-01")
				if err != nil || item.ID != "m4" {
					t.Errorf("StopRecording() = %+v, %v; want ID m4, nil", item, err)
				}
			},
		},
		{
			name: "AddMedia", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/add",
			body: `{"path":"/tmp/x.png"}`,
			resp: `{"id":"m5"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.AddMedia(context.Background(), "pawly/agent-01", AddMediaRequest{Path: "/tmp/x.png"})
				if err != nil || item.ID != "m5" {
					t.Errorf("AddMedia() = %+v, %v; want ID m5, nil", item, err)
				}
			},
		},
		{
			name: "AddNote", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/note",
			body: `{"text":"looks good"}`,
			resp: `{"id":"m6"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.AddNote(context.Background(), "pawly/agent-01", NoteRequest{Text: "looks good"})
				if err != nil || item.ID != "m6" {
					t.Errorf("AddNote() = %+v, %v; want ID m6, nil", item, err)
				}
			},
		},
		{
			name: "AddLogs", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/logs",
			body: `{"service":"web"}`,
			resp: `{"id":"m7"}`,
			run: func(t *testing.T, c *Client) {
				item, err := c.AddLogs(context.Background(), "pawly/agent-01", LogsRequest{Service: "web"})
				if err != nil || item.ID != "m7" {
					t.Errorf("AddLogs() = %+v, %v; want ID m7, nil", item, err)
				}
			},
		},
		{
			name: "ExportMedia", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/media/export",
			body: `{}`,
			resp: `{"dir":"/tmp/export","items":3}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.ExportMedia(context.Background(), "pawly/agent-01", ExportRequest{})
				if err != nil || res.Dir != "/tmp/export" || res.Items != 3 {
					t.Errorf("ExportMedia() = %+v, %v; want dir /tmp/export, 3 items, nil", res, err)
				}
			},
		},
		{
			name: "UpdateAgent", method: http.MethodPatch, path: "/v1/agents/pawly/agent-01",
			body: `{"title":"New title"}`,
			resp: `{"ref":"pawly/agent-01","title":"New title"}`,
			run: func(t *testing.T, c *Client) {
				title := "New title"
				a, err := c.UpdateAgent(context.Background(), "pawly/agent-01", UpdateAgentRequest{Title: &title})
				if err != nil || a.Title != "New title" {
					t.Errorf("UpdateAgent() = %+v, %v; want the new title, nil", a, err)
				}
			},
		},
		{
			name: "Browser", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/browser",
			resp: `{"running":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.Browser(context.Background(), "pawly/agent-01")
				if err != nil || !status.Running {
					t.Errorf("Browser() = %+v, %v; want Running true, nil", status, err)
				}
			},
		},
		{
			name: "BrowserAction", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/browser/start",
			resp: `{"running":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.BrowserAction(context.Background(), "pawly/agent-01", "start")
				if err != nil || !status.Running {
					t.Errorf("BrowserAction() = %+v, %v; want Running true, nil", status, err)
				}
			},
		},
		{
			name: "OpenInBrowser", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/browser/open",
			body: `{"url":"https://example.com"}`,
			resp: `{"running":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.OpenInBrowser(context.Background(), "pawly/agent-01", "https://example.com")
				if err != nil || !status.Running {
					t.Errorf("OpenInBrowser() = %+v, %v; want Running true, nil", status, err)
				}
			},
		},
		{
			name: "Android", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/android",
			resp: `{"running":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.Android(context.Background(), "pawly/agent-01")
				if err != nil || !status.Running {
					t.Errorf("Android() = %+v, %v; want Running true, nil", status, err)
				}
			},
		},
		{
			name: "StartAndroid", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/android/start",
			body: `{}`,
			resp: `{"running":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.StartAndroid(context.Background(), "pawly/agent-01", AndroidStartRequest{})
				if err != nil || !status.Running {
					t.Errorf("StartAndroid() = %+v, %v; want Running true, nil", status, err)
				}
			},
		},
		{
			name: "StopAndroid", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/android/stop",
			resp: `{"running":false}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.StopAndroid(context.Background(), "pawly/agent-01")
				if err != nil || status.Running {
					t.Errorf("StopAndroid() = %+v, %v; want Running false, nil", status, err)
				}
			},
		},
		{
			name: "InstallAPK", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/android/install",
			body: `{"path":"/tmp/app.apk"}`,
			resp: `{"output":"Success"}`,
			run: func(t *testing.T, c *Client) {
				res, err := c.InstallAPK(context.Background(), "pawly/agent-01", AndroidInstallRequest{Path: "/tmp/app.apk"})
				if err != nil || res.Output != "Success" {
					t.Errorf("InstallAPK() = %+v, %v; want output Success, nil", res, err)
				}
			},
		},
		{
			name: "Preview", method: http.MethodGet, path: "/v1/preview",
			resp: `{"addr":"0.0.0.0:7777"}`,
			run: func(t *testing.T, c *Client) {
				info, err := c.Preview(context.Background())
				if err != nil || info.Addr != "0.0.0.0:7777" {
					t.Errorf("Preview() = %+v, %v; want the preview addr, nil", info, err)
				}
			},
		},
		{
			name: "Ping", method: http.MethodGet, path: "/v1/version",
			resp: `{"version":"dev"}`,
			run: func(t *testing.T, c *Client) {
				if err := c.Ping(context.Background()); err != nil {
					t.Errorf("Ping() = %v", err)
				}
			},
		},
		{
			name: "Update", method: http.MethodGet, path: "/v1/update",
			resp: `{"current":"0.3.0","enabled":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.Update(context.Background())
				if err != nil || !status.Enabled {
					t.Errorf("Update() = %+v, %v; want Enabled true, nil", status, err)
				}
			},
		},
		{
			name: "Shutdown", method: http.MethodPost, path: "/v1/shutdown",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.Shutdown(context.Background()); err != nil {
					t.Errorf("Shutdown() = %v", err)
				}
			},
		},
		{
			name: "Projects", method: http.MethodGet, path: "/v1/projects",
			resp: `[{"name":"pawly"}]`,
			run: func(t *testing.T, c *Client) {
				projects, err := c.Projects(context.Background())
				if err != nil || len(projects) != 1 || projects[0].Name != "pawly" {
					t.Errorf("Projects() = %+v, %v; want [{pawly}], nil", projects, err)
				}
			},
		},
		{
			name: "AddProject", method: http.MethodPost, path: "/v1/projects",
			body: `{"path":"/home/x/pawly","name":"pawly"}`,
			resp: `{"name":"pawly"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.AddProject(context.Background(), AddProjectRequest{Path: "/home/x/pawly", Name: "pawly"})
				if err != nil || p.Name != "pawly" {
					t.Errorf("AddProject() = %+v, %v; want pawly, nil", p, err)
				}
			},
		},
		{
			name: "Project", method: http.MethodGet, path: "/v1/projects/pawly",
			resp: `{"name":"pawly","branch":"main"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.Project(context.Background(), "pawly")
				if err != nil || p.Branch != "main" {
					t.Errorf("Project() = %+v, %v; want branch main, nil", p, err)
				}
			},
		},
		{
			name: "UpdateProject", method: http.MethodPatch, path: "/v1/projects/pawly",
			body: `{}`,
			resp: `{"name":"pawly"}`,
			run: func(t *testing.T, c *Client) {
				p, err := c.UpdateProject(context.Background(), "pawly", UpdateProjectRequest{})
				if err != nil || p.Name != "pawly" {
					t.Errorf("UpdateProject() = %+v, %v; want pawly, nil", p, err)
				}
			},
		},
		{
			name: "RemoveProject", method: http.MethodDelete, path: "/v1/projects/pawly",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemoveProject(context.Background(), "pawly"); err != nil {
					t.Errorf("RemoveProject() = %v", err)
				}
			},
		},
		{
			name: "Brief", method: http.MethodGet, path: "/v1/projects/pawly/brief?agent=agent-01",
			resp: `the brief's markdown`,
			run: func(t *testing.T, c *Client) {
				brief, err := c.Brief(context.Background(), "pawly", "agent-01")
				if err != nil || brief != "the brief's markdown" {
					t.Errorf("Brief() = %q, %v; want the raw markdown, nil", brief, err)
				}
			},
		},
		{
			name: "Notes", method: http.MethodGet, path: "/v1/projects/pawly/notes",
			resp: `{"text":"what to know"}`,
			run: func(t *testing.T, c *Client) {
				notes, err := c.Notes(context.Background(), "pawly")
				if err != nil || notes.Text != "what to know" {
					t.Errorf("Notes() = %+v, %v; want the text, nil", notes, err)
				}
			},
		},
		{
			name: "SetNotes", method: http.MethodPut, path: "/v1/projects/pawly/notes",
			body: `{"text":"updated"}`,
			resp: `{"text":"updated"}`,
			run: func(t *testing.T, c *Client) {
				notes, err := c.SetNotes(context.Background(), "pawly", "updated")
				if err != nil || notes.Text != "updated" {
					t.Errorf("SetNotes() = %+v, %v; want updated, nil", notes, err)
				}
			},
		},
		{
			name: "SaveBase", method: http.MethodPost, path: "/v1/projects/pawly/base",
			body: `{"agent":"agent-01"}`,
			resp: `{"id":"job-2","status":"running"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.SaveBase(context.Background(), "pawly", "agent-01")
				if err != nil || job.ID != "job-2" {
					t.Errorf("SaveBase() = %+v, %v; want ID job-2, nil", job, err)
				}
			},
		},
		{
			name: "RemoveBase", method: http.MethodDelete, path: "/v1/projects/pawly/base",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemoveBase(context.Background(), "pawly"); err != nil {
					t.Errorf("RemoveBase() = %v", err)
				}
			},
		},
		{
			name: "RevertBase", method: http.MethodPost, path: "/v1/projects/pawly/base/revert",
			resp: `{"snapshot":"snap-1"}`,
			run: func(t *testing.T, c *Client) {
				base, err := c.RevertBase(context.Background(), "pawly")
				if err != nil || base.Snapshot != "snap-1" {
					t.Errorf("RevertBase() = %+v, %v; want snapshot snap-1, nil", base, err)
				}
			},
		},
		{
			name: "RemovePreviousBase", method: http.MethodDelete, path: "/v1/projects/pawly/base?previous=1",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.RemovePreviousBase(context.Background(), "pawly"); err != nil {
					t.Errorf("RemovePreviousBase() = %v", err)
				}
			},
		},
		{
			name: "Agents", method: http.MethodGet, path: "/v1/agents?project=pawly",
			resp: `[{"ref":"pawly/agent-01"}]`,
			run: func(t *testing.T, c *Client) {
				agents, err := c.Agents(context.Background(), "pawly")
				if err != nil || len(agents) != 1 {
					t.Errorf("Agents() = %+v, %v; want 1 agent, nil", agents, err)
				}
			},
		},
		{
			name: "Agent", method: http.MethodGet, path: "/v1/agents/pawly/agent-01",
			resp: `{"ref":"pawly/agent-01","title":"Fix login"}`,
			run: func(t *testing.T, c *Client) {
				a, err := c.Agent(context.Background(), "pawly/agent-01")
				if err != nil || a.Title != "Fix login" {
					t.Errorf("Agent() = %+v, %v; want title Fix login, nil", a, err)
				}
			},
		},
		{
			name: "CreateAgent", method: http.MethodPost, path: "/v1/agents",
			body: `{"project":"pawly","ai":""}`,
			resp: `{"id":"job-3","status":"running"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.CreateAgent(context.Background(), CreateAgentRequest{Project: "pawly"})
				if err != nil || job.ID != "job-3" {
					t.Errorf("CreateAgent() = %+v, %v; want ID job-3, nil", job, err)
				}
			},
		},
		{
			name: "DestroyAgent", method: http.MethodDelete,
			path: "/v1/agents/pawly/agent-01?deleteBranch=true&deleteMedia=false&force=true",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.DestroyAgent(context.Background(), "pawly/agent-01", true, true, false); err != nil {
					t.Errorf("DestroyAgent() = %v", err)
				}
			},
		},
		{
			name: "AgentAction", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/pause",
			resp: `{"ref":"pawly/agent-01"}`,
			run: func(t *testing.T, c *Client) {
				a, err := c.AgentAction(context.Background(), "pawly/agent-01", "pause")
				if err != nil || a.Ref != "pawly/agent-01" {
					t.Errorf("AgentAction() = %+v, %v; want the ref back, nil", a, err)
				}
			},
		},
		{
			name: "Diff", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/diff?stat=true",
			resp: `3 files changed`,
			run: func(t *testing.T, c *Client) {
				diff, err := c.Diff(context.Background(), "pawly/agent-01", true)
				if err != nil || diff != "3 files changed" {
					t.Errorf("Diff() = %q, %v; want the raw diff, nil", diff, err)
				}
			},
		},
		{
			name: "Snapshots", method: http.MethodGet, path: "/v1/agents/pawly/agent-01/snapshots",
			resp: `[{"name":"before-refactor"}]`,
			run: func(t *testing.T, c *Client) {
				snaps, err := c.Snapshots(context.Background(), "pawly/agent-01")
				if err != nil || len(snaps) != 1 || snaps[0].Name != "before-refactor" {
					t.Errorf("Snapshots() = %+v, %v; want [before-refactor], nil", snaps, err)
				}
			},
		},
		{
			name: "TakeSnapshot", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/snapshots",
			body: `{"name":"checkpoint"}`,
			resp: `{"name":"checkpoint"}`,
			run: func(t *testing.T, c *Client) {
				snap, err := c.TakeSnapshot(context.Background(), "pawly/agent-01", SnapshotRequest{Name: "checkpoint"})
				if err != nil || snap.Name != "checkpoint" {
					t.Errorf("TakeSnapshot() = %+v, %v; want checkpoint, nil", snap, err)
				}
			},
		},
		{
			name: "DeleteSnapshot", method: http.MethodDelete, path: "/v1/agents/pawly/agent-01/snapshots/checkpoint",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.DeleteSnapshot(context.Background(), "pawly/agent-01", "checkpoint"); err != nil {
					t.Errorf("DeleteSnapshot() = %v", err)
				}
			},
		},
		{
			name: "Restore", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/restore",
			body: `{"snapshot":"checkpoint"}`,
			resp: `{"id":"job-4"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.Restore(context.Background(), "pawly/agent-01", "checkpoint")
				if err != nil || job.ID != "job-4" {
					t.Errorf("Restore() = %+v, %v; want ID job-4, nil", job, err)
				}
			},
		},
		{
			name: "Fork", method: http.MethodPost, path: "/v1/agents/pawly/agent-01/fork",
			body: `{}`,
			resp: `{"id":"job-5"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.Fork(context.Background(), "pawly/agent-01", ForkRequest{})
				if err != nil || job.ID != "job-5" {
					t.Errorf("Fork() = %+v, %v; want ID job-5, nil", job, err)
				}
			},
		},
		{
			name: "Usage", method: http.MethodGet, path: "/v1/usage?interval=1h0m0s",
			resp: `{}`,
			run: func(t *testing.T, c *Client) {
				if _, err := c.Usage(context.Background(), time.Hour); err != nil {
					t.Errorf("Usage() = %v", err)
				}
			},
		},
		{
			name: "DiskUsage", method: http.MethodGet, path: "/v1/usage/disk",
			resp: `{"total":1024}`,
			run: func(t *testing.T, c *Client) {
				usage, err := c.DiskUsage(context.Background())
				if err != nil || usage.Total != 1024 {
					t.Errorf("DiskUsage() = %+v, %v; want Total 1024, nil", usage, err)
				}
			},
		},
		{
			name: "ImageReady true", method: http.MethodGet, path: "/v1/image",
			resp: `{"ready":true}`,
			run: func(t *testing.T, c *Client) {
				ready, err := c.ImageReady(context.Background())
				if err != nil || !ready {
					t.Errorf("ImageReady() = %v, %v; want true, nil", ready, err)
				}
			},
		},
		{
			name: "BuildImage", method: http.MethodPost, path: "/v1/image/build",
			body: `{}`,
			resp: `{"id":"job-6"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.BuildImage(context.Background(), BuildImageRequest{})
				if err != nil || job.ID != "job-6" {
					t.Errorf("BuildImage() = %+v, %v; want ID job-6, nil", job, err)
				}
			},
		},
		{
			name: "Auth", method: http.MethodGet, path: "/v1/auth",
			resp: `{}`,
			run: func(t *testing.T, c *Client) {
				if _, err := c.Auth(context.Background()); err != nil {
					t.Errorf("Auth() = %v", err)
				}
			},
		},
		{
			name: "Jobs", method: http.MethodGet, path: "/v1/jobs",
			resp: `[{"id":"job-1"}]`,
			run: func(t *testing.T, c *Client) {
				jobs, err := c.Jobs(context.Background())
				if err != nil || len(jobs) != 1 {
					t.Errorf("Jobs() = %+v, %v; want 1 job, nil", jobs, err)
				}
			},
		},
		{
			name: "Job", method: http.MethodGet, path: "/v1/jobs/job-1",
			resp: `{"id":"job-1","status":"running"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.Job(context.Background(), "job-1")
				if err != nil || job.Done() {
					t.Errorf("Job() = %+v, %v; want a running (not Done) job, nil", job, err)
				}
			},
		},
		{
			name: "CancelJob", method: http.MethodPost, path: "/v1/jobs/job-1/cancel",
			resp: `{"id":"job-1","status":"cancelled"}`,
			run: func(t *testing.T, c *Client) {
				job, err := c.CancelJob(context.Background(), "job-1")
				if err != nil || !job.Done() {
					t.Errorf("CancelJob() = %+v, %v; want a Done job, nil", job, err)
				}
			},
		},
		{
			name: "JobLog", method: http.MethodGet, path: "/v1/jobs/job-1/log",
			resp: `line one`,
			run: func(t *testing.T, c *Client) {
				log, err := c.JobLog(context.Background(), "job-1")
				if err != nil || log != "line one" {
					t.Errorf("JobLog() = %q, %v; want the raw log, nil", log, err)
				}
			},
		},
		{
			name: "RemoteStatus", method: http.MethodGet, path: "/v1/remote",
			resp: `{"connected":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.RemoteStatus(context.Background())
				if err != nil || !status.Connected {
					t.Errorf("RemoteStatus() = %+v, %v; want Connected true, nil", status, err)
				}
			},
		},
		{
			name: "ConnectRemote", method: http.MethodPut, path: "/v1/remote",
			body: `{"hub":"https://hub.example.com","token":"tok"}`,
			resp: `{"connected":true}`,
			run: func(t *testing.T, c *Client) {
				status, err := c.ConnectRemote(context.Background(), RemoteConnectRequest{Hub: "https://hub.example.com", Token: "tok"})
				if err != nil || !status.Connected {
					t.Errorf("ConnectRemote() = %+v, %v; want Connected true, nil", status, err)
				}
			},
		},
		{
			name: "DisconnectRemote", method: http.MethodDelete, path: "/v1/remote",
			resp: ``,
			run: func(t *testing.T, c *Client) {
				if err := c.DisconnectRemote(context.Background()); err != nil {
					t.Errorf("DisconnectRemote() = %v", err)
				}
			},
		},
		{
			name: "Self", method: http.MethodGet, path: "/v1/self",
			resp: `{"ref":"pawly/agent-01","agent":"agent-01"}`,
			run: func(t *testing.T, c *Client) {
				self, err := c.Self(context.Background())
				if err != nil || self.Agent != "agent-01" {
					t.Errorf("Self() = %+v, %v; want agent agent-01, nil", self, err)
				}
			},
		},
	})
}
